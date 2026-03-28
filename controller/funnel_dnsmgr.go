package controller

import (
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	dnsMgrManagedRecordType   = "CNAME"
	dnsMgrManagedRecordRemark = "Mirage Funnel managed"
)

type managedFunnelDNSProvider interface {
	EnsureManagedDomain(ctx context.Context, fqdn string) error
	DeleteManagedDomain(ctx context.Context, fqdn string) error
	LookupManagedDomain(ctx context.Context, fqdn string) (managedFunnelDNSLookupResult, error)
}

type dnsMgrManagedFunnelDNSProvider struct {
	baseURL string
	uid     int64
	apiKey  string
	zone    string
	target  string
	client  *http.Client
}

type managedFunnelDNSLookupResult struct {
	Ready   bool
	Message string
}

type dnsMgrAPIEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (e dnsMgrAPIEnvelope) GetCode() int {
	return e.Code
}

func (e dnsMgrAPIEnvelope) GetMsg() string {
	return e.Msg
}

type dnsMgrAPIErrorCarrier interface {
	GetCode() int
	GetMsg() string
}

type dnsMgrDomainListResponse struct {
	dnsMgrAPIEnvelope
	Total int               `json:"total"`
	Rows  []dnsMgrDomainRow `json:"rows"`
}

type dnsMgrDomainRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type dnsMgrDomainInfoResponse struct {
	dnsMgrAPIEnvelope
	Data dnsMgrDomainInfo `json:"data"`
}

type dnsMgrDomainInfo struct {
	ID         int64              `json:"id"`
	Name       string             `json:"name"`
	RecordLine []dnsMgrRecordLine `json:"recordLine"`
	MinTTL     string             `json:"minTTL"`
}

type dnsMgrRecordLine struct {
	ID   any    `json:"id"`
	Name string `json:"name"`
}

type dnsMgrRecordListResponse struct {
	dnsMgrAPIEnvelope
	Total int                `json:"total"`
	Rows  []dnsMgrRecordItem `json:"rows"`
}

type dnsMgrRecordItem struct {
	RecordID string `json:"RecordId"`
	Domain   string `json:"Domain"`
	Name     string `json:"Name"`
	Type     string `json:"Type"`
	Value    string `json:"Value"`
	Line     string `json:"Line"`
	Status   string `json:"Status"`
	TTL      int    `json:"TTL"`
	Remark   string `json:"Remark"`
}

type dnsMgrMutationResponse struct {
	dnsMgrAPIEnvelope
}

type dnsMgrResolvedZone struct {
	ID   int64
	Name string
	Line string
	TTL  int
}

func newManagedFunnelDNSProvider(cfg FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.ManagedDNSProvider)) {
	case FunnelManagedDNSProviderNone:
		return nil, nil
	case FunnelManagedDNSProviderDNSMgr:
		normalized, err := normalizeFunnelPlatformConfig(cfg)
		if err != nil {
			return nil, err
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: normalized.ManagedDNSSkipTLSVerify}
		return &dnsMgrManagedFunnelDNSProvider{
			baseURL: normalized.ManagedDNSAPIBaseURL,
			uid:     normalized.ManagedDNSUID,
			apiKey:  normalized.ManagedDNSAPIKey,
			zone:    normalizeFunnelBaseDomain(normalized.ManagedBaseDomain),
			target:  buildManagedFunnelDomainTarget(normalized),
			client: &http.Client{
				Timeout:   15 * time.Second,
				Transport: transport,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported managed funnel dns provider: %s", cfg.ManagedDNSProvider)
	}
}

// Managed free domains resolve to the platform apex, so existing LB/server-edge
// ingress can keep routing by Host without needing a per-service publish target.
func buildManagedFunnelDomainTarget(cfg FunnelPlatformConfig) string {
	return normalizeFunnelBaseDomain(cfg.ManagedBaseDomain)
}

func (p *dnsMgrManagedFunnelDNSProvider) EnsureManagedDomain(ctx context.Context, fqdn string) error {
	zone, err := p.resolveZone(ctx)
	if err != nil {
		return err
	}
	recordName, err := dnsMgrRecordNameForDomain(fqdn, zone.Name)
	if err != nil {
		return err
	}
	records, err := p.listRecords(ctx, zone.ID, recordName)
	if err != nil {
		return err
	}
	cnameRecords := make([]dnsMgrRecordItem, 0, len(records))
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSpace(record.Name), recordName) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record.Type), dnsMgrManagedRecordType) {
			return fmt.Errorf("managed funnel dns 记录冲突: %s 已存在 %s 记录", fqdn, record.Type)
		}
		cnameRecords = append(cnameRecords, record)
	}

	if len(cnameRecords) == 0 {
		return p.addRecord(ctx, zone, recordName, p.target)
	}

	primary := cnameRecords[0]
	for _, record := range cnameRecords {
		if strings.EqualFold(strings.TrimSpace(record.Value), p.target) {
			primary = record
			break
		}
	}
	if !strings.EqualFold(strings.TrimSpace(primary.Value), p.target) {
		if err := p.updateRecord(ctx, zone, primary.RecordID, recordName, p.target); err != nil {
			return err
		}
	}
	if strings.TrimSpace(primary.Status) != "1" {
		if err := p.setRecordStatus(ctx, zone.ID, primary.RecordID, "1"); err != nil {
			return err
		}
	}
	for _, record := range cnameRecords {
		if record.RecordID == primary.RecordID {
			continue
		}
		if err := p.deleteRecord(ctx, zone.ID, record.RecordID); err != nil {
			return err
		}
	}

	return nil
}

