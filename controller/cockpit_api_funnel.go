package controller

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

func (c *Cockpit) GetFunnelConfig(w http.ResponseWriter, r *http.Request) {
	if !c.authFunnelActor(w, r) {
		return
	}
	sysCfg, err := c.currentFunnelSysCfg()
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}
	payload, err := c.funnelConfigEnvelope(sysCfg)
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}
	c.doAPIResponse(w, "", payload)
}

func (c *Cockpit) SetFunnelConfig(w http.ResponseWriter, r *http.Request) {
	if !c.authFunnelActor(w, r) {
		return
	}
	sysCfg, err := c.currentFunnelSysCfg()
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}

	req := FunnelPlatformConfigRequest{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		c.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}

	cfg, err := normalizeFunnelPlatformConfig(funnelDomainFromRequest(req))
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}
	if strings.TrimSpace(cfg.ManagedBaseDomain) == "" {
		cfg.ManagedBaseDomain = strings.TrimSpace(sysCfg.Basedomain)
	}
	sysCfg.FunnelCfg = cfg
	if err := c.db.Save(sysCfg).Error; err != nil {
		c.doAPIResponse(w, "更新Funnel配置失败:"+err.Error(), nil)
		return
	}
	if _, err := c.ensureDefaultServerEdge(sysCfg); err != nil {
		c.doAPIResponse(w, "更新默认Funnel边缘失败:"+err.Error(), nil)
		return
	}
	if err := recordFunnelAudit(c.db, 0, "super_admin", "cockpit", "config", strconv.FormatUint(uint64(sysCfg.ID), 10), "config_updated", cfg); err != nil {
		c.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	select {
	case c.CtrlChn <- CtrlMsg{Msg: "reload-funnel"}:
	default:
	}
	payload, err := c.funnelConfigEnvelope(sysCfg)
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}
	c.doAPIResponse(w, "", payload)
}

func (c *Cockpit) GetFunnelEdges(w http.ResponseWriter, r *http.Request) {
	if !c.authFunnelActor(w, r) {
		return
	}
	sysCfg, err := c.currentFunnelSysCfg()
	if err != nil {
		c.doAPIResponse(w, err.Error(), nil)
		return
	}
	if _, err := c.ensureDefaultServerEdge(sysCfg); err != nil {
		c.doAPIResponse(w, "读取默认Funnel边缘失败:"+err.Error(), nil)
		return
	}
	edges := []FunnelEdge{}
	if err := c.db.Find(&edges).Error; err != nil {
		c.doAPIResponse(w, "读取Funnel边缘失败:"+err.Error(), nil)
		return
	}
	c.doAPIResponse(w, "", map[string]any{
		"edges": funnelEdgesResponse(edges),
	})
}

