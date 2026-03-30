package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
)

type FunnelDomainCreateRequest struct {
	Domain         string `json:"domain"`
	DomainType     string `json:"domainType"`
	ListenerMode   string `json:"listenerMode"`
	EdgeMode       string `json:"edgeMode"`
	TLSMode        string `json:"tlsMode"`
	HTTPPort       int    `json:"httpPort"`
	HTTPSPort      int    `json:"httpsPort"`
	ValidationMode string `json:"validationMethod"`
}

type FunnelServiceCreateRequest struct {
	MachineID      int64  `json:"machineId"`
	DomainID       int64  `json:"domainId"`
	DomainMode     string `json:"domainMode"`
	ListenProto    string `json:"listenProto"`
	ListenPort     int    `json:"listenPort"`
	MountPath      string `json:"mountPath"`
	BackendType    string `json:"backendType"`
	BackendScheme  string `json:"backendScheme"`
	BackendPort    int    `json:"backendPort"`
	Enabled        *bool  `json:"enabled"`
	BackendTailnet string `json:"backendTailnetIp"`
}

func (req *FunnelServiceCreateRequest) UnmarshalJSON(data []byte) error {
	type rawFunnelServiceCreateRequest struct {
		MachineID      FunnelFlexibleID `json:"machineId"`
		DomainID       FunnelFlexibleID `json:"domainId"`
		DomainMode     string           `json:"domainMode"`
		ListenProto    string           `json:"listenProto"`
		ListenPort     int              `json:"listenPort"`
		MountPath      string           `json:"mountPath"`
		BackendType    string           `json:"backendType"`
		BackendScheme  string           `json:"backendScheme"`
		BackendPort    int              `json:"backendPort"`
		Enabled        *bool            `json:"enabled"`
		BackendTailnet string           `json:"backendTailnetIp"`
	}

	raw := rawFunnelServiceCreateRequest{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	req.MachineID = raw.MachineID.Int64()
	req.DomainID = raw.DomainID.Int64()
	req.DomainMode = raw.DomainMode
	req.ListenProto = raw.ListenProto
	req.ListenPort = raw.ListenPort
	req.MountPath = raw.MountPath
	req.BackendType = raw.BackendType
	req.BackendScheme = raw.BackendScheme
	req.BackendPort = raw.BackendPort
	req.Enabled = raw.Enabled
	req.BackendTailnet = raw.BackendTailnet
	return nil
}

type FunnelServicePatchRequest struct {
	MountPath     *string `json:"mountPath"`
	BackendScheme *string `json:"backendScheme"`
	BackendPort   *int    `json:"backendPort"`
	Enabled       *bool   `json:"enabled"`
}

func (h *Mirage) requireTenantFunnelUser(w http.ResponseWriter, r *http.Request) (*User, bool) {
	user, err := h.verifyTokenIDandGetUser(w, r)
	if err != nil || user.CheckEmpty() {
		h.doAPIResponse(w, "用户信息核对失败:"+err.Error(), nil)
		return nil, false
	}
	return user, true
}

func (h *Mirage) requireTenantFunnelOwner(w http.ResponseWriter, r *http.Request) (*User, bool) {
	user, ok := h.requireTenantFunnelUser(w, r)
	if !ok {
		return nil, false
	}
	if user.Role != RoleOwner {
		h.doAPIResponse(w, "权限不足", nil)
		return nil, false
	}
	return user, true
}

func (h *Mirage) CAPIGetFunnelDomains(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelUser(w, r)
	if !ok {
		return
	}
	cfg, err := effectiveFunnelPlatformConfigFromDB(h.db, h.cfg.BaseDomain)
	if err != nil {
		h.doAPIResponse(w, "读取Funnel平台配置失败:"+err.Error(), nil)
		return
	}
	domains, err := h.listFunnelDomainsByOrgID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "读取Funnel域名失败:"+err.Error(), nil)
		return
	}
	h.doAPIResponse(w, "", map[string]any{
		"domains": funnelDomainsResponseWithConfig(domains, &cfg, h.lookupTenantFunnelEdge),
	})
}