func (p *dnsMgrManagedFunnelDNSProvider) DeleteManagedDomain(ctx context.Context, fqdn string) error {
	zone, err := p.resolveZone(ctx)
	if err != nil {
		return err
	}
	recordName, err := dnsMgrRecordNameForDomain(fqdn, zone.Name)
	if err != nil {
		return err
	}
	records, err := p.listRecords(ctx, zone.ID, recordName)
	if err != nil {
		return err
	}
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSpace(record.Name), recordName) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record.Type), dnsMgrManagedRecordType) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record.Value), p.target) {
			continue
		}
		if err := p.deleteRecord(ctx, zone.ID, record.RecordID); err != nil {
			return err
		}
	}
	return nil
}

func (p *dnsMgrManagedFunnelDNSProvider) LookupManagedDomain(ctx context.Context, fqdn string) (managedFunnelDNSLookupResult, error) {
	zone, err := p.resolveZone(ctx)
	if err != nil {
		return managedFunnelDNSLookupResult{}, err
	}
	recordName, err := dnsMgrRecordNameForDomain(fqdn, zone.Name)
	if err != nil {
		return managedFunnelDNSLookupResult{}, err
	}
	records, err := p.listRecords(ctx, zone.ID, recordName)
	if err != nil {
		return managedFunnelDNSLookupResult{}, err
	}
	if len(records) == 0 {
		return managedFunnelDNSLookupResult{
			Ready:   false,
			Message: "未找到托管 DNS 记录",
		}, nil
	}

	var (
		hasConflictingType bool
		hasMatchingTarget  bool
		hasEnabledMatch    bool
		wrongTargetValue   string
	)
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSpace(record.Name), recordName) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record.Type), dnsMgrManagedRecordType) {
			hasConflictingType = true
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record.Value), p.target) {
			if wrongTargetValue == "" {
				wrongTargetValue = strings.TrimSpace(record.Value)
			}
			continue
		}
		hasMatchingTarget = true
		if strings.TrimSpace(record.Status) == "1" {
			hasEnabledMatch = true
			break
		}
	}
	if hasEnabledMatch {
		return managedFunnelDNSLookupResult{Ready: true}, nil
	}
	if hasMatchingTarget {
		return managedFunnelDNSLookupResult{
			Ready:   false,
			Message: "托管 DNS 记录当前已暂停",
		}, nil
	}
	if wrongTargetValue != "" {
		return managedFunnelDNSLookupResult{
			Ready:   false,
			Message: fmt.Sprintf("托管 DNS 记录未指向 %s", p.target),
		}, nil
	}
	if hasConflictingType {
		return managedFunnelDNSLookupResult{
			Ready:   false,
			Message: "存在冲突的非 CNAME 记录",
		}, nil
	}
	return managedFunnelDNSLookupResult{
		Ready:   false,
		Message: "未找到托管 DNS 记录",
	}, nil
}