func (c *Cockpit) PostFunnelEdge(w http.ResponseWriter, r *http.Request) {
	if !c.authFunnelActor(w, r) {
		return
	}
	req := FunnelEdgeUpsertRequest{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		c.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}
	edgeType := strings.ToLower(strings.TrimSpace(req.EdgeType))
	if edgeType == "" {
		edgeType = FunnelEdgeTypeServer
	}
	desired := &FunnelEdge{
		StableID:             strings.TrimSpace(req.StableID),
		EdgeType:             edgeType,
		EdgeNodeID:           strings.TrimSpace(req.EdgeNodeID),
		Hostname:             strings.TrimSpace(req.Hostname),
		PublicAddrs:          req.PublicAddrs,
		SyncEndpoint:         strings.TrimSpace(req.SyncEndpoint),
		ListenerCapabilities: req.ListenerCapabilities,
		HealthStatus:         strings.TrimSpace(req.HealthStatus),
		Allocatable:          req.Allocatable,
		TrustProxyCIDRs:      normalizeProxyCIDRs(req.TrustProxyCIDRs),
	}
	if desired.Hostname == "" {
		desired.Hostname = "server-edge"
	}
	if desired.SyncEndpoint == "" {
		desired.SyncEndpoint = "/cockpit/api/funnel/edges/" + desired.EdgeType + "/sync"
	}
	if desired.HealthStatus == "" {
		desired.HealthStatus = FunnelEdgeHealthUnknown
	}
	if desired.EdgeNodeID == "" {
		desired.EdgeNodeID = desired.EdgeType
	}
	if desired.EdgeType == FunnelEdgeTypeServer && len(desired.PublicAddrs) == 0 {
		sysCfg, err := c.currentFunnelSysCfg()
		if err != nil {
			c.doAPIResponse(w, err.Error(), nil)
			return
		}
		cfg, err := effectiveFunnelPlatformConfig(sysCfg)
		if err != nil {
			c.doAPIResponse(w, err.Error(), nil)
			return
		}
		desired.PublicAddrs = FunnelPublicAddrList(effectiveFunnelIngressTargets(cfg))
		if desired.TrustProxyCIDRs == nil {
			desired.TrustProxyCIDRs = cfg.TrustedProxyCIDRs
		}
	}
	edge, err := upsertFunnelEdge(c.db, desired)
	if err != nil {
		c.doAPIResponse(w, "更新Funnel边缘失败:"+err.Error(), nil)
		return
	}
	if err := recordFunnelAudit(c.db, 0, "super_admin", "cockpit", "edge", edge.StableID, "edge_upsert", desired); err != nil {
		c.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	select {
	case c.CtrlChn <- CtrlMsg{Msg: "reload-funnel"}:
	default:
	}
	c.doAPIResponse(w, "", map[string]any{"edge": funnelEdgeToMap(edge)})
}

func (c *Cockpit) PostFunnelEdgeSync(w http.ResponseWriter, r *http.Request) {
	if !c.authFunnelActor(w, r) {
		return
	}
	edgeID, err := parseFunnelID(mux.Vars(r)["id"])
	if err != nil {
		c.doAPIResponse(w, "边缘ID解析失败:"+err.Error(), nil)
		return
	}
	edge := &FunnelEdge{}
	if err := c.db.First(edge, edgeID).Error; err != nil {
		c.doAPIResponse(w, "未找到Funnel边缘:"+err.Error(), nil)
		return
	}

	if edge.EdgeType != FunnelEdgeTypeServer {
		now := time.Now().UTC()
		edge.LastSeen = &now
		if strings.TrimSpace(edge.HealthStatus) == "" {
			edge.HealthStatus = FunnelEdgeHealthUnknown
		}
		if err := c.db.Save(edge).Error; err != nil {
			c.doAPIResponse(w, "更新Funnel边缘状态失败:"+err.Error(), nil)
			return
		}
		payload, err := buildFunnelEdgeSyncPayload(c.db, edge)
		if err != nil {
			c.doAPIResponse(w, "生成Funnel边缘同步数据失败:"+err.Error(), nil)
			return
		}
		if err := recordFunnelAudit(c.db, 0, "super_admin", "cockpit", "edge", edge.StableID, "edge_sync", map[string]any{"edgeId": edge.ID, "serviceCount": payload.ServiceCount}); err != nil {
			c.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
			return
		}
		c.doAPIResponse(w, "", payload)
		return
	}

	if err := recordFunnelAudit(c.db, 0, "super_admin", "cockpit", "edge", edge.StableID, "edge_sync", map[string]any{"edgeId": edge.ID}); err != nil {
		c.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	c.doAPIResponse(w, "", funnelEdgeWithSyncDeferred(edge))
}

func (c *Cockpit) GetFunnelDomains(w http.ResponseWriter, r *http.Request) {
	if !c.authFunnelActor(w, r) {
		return
	}
	domains := []FunnelDomain{}
	if err := c.db.Find(&domains).Error; err != nil {
		c.doAPIResponse(w, "读取Funnel域名失败:"+err.Error(), nil)
		return
	}
	c.doAPIResponse(w, "", map[string]any{"domains": funnelDomainsResponse(domains)})
}

func (c *Cockpit) PostFunnelDomainVerify(w http.ResponseWriter, r *http.Request) {
	if !c.authFunnelActor(w, r) {
		return
	}
	req := FunnelDomainVerifyRequest{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		c.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}
	var domain *FunnelDomain
	if req.DomainID != 0 {
		domain = &FunnelDomain{}
		if err := c.db.First(domain, req.DomainID).Error; err != nil {
			c.doAPIResponse(w, "未找到Funnel域名:"+err.Error(), nil)
			return
		}
	} else {
		normalized := normalizeFunnelBaseDomain(req.Domain)
		if normalized == "" {
			c.doAPIResponse(w, "未指定Funnel域名", nil)
			return
		}
		domain = &FunnelDomain{Domain: normalized}
		if err := c.db.Where("domain = ?", normalized).First(domain).Error; err != nil {
			c.doAPIResponse(w, "未找到Funnel域名:"+err.Error(), nil)
			return
		}
	}
	if err := markFunnelDomainVerified(c.db, domain); err != nil {
		c.doAPIResponse(w, "更新Funnel域名失败:"+err.Error(), nil)
		return
	}
	if err := recordFunnelAudit(c.db, domain.OrgID, "super_admin", "cockpit", "domain", domain.StableID, "domain_verify_requested", domainActionPayload(domain, nil)); err != nil {
		c.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	select {
	case c.CtrlChn <- CtrlMsg{Msg: "reload-funnel"}:
	default:
	}
	c.doAPIResponse(w, "", funnelDomainWithVerifiedFlag(domain))
}

func (c *Cockpit) PostFunnelCertRenew(w http.ResponseWriter, r *http.Request) {
	if !c.authFunnelActor(w, r) {
		return
	}
	req := FunnelCertRenewRequest{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		c.doAPIResponse(w, "用户请求解析失败:"+err.Error(), nil)
		return
	}
	var cert *FunnelCert
	if req.CertID != 0 {
		cert = &FunnelCert{}
		if err := c.db.First(cert, req.CertID).Error; err != nil {
			c.doAPIResponse(w, "未找到Funnel证书:"+err.Error(), nil)
			return
		}
	} else {
		if req.DomainID == 0 {
			c.doAPIResponse(w, "未指定Funnel域名", nil)
			return
		}
		cert = &FunnelCert{}
		if err := c.db.Where("domain_id = ?", req.DomainID).First(cert).Error; err != nil {
			c.doAPIResponse(w, "未找到Funnel证书:"+err.Error(), nil)
			return
		}
	}
	if err := recordFunnelAudit(c.db, 0, "super_admin", "cockpit", "cert", cert.StableID, "cert_renew_requested", funnelCertActionData(cert, nil)); err != nil {
		c.doAPIResponse(w, "记录Funnel审计失败:"+err.Error(), nil)
		return
	}
	c.doAPIResponse(w, "", funnelCertWithRenewDeferred(cert))
}