func (h *Mirage) CAPIPostFunnelDomains(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelOwner(w, r)
	if !ok {
		return
	}
	req := FunnelDomainCreateRequest{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}
	domain, cert, err := h.createTenantFunnelDomain(user, req)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	if err := recordFunnelAudit(h.db, user.OrganizationID, "tenant_owner", user.StableID, "domain", domain.StableID, "domain_created", domainActionPayload(domain, map[string]any{"cert": funnelCertToMap(cert)})); err != nil {
		h.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	cfg, cfgErr := effectiveFunnelPlatformConfigFromDB(h.db, h.cfg.BaseDomain)
	if cfgErr != nil {
		h.doAPIResponse(w, "读取Funnel平台配置失败:"+cfgErr.Error(), nil)
		return
	}
	h.doAPIResponse(w, "", map[string]any{
		"domain":                 funnelDomainToMapWithConfig(domain, &cfg, h.lookupTenantFunnelEdge(domain)),
		"cert":                   serializeFunnelCertResponse(cert),
		"validationInstructions": funnelDomainValidationInstructions(domain),
	})
}

func (h *Mirage) CAPIVerifyFunnelDomain(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelOwner(w, r)
	if !ok {
		return
	}
	domain, err := h.loadTenantFunnelDomain(r, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	cfg, cfgErr := effectiveFunnelPlatformConfigFromDB(h.db, h.cfg.BaseDomain)
	if cfgErr != nil {
		h.doAPIResponse(w, "读取Funnel平台配置失败:"+cfgErr.Error(), nil)
		return
	}
	edge := h.lookupTenantFunnelEdge(domain)
	response := funnelDomainWithVerifiedFlag(domain)
	if domain.DomainType == FunnelDomainTypeManaged {
		provider, err := h.currentManagedFunnelDNSProvider()
		if err != nil {
			h.doAPIResponse(w, "更新Funnel域名失败:"+err.Error(), nil)
			return
		}
		result, err := verifyManagedFunnelDomain(h.db, domain, provider)
		if err != nil {
			h.doAPIResponse(w, "更新Funnel域名失败:"+err.Error(), nil)
			return
		}
		response = funnelDomainVerificationResponse(domain, result.Ready, false, result.Message)
		if result.Ready {
			if certState := funnelManagedCertAutomationState(&cfg, domain, edge); certState != nil && !certState.Eligible {
				response["verificationMessage"] = fmt.Sprintf("DNS 已就绪，但%s。%s", certState.Reason, certState.NextAction)
			} else if rt := h.currentFunnelRuntime(); rt != nil {
				if err := rt.requestManagedCertificate(domain.Domain); err == nil {
					response["verificationMessage"] = "DNS 已就绪，已开始尝试签发证书"
				}
			}
		}
	} else {
		if err := markFunnelDomainVerified(h.db, domain); err != nil {
			h.doAPIResponse(w, "更新Funnel域名失败:"+err.Error(), nil)
			return
		}
	}
	if err := recordFunnelAudit(h.db, user.OrganizationID, "tenant_owner", user.StableID, "domain", domain.StableID, "domain_verify_requested", domainActionPayload(domain, nil)); err != nil {
		h.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	h.requestFunnelRuntimeReload()
	response["domain"] = funnelDomainToMapWithConfig(domain, &cfg, edge)
	h.doAPIResponse(w, "", response)
}

func (h *Mirage) CAPIDeleteFunnelDomain(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelOwner(w, r)
	if !ok {
		return
	}
	domain, err := h.loadTenantFunnelDomain(r, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	if err := h.deleteFunnelDomainWithChildren(domain); err != nil {
		h.doAPIResponse(w, "删除Funnel域名失败:"+err.Error(), nil)
		return
	}
	if err := recordFunnelAudit(h.db, user.OrganizationID, "tenant_owner", user.StableID, "domain", domain.StableID, "domain_deleted", domainActionPayload(domain, nil)); err != nil {
		h.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	h.requestFunnelRuntimeReload()
	h.doAPIResponse(w, "", map[string]any{"deleted": true})
}

func (h *Mirage) CAPIGetFunnelServices(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelUser(w, r)
	if !ok {
		return
	}
	services, err := h.listFunnelServicesByOrgID(user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, "读取Funnel服务失败:"+err.Error(), nil)
		return
	}
	payload := make([]map[string]any, 0, len(services))
	for i := range services {
		status, err := h.buildTenantFunnelServiceStatus(&services[i])
		if err != nil {
			h.doAPIResponse(w, err.Error(), nil)
			return
		}
		payload = append(payload, status)
	}
	h.doAPIResponse(w, "", map[string]any{"services": payload})
}

func (h *Mirage) CAPIPostFunnelServices(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelOwner(w, r)
	if !ok {
		return
	}
	req := FunnelServiceCreateRequest{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}
	service, domain, cert, err := h.createTenantFunnelService(user, req)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	if err := recordFunnelAudit(h.db, user.OrganizationID, "tenant_owner", user.StableID, "service", service.StableID, "service_created", map[string]any{"service": funnelServiceToMap(service), "domain": funnelDomainToMap(domain), "cert": funnelCertToMap(cert)}); err != nil {
		h.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	h.requestFunnelRuntimeReload()
	status, err := h.buildTenantFunnelServiceStatus(service)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	h.doAPIResponse(w, "", status)
}

func (h *Mirage) CAPIPatchFunnelService(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelOwner(w, r)
	if !ok {
		return
	}
	service, domain, cert, err := h.loadTenantFunnelService(r, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	req := FunnelServicePatchRequest{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}
	if req.MountPath != nil {
		service.MountPath = normalizeFunnelMountPath(*req.MountPath)
	}
	if req.BackendScheme != nil {
		service.BackendScheme = strings.ToLower(strings.TrimSpace(*req.BackendScheme))
	}
	if req.BackendPort != nil {
		service.BackendPort = *req.BackendPort
	}
	if req.Enabled != nil {
		service.Enabled = *req.Enabled
	}
	if err := applyTenantFunnelServiceProjection(service); err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	if err := h.db.Save(service).Error; err != nil {
		h.doAPIResponse(w, "更新Funnel服务失败:"+err.Error(), nil)
		return
	}
	if err := recordFunnelAudit(h.db, user.OrganizationID, "tenant_owner", user.StableID, "service", service.StableID, "service_updated", map[string]any{"service": funnelServiceToMap(service)}); err != nil {
		h.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	h.requestFunnelRuntimeReload()
	h.doAPIResponse(w, "", funnelServiceStatusResponse(service, domain, cert, h.lookupTenantFunnelEdge(domain)))
}

func (h *Mirage) CAPIEnableFunnelService(w http.ResponseWriter, r *http.Request) {
	h.updateTenantFunnelServiceEnabled(w, r, true)
}

func (h *Mirage) CAPIDisableFunnelService(w http.ResponseWriter, r *http.Request) {
	h.updateTenantFunnelServiceEnabled(w, r, false)
}

func (h *Mirage) CAPIDeleteFunnelService(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelOwner(w, r)
	if !ok {
		return
	}
	service, domain, cert, err := h.loadTenantFunnelService(r, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	if err := h.db.Delete(&FunnelService{}, service.ID).Error; err != nil {
		h.doAPIResponse(w, "删除Funnel服务失败:"+err.Error(), nil)
		return
	}
	if err := recordFunnelAudit(h.db, user.OrganizationID, "tenant_owner", user.StableID, "service", service.StableID, "service_deleted", map[string]any{"service": funnelServiceToMap(service)}); err != nil {
		h.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	h.requestFunnelRuntimeReload()
	h.doAPIResponse(w, "", funnelServiceStatusResponse(service, domain, cert, h.lookupTenantFunnelEdge(domain)))
}

func (h *Mirage) CAPIGetFunnelServiceStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelUser(w, r)
	if !ok {
		return
	}
	service, err := h.loadTenantFunnelServiceByID(r, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	status, err := h.buildTenantFunnelServiceStatus(service)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	h.doAPIResponse(w, "", status)
}

func (h *Mirage) CAPIGetFunnelServiceLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireTenantFunnelUser(w, r)
	if !ok {
		return
	}
	service, err := h.loadTenantFunnelServiceByID(r, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	logs := []any{}
	if rt := h.currentFunnelRuntime(); rt != nil {
		logs = rt.logsForService(service.ID)
	}
	h.doAPIResponse(w, "", map[string]any{
		"service": funnelServiceToMap(service),
		"logs":    logs,
	})
}

func (h *Mirage) updateTenantFunnelServiceEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	user, ok := h.requireTenantFunnelOwner(w, r)
	if !ok {
		return
	}
	service, domain, cert, err := h.loadTenantFunnelService(r, user.OrganizationID)
	if err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	service.Enabled = enabled
	if err := applyTenantFunnelServiceProjection(service); err != nil {
		h.doAPIResponse(w, err.Error(), nil)
		return
	}
	if err := h.db.Save(service).Error; err != nil {
		h.doAPIResponse(w, "更新Funnel服务失败:"+err.Error(), nil)
		return
	}
	action := "service_enabled"
	if !enabled {
		action = "service_disabled"
	}
	if err := recordFunnelAudit(h.db, user.OrganizationID, "tenant_owner", user.StableID, "service", service.StableID, action, map[string]any{"service": funnelServiceToMap(service)}); err != nil {
		h.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	h.requestFunnelRuntimeReload()
	h.doAPIResponse(w, "", funnelServiceStatusResponse(service, domain, cert, h.lookupTenantFunnelEdge(domain)))
}

func (h *Mirage) loadTenantFunnelDomain(r *http.Request, orgID int64) (*FunnelDomain, error) {
	id, err := parseFunnelID(mux.Vars(r)["id"])
	if err != nil {
		return nil, fmt.Errorf("域名ID解析失败: %w", err)
	}
	domain := &FunnelDomain{}
	if err := h.db.First(domain, id).Error; err != nil {
		return nil, fmt.Errorf("未找到Funnel域名: %w", err)
	}
	if domain.OrgID != orgID {
		return nil, fmt.Errorf("无权访问该Funnel域名")
	}
	return domain, nil
}

func (h *Mirage) loadTenantFunnelServiceByID(r *http.Request, orgID int64) (*FunnelService, error) {
	id, err := parseFunnelID(mux.Vars(r)["id"])
	if err != nil {
		return nil, fmt.Errorf("服务ID解析失败: %w", err)
	}
	service := &FunnelService{}
	if err := h.db.First(service, id).Error; err != nil {
		return nil, fmt.Errorf("未找到Funnel服务: %w", err)
	}
	if service.OrgID != orgID {
		return nil, fmt.Errorf("无权访问该Funnel服务")
	}
	return service, nil
}

func (h *Mirage) loadTenantFunnelService(r *http.Request, orgID int64) (*FunnelService, *FunnelDomain, *FunnelCert, error) {
	service, err := h.loadTenantFunnelServiceByID(r, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	domain, err := h.getTenantFunnelDomainByID(service.DomainID, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := h.getTenantFunnelCertByDomainID(domain.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		cert = &FunnelCert{DomainID: domain.ID, CertStatus: FunnelCertStatusPending}
	}
	return service, domain, cert, nil
}

func (h *Mirage) createTenantFunnelDomain(user *User, req FunnelDomainCreateRequest) (*FunnelDomain, *FunnelCert, error) {
	domainType := strings.ToLower(strings.TrimSpace(req.DomainType))
	if domainType == "" {
		domainType = FunnelDomainTypeCustom
	}
	cfg := req
	if strings.TrimSpace(cfg.ListenerMode) == "" {
		cfg.ListenerMode = FunnelListenerModeDirect
	}
	if strings.TrimSpace(cfg.EdgeMode) == "" {
		cfg.EdgeMode = FunnelEdgeModeServer
	}
	if strings.TrimSpace(cfg.TLSMode) == "" {
		cfg.TLSMode = FunnelTLSModePlatformManaged
	}
	if domainType == FunnelDomainTypeManaged {
		org := &user.Organization
		if org.ID == 0 {
			loadedOrg, err := h.GetOrgnaizationByID(user.OrganizationID)
			if err != nil {
				return nil, nil, err
			}
			org = loadedOrg
		}
		funnelCfg, err := effectiveFunnelPlatformConfigFromDB(h.db, h.cfg.BaseDomain)
		if err != nil {
			return nil, nil, err
		}
		managedDomainName, err := allocateManagedFunnelDomainForOrg(h.db, org, funnelCfg)
		if err != nil {
			return nil, nil, err
		}
		listenPort := cfg.HTTPSPort
		if listenPort <= 0 {
			listenPort = cfg.HTTPPort
		}
		if listenPort <= 0 {
			listenPort = 443
		}
		domain, err := h.createManagedFunnelDomain(user, managedDomainName, listenPort, cfg.EdgeMode, cfg.ListenerMode)
		if err != nil {
			return nil, nil, err
		}
		cert, err := h.getTenantFunnelCertByDomainID(domain.ID)
		if err != nil {
			return nil, nil, err
		}
		return domain, cert, nil
	}
	if domainType != FunnelDomainTypeCustom {
		return nil, nil, fmt.Errorf("不支持的Funnel域名类型")
	}
	domainName := normalizeFunnelBaseDomain(req.Domain)
	if domainName == "" {
		return nil, nil, fmt.Errorf("未指定Funnel域名")
	}
	if strings.Contains(domainName, "*") {
		return nil, nil, fmt.Errorf("当前不支持通配Funnel域名")
	}
	if err := h.db.Where("domain = ?", domainName).First(&FunnelDomain{}).Error; err == nil {
		return nil, nil, fmt.Errorf("该Funnel域名已被占用")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	domain := &FunnelDomain{
		OrgID:            user.OrganizationID,
		Domain:           domainName,
		DomainType:       FunnelDomainTypeCustom,
		Status:           FunnelDomainStatusPendingDNS,
		DNSStatus:        FunnelDNSStatusPending,
		TLSMode:          cfg.TLSMode,
		ListenerMode:     cfg.ListenerMode,
		EdgeMode:         cfg.EdgeMode,
		EdgeTargetID:     defaultFunnelEdgeTargetID(cfg.EdgeMode),
		HTTPPort:         cfg.HTTPPort,
		HTTPSPort:        cfg.HTTPSPort,
		ValidationMethod: chooseFunnelValidationMethod(cfg.ValidationMode),
		ValidationTarget: h.buildFunnelValidationTarget(domainName),
		ValidationToken:  GenerateRandomStringURLSafeOrEmpty(24),
	}
	if err := h.db.Create(domain).Error; err != nil {
		return nil, nil, err
	}
	cert := &FunnelCert{
		DomainID:      domain.ID,
		CertStatus:    FunnelCertStatusPending,
		ChallengeType: domain.ValidationMethod,
	}
	if err := h.db.Create(cert).Error; err != nil {
		return nil, nil, err
	}
	domain.CertID = &cert.ID
	if err := h.db.Save(domain).Error; err != nil {
		return nil, nil, err
	}
	return domain, cert, nil
}

func (h *Mirage) createTenantFunnelService(user *User, req FunnelServiceCreateRequest) (*FunnelService, *FunnelDomain, *FunnelCert, error) {
	org := &user.Organization
	if org.ID == 0 {
		loadedOrg, err := h.GetOrgnaizationByID(user.OrganizationID)
		if err != nil {
			return nil, nil, nil, err
		}
		org = loadedOrg
	}
	machine, err := h.GetMachineByID(req.MachineID)
	if err != nil {
		return nil, nil, nil, err
	}
	if machine.User.OrganizationID != user.OrganizationID {
		return nil, nil, nil, fmt.Errorf("无权使用该设备")
	}
	listenProto, err := normalizeFunnelListenProto(req.ListenProto)
	if err != nil {
		return nil, nil, nil, err
	}
	backendType := strings.ToLower(strings.TrimSpace(req.BackendType))
	if backendType == "" {
		return nil, nil, nil, fmt.Errorf("未指定Funnel后端类型")
	}
	if err := validateFunnelListenBackendPair(listenProto, backendType); err != nil {
		return nil, nil, nil, err
	}
	backendScheme, backendPort, err := normalizeFunnelServiceBackend(backendType, req.BackendScheme, req.BackendPort)
	if err != nil {
		return nil, nil, nil, err
	}
	listenPort, err := defaultFunnelListenPort(listenProto, req.ListenPort)
	if err != nil {
		return nil, nil, nil, err
	}
	mountPath := normalizeFunnelMountPath(req.MountPath)

	var domain *FunnelDomain
	switch strings.ToLower(strings.TrimSpace(req.DomainMode)) {
	case "", FunnelDomainTypeCustom, "existing":
		if req.DomainID == 0 {
			return nil, nil, nil, fmt.Errorf("未指定Funnel域名")
		}
		domain, err = h.getTenantFunnelDomainByID(req.DomainID, user.OrganizationID)
		if err != nil {
			return nil, nil, nil, err
		}
	case "managed":
		funnelCfg, err := effectiveFunnelPlatformConfigFromDB(h.db, h.cfg.BaseDomain)
		if err != nil {
			return nil, nil, nil, err
		}
		managedDomainName := allocateManagedFunnelDomain(org, machine, funnelCfg)
		if managedDomainName == "" {
			return nil, nil, nil, fmt.Errorf("无法生成托管Funnel域名")
		}
		domain, err = h.createManagedFunnelDomain(user, managedDomainName, listenPort, funnelCfg.DefaultEdgeMode, funnelCfg.DefaultListenerMode)
		if err != nil {
			return nil, nil, nil, err
		}
	default:
		return nil, nil, nil, fmt.Errorf("不支持的Funnel域名模式")
	}

	backendTailnetIP := req.BackendTailnet
	if backendTailnetIP == "" && len(machine.IPAddresses) > 0 {
		backendTailnetIP = machine.IPAddresses[0].String()
	}
	if backendTailnetIP == "" {
		return nil, nil, nil, fmt.Errorf("未找到设备尾网地址")
	}

	service := &FunnelService{
		OrgID:               user.OrganizationID,
		MachineID:           machine.ID,
		DomainID:            domain.ID,
		Enabled:             true,
		Public:              true,
		ListenProto:         listenProto,
		ListenPort:          listenPort,
		MountPath:           mountPath,
		BackendType:         backendType,
		BackendScheme:       backendScheme,
		BackendTailnetIP:    backendTailnetIP,
		BackendPort:         backendPort,
		TargetMachineOnline: machine.isOnline(),
	}
	if req.Enabled != nil {
		service.Enabled = *req.Enabled
	}
	if err := applyTenantFunnelServiceProjection(service); err != nil {
		return nil, nil, nil, err
	}
	if err := h.db.Create(service).Error; err != nil {
		return nil, nil, nil, err
	}
	cert, err := h.getTenantFunnelCertByDomainID(domain.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		cert = &FunnelCert{DomainID: domain.ID, CertStatus: FunnelCertStatusPending}
		if err := h.db.Create(cert).Error; err != nil {
			return nil, nil, nil, err
		}
		domain.CertID = &cert.ID
		if err := h.db.Save(domain).Error; err != nil {
			return nil, nil, nil, err
		}
	}
	return service, domain, cert, nil
}

func (h *Mirage) createManagedFunnelDomain(user *User, domainName string, listenPort int, edgeMode, listenerMode string) (*FunnelDomain, error) {
	if strings.TrimSpace(edgeMode) == "" {
		edgeMode = FunnelEdgeModeServer
	}
	if strings.TrimSpace(listenerMode) == "" {
		listenerMode = FunnelListenerModeDirect
	}
	provider, err := h.currentManagedFunnelDNSProvider()
	if err != nil {
		return nil, err
	}
	dnsCtx := context.Background()
	if h != nil && h.ctx != nil {
		dnsCtx = h.ctx
	}
	existing := &FunnelDomain{}
	if err := h.db.Where("domain = ?", domainName).First(existing).Error; err == nil {
		if existing.OrgID != user.OrganizationID {
			return nil, fmt.Errorf("该Funnel域名已被占用")
		}
		if provider != nil {
			if err := provider.EnsureManagedDomain(dnsCtx, existing.Domain); err != nil {
				return nil, err
			}
		}
		return existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	domain := &FunnelDomain{
		OrgID:            user.OrganizationID,
		Domain:           domainName,
		DomainType:       FunnelDomainTypeManaged,
		Status:           FunnelDomainStatusPendingCert,
		DNSStatus:        FunnelDNSStatusReady,
		TLSMode:          FunnelTLSModePlatformManaged,
		ListenerMode:     listenerMode,
		EdgeMode:         edgeMode,
		EdgeTargetID:     defaultFunnelEdgeTargetID(edgeMode),
		HTTPPort:         listenPort,
		HTTPSPort:        listenPort,
		ValidationMethod: "managed",
		ValidationTarget: domainName,
		ValidationToken:  GenerateRandomStringURLSafeOrEmpty(24),
	}
	if err := h.db.Create(domain).Error; err != nil {
		return nil, err
	}
	cert := &FunnelCert{
		DomainID:   domain.ID,
		CertStatus: FunnelCertStatusPending,
	}
	if err := h.db.Create(cert).Error; err != nil {
		_ = h.db.Delete(domain).Error
		return nil, err
	}
	domain.CertID = &cert.ID
	if err := h.db.Save(domain).Error; err != nil {
		_ = h.db.Where("domain_id = ?", domain.ID).Delete(&FunnelCert{}).Error
		_ = h.db.Delete(domain).Error
		return nil, err
	}
	if provider != nil {
		if err := provider.EnsureManagedDomain(dnsCtx, domain.Domain); err != nil {
			_ = h.db.Where("domain_id = ?", domain.ID).Delete(&FunnelCert{}).Error
			_ = h.db.Delete(domain).Error
			return nil, err
		}
	}
	return domain, nil
}

func (h *Mirage) getTenantFunnelDomainByID(id int64, orgID int64) (*FunnelDomain, error) {
	domain := &FunnelDomain{}
	if err := h.db.First(domain, id).Error; err != nil {
		return nil, fmt.Errorf("未找到Funnel域名: %w", err)
	}
	if domain.OrgID != orgID {
		return nil, fmt.Errorf("无权访问该Funnel域名")
	}
	return domain, nil
}

func (h *Mirage) getTenantFunnelCertByDomainID(domainID int64) (*FunnelCert, error) {
	cert := &FunnelCert{}
	if err := h.db.Where("domain_id = ?", domainID).First(cert).Error; err != nil {
		return nil, err
	}
	return cert, nil
}

func (h *Mirage) getTenantFunnelServiceByID(id int64, orgID int64) (*FunnelService, error) {
	service := &FunnelService{}
	if err := h.db.First(service, id).Error; err != nil {
		return nil, fmt.Errorf("未找到Funnel服务: %w", err)
	}
	if service.OrgID != orgID {
		return nil, fmt.Errorf("无权访问该Funnel服务")
	}
	return service, nil
}

func (h *Mirage) listFunnelDomainsByOrgID(orgID int64) ([]FunnelDomain, error) {
	var domains []FunnelDomain
	if err := h.db.Where("org_id = ?", orgID).Order("created_at DESC").Find(&domains).Error; err != nil {
		return nil, err
	}
	return domains, nil
}

func (h *Mirage) listFunnelServicesByOrgID(orgID int64) ([]FunnelService, error) {
	var services []FunnelService
	if err := h.db.Where("org_id = ?", orgID).Order("created_at DESC").Find(&services).Error; err != nil {
		return nil, err
	}
	return services, nil
}

func (h *Mirage) buildTenantFunnelServiceStatus(service *FunnelService) (map[string]any, error) {
	if service == nil {
		return nil, fmt.Errorf("未找到Funnel服务")
	}
	domain, err := h.getTenantFunnelDomainByID(service.DomainID, service.OrgID)
	if err != nil {
		return nil, err
	}
	cert, err := h.getTenantFunnelCertByDomainID(domain.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	edge := h.lookupTenantFunnelEdgeForService(domain, service)
	if domain != nil && domain.EdgeMode == FunnelEdgeModeServer {
		if rt := h.currentFunnelRuntime(); rt != nil {
			rt.applyStatusOverlay(service, domain, cert)
		}
	} else {
		h.applyRemoteEdgeProjection(service, domain, cert, edge)
	}
	applyFunnelDomainReadinessProjection(service, domain)
	cfg, err := effectiveFunnelPlatformConfigFromDB(h.db, h.cfg.BaseDomain)
	if err != nil {
		return nil, err
	}
	return funnelServiceStatusResponseWithConfig(service, domain, cert, edge, &cfg), nil
}

func applyFunnelDomainReadinessProjection(service *FunnelService, domain *FunnelDomain) {
	if service == nil || domain == nil || !service.Enabled {
		return
	}
	if funnelDomainDNSReady(domain) {
		domain.DNSStatus = FunnelDNSStatusReady
		return
	}
	if strings.TrimSpace(domain.DNSStatus) == "" {
		domain.DNSStatus = FunnelDNSStatusPending
	}
	if strings.TrimSpace(domain.Status) == "" {
		domain.Status = FunnelDomainStatusPendingDNS
	}
	service.ConfigStatus = FunnelServiceConfigStatusPending
	service.BackendStatus = FunnelServiceBackendStatusUnknown
	service.LastError = strings.TrimSpace(domain.LastDNSError)
	if service.LastError == "" {
		service.LastError = "域名尚未完成验证"
	}
}

func (h *Mirage) applyRemoteEdgeProjection(service *FunnelService, domain *FunnelDomain, cert *FunnelCert, edge *FunnelEdge) {
	if service == nil || domain == nil {
		return
	}
	if !service.Enabled {
		service.ConfigStatus = FunnelServiceConfigStatusDisabled
		service.EdgeStatus = FunnelServiceEdgeStatusUnavailable
		service.BackendStatus = FunnelServiceConfigStatusDisabled
		service.LastError = ""
		return
	}

	if !funnelRemoteEdgeFirstSliceSupportsProto(service.ListenProto) {
		service.ConfigStatus = FunnelServiceConfigStatusPending
		service.EdgeStatus = FunnelServiceEdgeStatusPending
		service.BackendStatus = FunnelServiceBackendStatusUnknown
		service.LastError = "当前remote-edge首批仅支持HTTP/HTTPS/WS/WSS"
		return
	}

	if edge == nil || !edge.Allocatable || edge.EdgeType == FunnelEdgeTypeServer {
		service.ConfigStatus = FunnelServiceConfigStatusPending
		service.EdgeStatus = FunnelServiceEdgeStatusUnavailable
		service.BackendStatus = FunnelServiceBackendStatusUnknown
		service.LastError = "当前服务未分配到remote-edge"
		return
	}

	if !funnelEdgeSupportsService(edge, service) {
		service.ConfigStatus = FunnelServiceConfigStatusPending
		service.EdgeStatus = FunnelServiceEdgeStatusUnavailable
		service.BackendStatus = FunnelServiceBackendStatusUnknown
		service.LastError = "当前remote-edge不支持该协议"
		return
	}

	now := time.Now().UTC()
	switch strings.ToLower(strings.TrimSpace(edge.HealthStatus)) {
	case FunnelEdgeHealthHealthy:
		if !funnelRemoteEdgeIsFresh(edge, now) {
			service.ConfigStatus = FunnelServiceConfigStatusPending
			service.EdgeStatus = FunnelServiceEdgeStatusPending
			service.BackendStatus = FunnelServiceBackendStatusUnknown
			service.LastError = "当前remote-edge尚未完成最近一次同步"
			return
		}
	case FunnelEdgeHealthUnhealthy:
		service.ConfigStatus = FunnelServiceConfigStatusPending
		service.EdgeStatus = FunnelServiceEdgeStatusUnavailable
		service.BackendStatus = FunnelServiceBackendStatusUnknown
		service.LastError = "当前remote-edge不健康"
		return
	default:
		service.ConfigStatus = FunnelServiceConfigStatusPending
		service.EdgeStatus = FunnelServiceEdgeStatusPending
		service.BackendStatus = FunnelServiceBackendStatusUnknown
		service.LastError = "当前remote-edge尚未就绪"
		return
	}

	domainReady := funnelDomainDNSReady(domain)
	if !domainReady {
		if strings.TrimSpace(domain.DNSStatus) == "" {
			domain.DNSStatus = FunnelDNSStatusPending
		}
		if strings.TrimSpace(domain.Status) == "" {
			domain.Status = FunnelDomainStatusPendingDNS
		}
		service.ConfigStatus = FunnelServiceConfigStatusPending
		service.EdgeStatus = FunnelServiceEdgeStatusPending
		service.BackendStatus = FunnelServiceBackendStatusUnknown
		service.LastError = strings.TrimSpace(domain.LastDNSError)
		if service.LastError == "" {
			service.LastError = "域名尚未完成验证"
		}
		return
	}
	domain.DNSStatus = FunnelDNSStatusReady

	if service.BackendTailnetIP == "" || service.BackendPort <= 0 {
		service.ConfigStatus = FunnelServiceConfigStatusError
		service.EdgeStatus = FunnelServiceEdgeStatusUnavailable
		service.BackendStatus = FunnelServiceBackendStatusError
		service.LastError = "后端地址配置不完整"
		return
	}

	if funnelListenProtoUsesTLS(service.ListenProto) {
		if strings.EqualFold(domain.TLSMode, FunnelTLSModeBringYourOwn) {
			service.ConfigStatus = FunnelServiceConfigStatusPending
			service.EdgeStatus = FunnelServiceEdgeStatusPending
			service.BackendStatus = FunnelServiceBackendStatusUnknown
			service.LastError = "自带证书模式尚未实现"
			return
		}
		if cert == nil || cert.CertStatus != FunnelCertStatusReady {
			domain.Status = FunnelDomainStatusPendingCert
		} else {
			domain.Status = FunnelDomainStatusActive
		}
	} else {
		domain.Status = FunnelDomainStatusActive
	}

	service.ConfigStatus = FunnelServiceConfigStatusActive
	service.EdgeStatus = FunnelServiceEdgeStatusApplied
	if service.TargetMachineOnline {
		service.BackendStatus = FunnelServiceBackendStatusUnknown
		service.LastError = ""
	} else {
		service.BackendStatus = FunnelServiceBackendStatusDegraded
		service.LastError = "目标设备当前离线"
	}
}

func (h *Mirage) lookupTenantFunnelEdgeForService(domain *FunnelDomain, service *FunnelService) *FunnelEdge {
	if domain == nil {
		return nil
	}
	edge, err := resolveFunnelEdgeForService(h.db, domain, service)
	if err == nil {
		return edge
	}
	if strings.TrimSpace(domain.EdgeTargetID) != "" {
		if edge, err := resolveFunnelEdge(h.db, "", domain.EdgeTargetID); err == nil {
			return edge
		}
	}
	if domain.EdgeMode == FunnelEdgeModeRemote {
		return nil
	}
	defaultEdge := buildDefaultServerEdge(FunnelPlatformConfig{
		ManagedBaseDomain: domain.Domain,
		DefaultEdgeMode:   domain.EdgeMode,
	})
	return &defaultEdge
}

func (h *Mirage) lookupTenantFunnelEdge(domain *FunnelDomain) *FunnelEdge {
	return h.lookupTenantFunnelEdgeForService(domain, nil)
}

func (h *Mirage) deleteFunnelDomainWithChildren(domain *FunnelDomain) error {
	if domain == nil {
		return fmt.Errorf("未找到Funnel域名")
	}
	if domain.DomainType == FunnelDomainTypeManaged {
		provider, err := h.currentManagedFunnelDNSProvider()
		if err != nil {
			return err
		}
		if provider != nil {
			dnsCtx := context.Background()
			if h != nil && h.ctx != nil {
				dnsCtx = h.ctx
			}
			if err := provider.DeleteManagedDomain(dnsCtx, domain.Domain); err != nil {
				return err
			}
		}
	}
	if err := h.db.Where("domain_id = ?", domain.ID).Delete(&FunnelService{}).Error; err != nil {
		return err
	}
	if err := h.db.Where("domain_id = ?", domain.ID).Delete(&FunnelCert{}).Error; err != nil {
		return err
	}
	return h.db.Delete(domain).Error
}

func applyTenantFunnelServiceProjection(service *FunnelService) error {
	if service == nil {
		return fmt.Errorf("未找到Funnel服务")
	}
	if !service.Enabled {
		service.ConfigStatus = FunnelServiceConfigStatusDisabled
		service.EdgeStatus = FunnelServiceEdgeStatusUnavailable
		service.BackendStatus = FunnelServiceConfigStatusDisabled
		service.LastError = ""
		return nil
	}
	service.ConfigStatus = FunnelServiceConfigStatusPending
	service.EdgeStatus = FunnelServiceEdgeStatusPending
	service.BackendStatus = FunnelServiceBackendStatusUnknown
	service.LastError = ""
	return nil
}

func normalizeFunnelListenProto(raw string) (string, error) {
	proto := strings.ToLower(strings.TrimSpace(raw))
	switch proto {
	case FunnelListenProtoHTTP, FunnelListenProtoHTTPS, FunnelListenProtoWS, FunnelListenProtoWSS, FunnelListenProtoTCP, FunnelListenProtoTLSTerminatedTCP:
		return proto, nil
	default:
		return "", fmt.Errorf("不支持的Funnel监听协议")
	}
}

func normalizeFunnelMountPath(raw string) string {
	mountPath := strings.TrimSpace(raw)
	if mountPath == "" || mountPath == "/" {
		return "/"
	}
	if !strings.HasPrefix(mountPath, "/") {
		mountPath = "/" + mountPath
	}
	return path.Clean(mountPath)
}

func defaultFunnelListenPort(proto string, listenPort int) (int, error) {
	if listenPort > 0 {
		return listenPort, nil
	}
	switch proto {
	case FunnelListenProtoHTTP, FunnelListenProtoWS:
		return 80, nil
	case FunnelListenProtoHTTPS, FunnelListenProtoWSS:
		return 443, nil
	case FunnelListenProtoTCP, FunnelListenProtoTLSTerminatedTCP:
		return 0, fmt.Errorf("当前协议需要显式指定监听端口")
	default:
		return 0, fmt.Errorf("不支持的Funnel监听协议")
	}
}

func normalizeFunnelServiceBackend(backendType, backendScheme string, backendPort int) (string, int, error) {
	backendType = strings.ToLower(strings.TrimSpace(backendType))
	switch backendType {
	case FunnelBackendTypeHTTPProxy:
		backendScheme = strings.ToLower(strings.TrimSpace(backendScheme))
		if backendScheme == "" {
			return "", 0, fmt.Errorf("HTTP类型Funnel后端必须指定 backendScheme")
		}
		if backendPort <= 0 {
			return "", 0, fmt.Errorf("HTTP类型Funnel后端必须指定 backendPort")
		}
		return backendScheme, backendPort, nil
	case FunnelBackendTypeTCPProxy:
		if strings.TrimSpace(backendScheme) != "" {
			return "", 0, fmt.Errorf("TCP类型Funnel后端不能指定 backendScheme")
		}
		if backendPort <= 0 {
			return "", 0, fmt.Errorf("TCP类型Funnel后端必须指定 backendPort")
		}
		return "", backendPort, nil
	default:
		return "", 0, fmt.Errorf("不支持的Funnel后端类型")
	}
}

func validateFunnelListenBackendPair(listenProto, backendType string) error {
	listenProto = strings.ToLower(strings.TrimSpace(listenProto))
	backendType = strings.ToLower(strings.TrimSpace(backendType))
	switch listenProto {
	case FunnelListenProtoHTTP, FunnelListenProtoHTTPS, FunnelListenProtoWS, FunnelListenProtoWSS:
		if backendType != FunnelBackendTypeHTTPProxy {
			return fmt.Errorf("HTTP类Funnel监听协议必须使用 HTTP 代理后端")
		}
		return nil
	case FunnelListenProtoTCP, FunnelListenProtoTLSTerminatedTCP:
		if backendType != FunnelBackendTypeTCPProxy {
			return fmt.Errorf("TCP类Funnel监听协议必须使用 TCP 代理后端")
		}
		return nil
	default:
		return fmt.Errorf("不支持的Funnel监听协议")
	}
}

func allocateManagedFunnelDomain(org *Organization, machine *Machine, cfg FunnelPlatformConfig) string {
	base := normalizeFunnelBaseDomain(cfg.ManagedBaseDomain)
	if base == "" && org != nil {
		base = normalizeFunnelBaseDomain(org.MagicDnsDomain)
	}
	if base == "" {
		base = "example.invalid"
	}
	if org == nil || machine == nil {
		return ""
	}
	return fmt.Sprintf("machine-%d-%s.%s", machine.ID, org.StableID, base)
}

func allocateManagedFunnelDomainForOrg(db *gorm.DB, org *Organization, cfg FunnelPlatformConfig) (string, error) {
	base := normalizeFunnelBaseDomain(cfg.ManagedBaseDomain)
	if base == "" && org != nil {
		base = normalizeFunnelBaseDomain(org.MagicDnsDomain)
	}
	if base == "" {
		base = "example.invalid"
	}
	if org == nil {
		return "", fmt.Errorf("未找到组织信息")
	}
	orgLabel := normalizeFunnelDNSLabel(org.StableID)
	if orgLabel == "" {
		orgLabel = fmt.Sprintf("org%d", org.ID)
	}
	for attempt := 1; attempt <= 1000; attempt++ {
		candidate := fmt.Sprintf("managed-%s-%d.%s", orgLabel, attempt, base)
		existing := &FunnelDomain{}
		err := db.Where("domain = ?", candidate).First(existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("无法分配托管Funnel域名")
}

func normalizeFunnelDNSLabel(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	label := strings.Trim(builder.String(), "-")
	if label == "" {
		return ""
	}
	if len(label) > 48 {
		label = strings.Trim(label[:48], "-")
	}
	return label
}

func chooseFunnelValidationMethod(raw string) string {
	method := strings.ToLower(strings.TrimSpace(raw))
	if method == "" {
		return "dns-txt"
	}
	return method
}

func defaultFunnelEdgeTargetID(edgeMode string) string {
	if strings.TrimSpace(edgeMode) == FunnelEdgeModeRemote {
		return ""
	}
	return "server-edge"
}

func (h *Mirage) buildFunnelValidationTarget(domainName string) string {
	return "_mirage-funnel." + domainName
}

func funnelDomainValidationInstructions(domain *FunnelDomain) map[string]any {
	if domain == nil {
		return nil
	}
	return map[string]any{
		"deferred":    true,
		"method":      domain.ValidationMethod,
		"recordName":  domain.ValidationTarget,
		"recordValue": domain.ValidationToken,
		"domain":      domain.Domain,
	}
}

func GenerateRandomStringURLSafeOrEmpty(length int) string {
	token, err := GenerateRandomStringURLSafe(length)
	if err != nil {
		return ""
	}
	return token
}