func (p *dnsMgrManagedFunnelDNSProvider) resolveZone(ctx context.Context) (dnsMgrResolvedZone, error) {
	resp := dnsMgrDomainListResponse{}
	if err := p.postForm(ctx, "/api/domain", url.Values{
		"kw":    []string{p.zone},
		"limit": []string{"100"},
	}, &resp); err != nil {
		return dnsMgrResolvedZone{}, err
	}
	var zoneID int64
	for _, row := range resp.Rows {
		if strings.EqualFold(strings.TrimSpace(row.Name), p.zone) {
			zoneID = row.ID
			break
		}
	}
	if zoneID == 0 {
		return dnsMgrResolvedZone{}, fmt.Errorf("dnsmgr 未找到托管域名区 %s", p.zone)
	}

	infoResp := dnsMgrDomainInfoResponse{}
	if err := p.postForm(ctx, fmt.Sprintf("/api/domain/%d", zoneID), nil, &infoResp); err != nil {
		return dnsMgrResolvedZone{}, err
	}
	return dnsMgrResolvedZone{
		ID:   zoneID,
		Name: normalizeFunnelBaseDomain(infoResp.Data.Name),
		Line: dnsMgrDefaultRecordLine(infoResp.Data.RecordLine),
		TTL:  dnsMgrMinTTL(infoResp.Data.MinTTL),
	}, nil
}

func (p *dnsMgrManagedFunnelDNSProvider) listRecords(ctx context.Context, zoneID int64, recordName string) ([]dnsMgrRecordItem, error) {
	resp := dnsMgrRecordListResponse{}
	if err := p.postForm(ctx, fmt.Sprintf("/api/record/data/%d", zoneID), url.Values{
		"subdomain": []string{recordName},
		"limit":     []string{"100"},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Rows, nil
}

func (p *dnsMgrManagedFunnelDNSProvider) addRecord(ctx context.Context, zone dnsMgrResolvedZone, recordName, value string) error {
	resp := dnsMgrMutationResponse{}
	return p.postForm(ctx, fmt.Sprintf("/api/record/add/%d", zone.ID), url.Values{
		"name":   []string{recordName},
		"type":   []string{dnsMgrManagedRecordType},
		"value":  []string{value},
		"line":   []string{zone.Line},
		"ttl":    []string{strconv.Itoa(zone.TTL)},
		"remark": []string{dnsMgrManagedRecordRemark},
	}, &resp)
}

func (p *dnsMgrManagedFunnelDNSProvider) updateRecord(ctx context.Context, zone dnsMgrResolvedZone, recordID, recordName, value string) error {
	resp := dnsMgrMutationResponse{}
	return p.postForm(ctx, fmt.Sprintf("/api/record/update/%d", zone.ID), url.Values{
		"recordid": []string{recordID},
		"name":     []string{recordName},
		"type":     []string{dnsMgrManagedRecordType},
		"value":    []string{value},
		"line":     []string{zone.Line},
		"ttl":      []string{strconv.Itoa(zone.TTL)},
		"remark":   []string{dnsMgrManagedRecordRemark},
	}, &resp)
}

func (p *dnsMgrManagedFunnelDNSProvider) setRecordStatus(ctx context.Context, zoneID int64, recordID, status string) error {
	resp := dnsMgrMutationResponse{}
	return p.postForm(ctx, fmt.Sprintf("/api/record/status/%d", zoneID), url.Values{
		"recordid": []string{recordID},
		"status":   []string{status},
	}, &resp)
}

func (p *dnsMgrManagedFunnelDNSProvider) deleteRecord(ctx context.Context, zoneID int64, recordID string) error {
	resp := dnsMgrMutationResponse{}
	return p.postForm(ctx, fmt.Sprintf("/api/record/delete/%d", zoneID), url.Values{
		"recordid": []string{recordID},
	}, &resp)
}

func (p *dnsMgrManagedFunnelDNSProvider) postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	if form == nil {
		form = url.Values{}
	}
	timestamp := time.Now().Unix()
	form.Set("uid", strconv.FormatInt(p.uid, 10))
	form.Set("timestamp", strconv.FormatInt(timestamp, 10))
	form.Set("sign", dnsMgrAPISign(p.uid, timestamp, p.apiKey))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.baseURL, "/")+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("dnsmgr request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("dnsmgr read failed: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("dnsmgr api status %d", resp.StatusCode)
		}
		return fmt.Errorf("dnsmgr decode failed: %w", err)
	}
	if carrier, ok := out.(dnsMgrAPIErrorCarrier); ok && carrier.GetCode() != 0 {
		return fmt.Errorf("dnsmgr api error: %s", strings.TrimSpace(carrier.GetMsg()))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("dnsmgr api status %d", resp.StatusCode)
	}
	return nil
}

func dnsMgrAPISign(uid int64, timestamp int64, apiKey string) string {
	sum := md5.Sum([]byte(strconv.FormatInt(uid, 10) + strconv.FormatInt(timestamp, 10) + apiKey))
	return hex.EncodeToString(sum[:])
}

func dnsMgrRecordNameForDomain(fqdn, zone string) (string, error) {
	fqdn = normalizeFunnelBaseDomain(fqdn)
	zone = normalizeFunnelBaseDomain(zone)
	if fqdn == "" || zone == "" {
		return "", fmt.Errorf("invalid managed funnel domain")
	}
	if fqdn == zone {
		return "@", nil
	}
	suffix := "." + zone
	if !strings.HasSuffix(fqdn, suffix) {
		return "", fmt.Errorf("managed funnel domain %s does not belong to %s", fqdn, zone)
	}
	recordName := strings.TrimSuffix(fqdn, suffix)
	recordName = strings.TrimSuffix(recordName, ".")
	if recordName == "" {
		return "@", nil
	}
	return recordName, nil
}

func dnsMgrDefaultRecordLine(lines []dnsMgrRecordLine) string {
	for _, line := range lines {
		if strings.TrimSpace(line.Name) == "默认" {
			return fmt.Sprint(line.ID)
		}
	}
	if len(lines) > 0 {
		return fmt.Sprint(lines[0].ID)
	}
	return "0"
}

func dnsMgrMinTTL(raw string) int {
	ttl, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || ttl <= 0 {
		return 60
	}
	return ttl
}

func (h *Mirage) currentManagedFunnelDNSProvider() (managedFunnelDNSProvider, error) {
	if h == nil || h.db == nil {
		return nil, nil
	}
	fallbackBaseDomain := ""
	if h.cfg != nil {
		fallbackBaseDomain = h.cfg.BaseDomain
	}
	cfg, err := effectiveFunnelPlatformConfigFromDB(h.db, fallbackBaseDomain)
	if err != nil {
		return nil, err
	}
	factory := h.newManagedFunnelDNSProvider
	if factory == nil {
		factory = newManagedFunnelDNSProvider
	}
	return factory(cfg)
}

func (c *Cockpit) currentManagedFunnelDNSProvider() (managedFunnelDNSProvider, error) {
	if c == nil || c.db == nil {
		return nil, nil
	}
	sysCfg, err := c.currentFunnelSysCfg()
	if err != nil {
		return nil, err
	}
	cfg, err := effectiveFunnelPlatformConfig(sysCfg)
	if err != nil {
		return nil, err
	}
	return newManagedFunnelDNSProvider(cfg)
}

func verifyManagedFunnelDomain(tx *gorm.DB, domain *FunnelDomain, provider managedFunnelDNSProvider) (managedFunnelDNSLookupResult, error) {
	if domain == nil {
		return managedFunnelDNSLookupResult{}, fmt.Errorf("未找到Funnel域名")
	}
	if provider == nil {
		if err := markFunnelDomainVerified(tx, domain); err != nil {
			return managedFunnelDNSLookupResult{}, err
		}
		return managedFunnelDNSLookupResult{Ready: true}, nil
	}
	result, err := provider.LookupManagedDomain(context.Background(), domain.Domain)
	if err != nil {
		_ = markFunnelDomainDNSFailed(tx, domain, err.Error())
		return managedFunnelDNSLookupResult{}, err
	}
	if result.Ready {
		if err := markFunnelDomainVerified(tx, domain); err != nil {
			return managedFunnelDNSLookupResult{}, err
		}
		return result, nil
	}
	if err := markFunnelDomainDNSFailed(tx, domain, result.Message); err != nil {
		return managedFunnelDNSLookupResult{}, err
	}
	return result, nil
}
