package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type FunnelPlatformConfigRequest struct {
	ManagedBaseDomain       string         `json:"managedBaseDomain"`
	DefaultEdgeMode         string         `json:"defaultEdgeMode"`
	DefaultListenerMode     string         `json:"defaultListenerMode"`
	DirectBindAddrs         StringList     `json:"directBindAddrs"`
	DirectBindPorts         FunnelPortList `json:"directBindPorts"`
	TrustedProxyCIDRs       StringList     `json:"trustedProxyCIDRs"`
	ManagedDNSProvider      string         `json:"managedDnsProvider"`
	ManagedDNSAPIBaseURL    string         `json:"managedDnsApiBaseUrl"`
	ManagedDNSUID           int64          `json:"managedDnsUid"`
	ManagedDNSAPIKey        string         `json:"managedDnsApiKey"`
	ManagedDNSSkipTLSVerify bool           `json:"managedDnsSkipTlsVerify"`
}

type FunnelEdgeUpsertRequest struct {
	StableID             string                     `json:"stableId"`
	EdgeType             string                     `json:"edgeType"`
	EdgeNodeID           string                     `json:"edgeNodeId"`
	Hostname             string                     `json:"hostname"`
	PublicAddrs          FunnelPublicAddrList       `json:"publicAddrs"`
	SyncEndpoint         string                     `json:"syncEndpoint"`
	ListenerCapabilities FunnelListenerCapabilities `json:"listenerCapabilities"`
	HealthStatus         string                     `json:"healthStatus"`
	Allocatable          bool                       `json:"allocatable"`
	TrustProxyCIDRs      StringList                 `json:"trustProxyCIDRs"`
}

type FunnelDomainVerifyRequest struct {
	DomainID int64  `json:"domainId"`
	Domain   string `json:"domain"`
}

type FunnelCertRenewRequest struct {
	DomainID int64 `json:"domainId"`
	CertID   int64 `json:"certId"`
}

type FunnelEdgeSyncPayload struct {
	Edge         map[string]any          `json:"edge"`
	Services     []FunnelEdgeSyncService `json:"services"`
	ServiceCount int                     `json:"serviceCount"`
	GeneratedAt  time.Time               `json:"generatedAt"`
	SyncDeferred bool                    `json:"syncDeferred"`
}

type FunnelEdgeSyncService struct {
	OrgID   int64                 `json:"orgId"`
	Service map[string]any        `json:"service"`
	Domain  map[string]any        `json:"domain"`
	Cert    map[string]any        `json:"cert"`
	Public  FunnelEdgeSyncPublic  `json:"public"`
	Backend FunnelEdgeSyncBackend `json:"backend"`
}

type FunnelEdgeSyncPublic struct {
	Host         string `json:"host"`
	ListenProto  string `json:"listenProto"`
	ListenPort   int    `json:"listenPort"`
	MountPath    string `json:"mountPath"`
	ListenerMode string `json:"listenerMode"`
	TLSMode      string `json:"tlsMode"`
}

type FunnelEdgeSyncBackend struct {
	Network string `json:"network"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Scheme  string `json:"scheme"`
}

func defaultFunnelPlatformConfig() FunnelPlatformConfig {
	return FunnelPlatformConfig{
		DefaultEdgeMode:     FunnelEdgeModeServer,
		DefaultListenerMode: FunnelListenerModeDirect,
		DirectBindAddrs:     StringList{"0.0.0.0", "::"},
		DirectBindPorts:     FunnelPortList{80, 443},
		ManagedDNSProvider:  FunnelManagedDNSProviderNone,
	}
}

const (
	defaultFunnelDNSMgrAPIBaseURL = "https://dnsmgr.mm.md"
	defaultFunnelDNSMgrBaseDomain = "mirage.mm.md"
)

func normalizeFunnelBaseDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		if parsed, err := url.Parse(raw); err == nil {
			raw = parsed.Host
			if raw == "" {
				raw = strings.Trim(parsed.Path, "/")
			}
		}
	}
	raw = strings.Trim(raw, "/")
	return strings.ToLower(raw)
}

func normalizeFunnelPlatformConfig(cfg FunnelPlatformConfig) (FunnelPlatformConfig, error) {
	normalized := cfg
	normalized.ManagedBaseDomain = normalizeFunnelBaseDomain(normalized.ManagedBaseDomain)
	normalized.ManagedDNSProvider = strings.ToLower(strings.TrimSpace(normalized.ManagedDNSProvider))
	switch normalized.ManagedDNSProvider {
	case FunnelManagedDNSProviderNone, FunnelManagedDNSProviderDNSMgr:
	default:
		return FunnelPlatformConfig{}, fmt.Errorf("unsupported managed funnel dns provider: %s", normalized.ManagedDNSProvider)
	}
	normalized.ManagedDNSAPIBaseURL = strings.TrimSpace(normalized.ManagedDNSAPIBaseURL)
	if normalized.ManagedDNSProvider == FunnelManagedDNSProviderDNSMgr {
		if normalized.ManagedDNSAPIBaseURL == "" {
			normalized.ManagedDNSAPIBaseURL = defaultFunnelDNSMgrAPIBaseURL
		}
		if normalized.ManagedBaseDomain == "" {
			normalized.ManagedBaseDomain = defaultFunnelDNSMgrBaseDomain
		}
		if normalized.ManagedBaseDomain != defaultFunnelDNSMgrBaseDomain {
			return FunnelPlatformConfig{}, fmt.Errorf("dnsmgr 托管域名后缀固定为 %s", defaultFunnelDNSMgrBaseDomain)
		}
		if normalized.ManagedDNSUID <= 0 {
			return FunnelPlatformConfig{}, fmt.Errorf("managed funnel dns uid is required")
		}
		if strings.TrimSpace(normalized.ManagedDNSAPIKey) == "" {
			return FunnelPlatformConfig{}, fmt.Errorf("managed funnel dns api key is required")
		}
	}
	if normalized.ManagedDNSAPIBaseURL != "" {
		parsed, err := url.Parse(normalized.ManagedDNSAPIBaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return FunnelPlatformConfig{}, fmt.Errorf("invalid managed funnel dns api url")
		}
		normalized.ManagedDNSAPIBaseURL = strings.TrimRight(parsed.String(), "/")
	}

	normalized.DefaultEdgeMode = strings.ToLower(strings.TrimSpace(normalized.DefaultEdgeMode))
	if normalized.DefaultEdgeMode == "" {
		normalized.DefaultEdgeMode = FunnelEdgeModeServer
	}
	switch normalized.DefaultEdgeMode {
	case FunnelEdgeModeServer, FunnelEdgeModeRemote:
	default:
		return FunnelPlatformConfig{}, fmt.Errorf("unsupported funnel default edge mode: %s", normalized.DefaultEdgeMode)
	}

	normalized.DefaultListenerMode = strings.ToLower(strings.TrimSpace(normalized.DefaultListenerMode))
	if normalized.DefaultListenerMode == "" {
		normalized.DefaultListenerMode = FunnelListenerModeDirect
	}
	switch normalized.DefaultListenerMode {
	case FunnelListenerModeDirect, FunnelListenerModeBehindProxy:
	default:
		return FunnelPlatformConfig{}, fmt.Errorf("unsupported funnel listener mode: %s", normalized.DefaultListenerMode)
	}

	normalized.DirectBindAddrs = normalizeStringList(normalized.DirectBindAddrs)
	if len(normalized.DirectBindAddrs) == 0 {
		normalized.DirectBindAddrs = StringList{"0.0.0.0", "::"}
	}

	ports := make([]int, 0, len(normalized.DirectBindPorts))
	seenPorts := make(map[int]struct{}, len(normalized.DirectBindPorts))
	for _, port := range normalized.DirectBindPorts {
		if port <= 0 || port > 65535 {
			return FunnelPlatformConfig{}, fmt.Errorf("invalid funnel direct bind port: %d", port)
		}
		if _, ok := seenPorts[port]; ok {
			continue
		}
		seenPorts[port] = struct{}{}
		ports = append(ports, port)
	}
	sort.Ints(ports)
	if len(ports) == 0 {
		ports = []int{80, 443}
	}
	normalized.DirectBindPorts = FunnelPortList(ports)

	proxyCIDRs := normalizeStringList(normalized.TrustedProxyCIDRs)
	for _, cidr := range proxyCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return FunnelPlatformConfig{}, fmt.Errorf("invalid funnel trusted proxy cidr: %s", cidr)
		}
	}
	normalized.TrustedProxyCIDRs = proxyCIDRs

	return normalized, nil
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func effectiveFunnelPlatformConfig(sysCfg *SysConfig) (FunnelPlatformConfig, error) {
	cfg := defaultFunnelPlatformConfig()
	if sysCfg != nil {
		cfg = sysCfg.FunnelCfg
	}
	cfg, err := normalizeFunnelPlatformConfig(cfg)
	if err != nil {
		return FunnelPlatformConfig{}, err
	}
	if strings.TrimSpace(cfg.ManagedBaseDomain) == "" && sysCfg != nil {
		cfg.ManagedBaseDomain = normalizeFunnelBaseDomain(sysCfg.Basedomain)
	}
	return normalizeFunnelPlatformConfig(cfg)
}

func effectiveFunnelPlatformConfigFromDB(db *gorm.DB, fallbackBaseDomain string) (FunnelPlatformConfig, error) {
	sysCfg := &SysConfig{}
	if err := db.Order("id ASC").First(sysCfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return normalizeFunnelPlatformConfig(FunnelPlatformConfig{
				ManagedBaseDomain: fallbackBaseDomain,
			})
		}
		return FunnelPlatformConfig{}, err
	}
	if strings.TrimSpace(sysCfg.Basedomain) == "" {
		sysCfg.Basedomain = fallbackBaseDomain
	}
	return effectiveFunnelPlatformConfig(sysCfg)
}

func effectiveFunnelIngressTargets(cfg FunnelPlatformConfig) []FunnelPublicAddr {
	if len(cfg.DirectBindAddrs) == 0 {
		return nil
	}
	ports := cfg.DirectBindPorts
	if len(ports) == 0 {
		ports = FunnelPortList{80, 443}
	}
	targets := make([]FunnelPublicAddr, 0, len(cfg.DirectBindAddrs)*len(ports))
	for _, addr := range cfg.DirectBindAddrs {
		for _, port := range ports {
			targets = append(targets, FunnelPublicAddr{
				Network: "tcp",
				Address: addr,
				Port:    port,
			})
		}
	}
	return targets
}

func buildDefaultServerEdge(cfg FunnelPlatformConfig) FunnelEdge {
	targets := effectiveFunnelIngressTargets(cfg)
	hostname := cfg.ManagedBaseDomain
	if hostname == "" {
		hostname = "server-edge"
	}
	return FunnelEdge{
		EdgeType:        FunnelEdgeTypeServer,
		EdgeNodeID:      "server-edge",
		Hostname:        hostname,
		PublicAddrs:     FunnelPublicAddrList(targets),
		SyncEndpoint:    defaultFunnelSyncEndpoint(FunnelEdgeTypeServer),
		HealthStatus:    FunnelEdgeHealthUnknown,
		Allocatable:     true,
		TrustProxyCIDRs: normalizeProxyCIDRs(cfg.TrustedProxyCIDRs),
		ListenerCapabilities: FunnelListenerCapabilities{
			SupportsHTTP:             true,
			SupportsWS:               true,
			SupportsTCP:              true,
			SupportsTLSTerminatedTCP: true,
			SupportsProxyProtocol:    true,
			SupportsForwardedHeaders: true,
			SupportsDirectBind:       true,
		},
	}
}

func funnelListenProtoUsesTLS(proto string) bool {
	switch proto {
	case FunnelListenProtoHTTPS, FunnelListenProtoWSS, FunnelListenProtoTLSTerminatedTCP:
		return true
	default:
		return false
	}
}

func funnelHTTPFamilyProto(proto string) bool {
	switch proto {
	case FunnelListenProtoHTTP, FunnelListenProtoHTTPS, FunnelListenProtoWS, FunnelListenProtoWSS:
		return true
	default:
		return false
	}
}

func funnelRemoteEdgeFirstSliceSupportsProto(proto string) bool {
	return funnelHTTPFamilyProto(proto)
}

func funnelEdgeSupportsService(edge *FunnelEdge, service *FunnelService) bool {
	if edge == nil || service == nil {
		return false
	}
	switch service.ListenProto {
	case FunnelListenProtoHTTP, FunnelListenProtoHTTPS:
		return edge.ListenerCapabilities.SupportsHTTP
	case FunnelListenProtoWS, FunnelListenProtoWSS:
		return edge.ListenerCapabilities.SupportsWS || edge.ListenerCapabilities.SupportsHTTP
	case FunnelListenProtoTCP:
		return edge.ListenerCapabilities.SupportsTCP
	case FunnelListenProtoTLSTerminatedTCP:
		return edge.ListenerCapabilities.SupportsTLSTerminatedTCP
	default:
		return false
	}
}

func funnelEdgeHealthRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case FunnelEdgeHealthHealthy:
		return 3
	case FunnelEdgeHealthUnknown, "":
		return 2
	case FunnelEdgeHealthUnhealthy:
		return 1
	default:
		return 0
	}
}

func listAllocatableRemoteEdges(tx *gorm.DB) ([]FunnelEdge, error) {
	var edges []FunnelEdge
	if err := tx.Where("edge_type <> ? AND allocatable = ?", FunnelEdgeTypeServer, true).Find(&edges).Error; err != nil {
		return nil, err
	}
	sort.SliceStable(edges, func(i, j int) bool {
		leftRank := funnelEdgeHealthRank(edges[i].HealthStatus)
		rightRank := funnelEdgeHealthRank(edges[j].HealthStatus)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		leftSeen := time.Time{}
		rightSeen := time.Time{}
		if edges[i].LastSeen != nil {
			leftSeen = *edges[i].LastSeen
		}
		if edges[j].LastSeen != nil {
			rightSeen = *edges[j].LastSeen
		}
		if !leftSeen.Equal(rightSeen) {
			return leftSeen.After(rightSeen)
		}
		return edges[i].CreatedAt.Before(edges[j].CreatedAt)
	})
	return edges, nil
}

func resolveFunnelEdgeForService(tx *gorm.DB, domain *FunnelDomain, service *FunnelService) (*FunnelEdge, error) {
	if domain == nil {
		return nil, fmt.Errorf("nil funnel domain")
	}
	edgeMode := strings.ToLower(strings.TrimSpace(domain.EdgeMode))
	if edgeMode == "" || edgeMode == FunnelEdgeModeServer {
		if strings.TrimSpace(domain.EdgeTargetID) != "" {
			return resolveFunnelEdge(tx, "", domain.EdgeTargetID)
		}
		if edge, err := resolveFunnelEdge(tx, "", "server-edge"); err == nil {
			return edge, nil
		}
		return resolveFunnelEdge(tx, FunnelEdgeTypeServer, "")
	}

	if targetID := strings.TrimSpace(domain.EdgeTargetID); targetID != "" {
		edge, err := resolveFunnelEdge(tx, "", targetID)
		if err != nil {
			return nil, err
		}
		if edge.EdgeType == FunnelEdgeTypeServer || !edge.Allocatable {
			return nil, fmt.Errorf("funnel remote edge unavailable")
		}
		if service != nil {
			if !funnelRemoteEdgeFirstSliceSupportsProto(service.ListenProto) {
				return nil, fmt.Errorf("remote edge first slice does not support %s", service.ListenProto)
			}
			if !funnelEdgeSupportsService(edge, service) {
				return nil, fmt.Errorf("remote edge does not support service protocol")
			}
		}
		return edge, nil
	}

	edges, err := listAllocatableRemoteEdges(tx)
	if err != nil {
		return nil, err
	}
	for i := range edges {
		edge := edges[i]
		if service != nil {
			if !funnelRemoteEdgeFirstSliceSupportsProto(service.ListenProto) {
				continue
			}
			if !funnelEdgeSupportsService(&edge, service) {
				continue
			}
		}
		return &edge, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func buildFunnelEdgeSyncPayload(tx *gorm.DB, edge *FunnelEdge) (*FunnelEdgeSyncPayload, error) {
	if edge == nil {
		return nil, fmt.Errorf("nil funnel edge")
	}

	var services []FunnelService
	if err := tx.Order("created_at ASC").Find(&services).Error; err != nil {
		return nil, err
	}
	if len(services) == 0 {
		return &FunnelEdgeSyncPayload{
			Edge:         funnelEdgeToMap(edge),
			Services:     []FunnelEdgeSyncService{},
			ServiceCount: 0,
			GeneratedAt:  funnelStatusStamp(),
			SyncDeferred: false,
		}, nil
	}

	domainIDs := make([]int64, 0, len(services))
	for _, service := range services {
		domainIDs = append(domainIDs, service.DomainID)
	}

	var domains []FunnelDomain
	if err := tx.Where("id IN ?", domainIDs).Find(&domains).Error; err != nil {
		return nil, err
	}
	domainByID := make(map[int64]*FunnelDomain, len(domains))
	for i := range domains {
		domain := domains[i]
		domainByID[domain.ID] = &domain
	}

	var certs []FunnelCert
	if err := tx.Where("domain_id IN ?", domainIDs).Find(&certs).Error; err != nil {
		return nil, err
	}
	certByDomainID := make(map[int64]*FunnelCert, len(certs))
	for i := range certs {
		cert := certs[i]
		certByDomainID[cert.DomainID] = &cert
	}

	items := make([]FunnelEdgeSyncService, 0)
	for i := range services {
		service := services[i]
		if !service.Enabled {
			continue
		}
		domain := domainByID[service.DomainID]
		if domain == nil || domain.EdgeMode != FunnelEdgeModeRemote {
			continue
		}
		selectedEdge, err := resolveFunnelEdgeForService(tx, domain, &service)
		if err != nil || selectedEdge == nil || selectedEdge.ID != edge.ID {
			continue
		}
		if !funnelRemoteEdgeFirstSliceSupportsProto(service.ListenProto) {
			continue
		}
		items = append(items, FunnelEdgeSyncService{
			OrgID:   service.OrgID,
			Service: funnelServiceToMap(&service),
			Domain:  funnelDomainToMap(domain),
			Cert:    funnelCertToMap(certByDomainID[domain.ID]),
			Public: FunnelEdgeSyncPublic{
				Host:         domain.Domain,
				ListenProto:  service.ListenProto,
				ListenPort:   service.ListenPort,
				MountPath:    service.MountPath,
				ListenerMode: domain.ListenerMode,
				TLSMode:      domain.TLSMode,
			},
			Backend: FunnelEdgeSyncBackend{
				Network: "tcp",
				Address: service.BackendTailnetIP,
				Port:    service.BackendPort,
				Scheme:  service.BackendScheme,
			},
		})
	}

	return &FunnelEdgeSyncPayload{
		Edge:         funnelEdgeToMap(edge),
		Services:     items,
		ServiceCount: len(items),
		GeneratedAt:  funnelStatusStamp(),
		SyncDeferred: false,
	}, nil
}

func normalizeProxyCIDRs(cidrs []string) StringList {
	normalized := normalizeStringList(cidrs)
	return StringList(normalized)
}

func resolveFunnelEdge(db *gorm.DB, edgeType, edgeNodeID string) (*FunnelEdge, error) {
	edge := &FunnelEdge{}
	query := db.Model(&FunnelEdge{})
	if strings.TrimSpace(edgeNodeID) != "" {
		query = query.Where("edge_node_id = ?", strings.TrimSpace(edgeNodeID))
	} else {
		query = query.Where("edge_type = ?", strings.TrimSpace(edgeType))
	}
	if err := query.First(edge).Error; err != nil {
		return nil, err
	}
	return edge, nil
}

func upsertFunnelEdge(db *gorm.DB, desired *FunnelEdge) (*FunnelEdge, error) {
	if desired == nil {
		return nil, fmt.Errorf("nil funnel edge")
	}
	current := &FunnelEdge{}
	query := db.Model(&FunnelEdge{})
	switch {
	case strings.TrimSpace(desired.StableID) != "":
		query = query.Where("stable_id = ?", strings.TrimSpace(desired.StableID))
	case strings.TrimSpace(desired.EdgeNodeID) != "":
		query = query.Where("edge_node_id = ?", strings.TrimSpace(desired.EdgeNodeID))
	default:
		return nil, fmt.Errorf("missing funnel edge identity")
	}
	if err := query.First(current).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err := db.Create(desired).Error; err != nil {
			return nil, err
		}
		return desired, nil
	}
	current.EdgeType = desired.EdgeType
	current.EdgeNodeID = desired.EdgeNodeID
	current.Hostname = desired.Hostname
	current.PublicAddrs = desired.PublicAddrs
	current.SyncEndpoint = desired.SyncEndpoint
	current.ListenerCapabilities = desired.ListenerCapabilities
	current.HealthStatus = desired.HealthStatus
	current.LastSeen = desired.LastSeen
	current.Allocatable = desired.Allocatable
	current.TrustProxyCIDRs = desired.TrustProxyCIDRs
	if err := db.Save(current).Error; err != nil {
		return nil, err
	}
	return current, nil
}

func domainActionPayload(domain *FunnelDomain, extra map[string]any) map[string]any {
	payload := map[string]any{
		"domain": funnelDomainToMap(domain),
	}
	for k, v := range extra {
		payload[k] = v
	}
	return payload
}

func certActionPayload(cert *FunnelCert, extra map[string]any) map[string]any {
	payload := map[string]any{
		"cert": funnelCertToMap(cert),
	}
	for k, v := range extra {
		payload[k] = v
	}
	return payload
}

func funnelDomainToMap(domain *FunnelDomain) map[string]any {
	if domain == nil {
		return nil
	}
	return map[string]any{
		"id":                  strconv.FormatInt(domain.ID, 10),
		"stableId":            domain.StableID,
		"orgId":               strconv.FormatInt(domain.OrgID, 10),
		"domain":              domain.Domain,
		"domainType":          domain.DomainType,
		"status":              domain.Status,
		"dnsStatus":           domain.DNSStatus,
		"tlsMode":             domain.TLSMode,
		"listenerMode":        domain.ListenerMode,
		"edgeMode":            domain.EdgeMode,
		"edgeTargetId":        domain.EdgeTargetID,
		"httpPort":            domain.HTTPPort,
		"httpsPort":           domain.HTTPSPort,
		"validationMethod":    domain.ValidationMethod,
		"validationTarget":    domain.ValidationTarget,
		"validationToken":     domain.ValidationToken,
		"validationCheckedAt": domain.ValidationCheckedAt,
		"certId":              nullableInt64ToString(domain.CertID),
		"lastError":           domain.LastError,
		"lastDnsError":        domain.LastDNSError,
		"createdAt":           domain.CreatedAt,
		"updatedAt":           domain.UpdatedAt,
	}
}

func funnelServiceToMap(service *FunnelService) map[string]any {
	if service == nil {
		return nil
	}
	return map[string]any{
		"id":                  strconv.FormatInt(service.ID, 10),
		"stableId":            service.StableID,
		"orgId":               strconv.FormatInt(service.OrgID, 10),
		"machineId":           strconv.FormatInt(service.MachineID, 10),
		"domainId":            strconv.FormatInt(service.DomainID, 10),
		"enabled":             service.Enabled,
		"public":              service.Public,
		"listenProto":         service.ListenProto,
		"listenPort":          service.ListenPort,
		"mountPath":           service.MountPath,
		"backendType":         service.BackendType,
		"backendScheme":       service.BackendScheme,
		"backendTailnetIp":    service.BackendTailnetIP,
		"backendPort":         service.BackendPort,
		"targetMachineOnline": service.TargetMachineOnline,
		"configStatus":        service.ConfigStatus,
		"edgeStatus":          service.EdgeStatus,
		"backendStatus":       service.BackendStatus,
		"lastError":           service.LastError,
		"createdAt":           service.CreatedAt,
		"updatedAt":           service.UpdatedAt,
	}
}

func funnelEdgeToMap(edge *FunnelEdge) map[string]any {
	if edge == nil {
		return nil
	}
	return map[string]any{
		"id":                   strconv.FormatInt(edge.ID, 10),
		"stableId":             edge.StableID,
		"edgeType":             edge.EdgeType,
		"edgeNodeId":           edge.EdgeNodeID,
		"hostname":             edge.Hostname,
		"publicAddrs":          edge.PublicAddrs,
		"syncEndpoint":         edge.SyncEndpoint,
		"listenerCapabilities": edge.ListenerCapabilities,
		"healthStatus":         edge.HealthStatus,
		"lastSeen":             edge.LastSeen,
		"allocatable":          edge.Allocatable,
		"trustProxyCidrs":      edge.TrustProxyCIDRs,
		"createdAt":            edge.CreatedAt,
		"updatedAt":            edge.UpdatedAt,
	}
}

func funnelCertToMap(cert *FunnelCert) map[string]any {
	if cert == nil {
		return nil
	}
	return map[string]any{
		"id":             strconv.FormatInt(cert.ID, 10),
		"stableId":       cert.StableID,
		"domainId":       strconv.FormatInt(cert.DomainID, 10),
		"issuer":         cert.Issuer,
		"challengeType":  cert.ChallengeType,
		"certStatus":     cert.CertStatus,
		"certificateRef": cert.CertificateRef,
		"privateKeyRef":  cert.PrivateKeyRef,
		"notBefore":      cert.NotBefore,
		"notAfter":       cert.NotAfter,
		"renewAfter":     cert.RenewAfter,
		"lastError":      cert.LastError,
		"createdAt":      cert.CreatedAt,
		"updatedAt":      cert.UpdatedAt,
	}
}

func nullableInt64ToString(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func recordFunnelAudit(tx *gorm.DB, orgID int64, actorType, actorID, resourceType, resourceID, action string, payload any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return tx.Create(&FunnelAudit{
		OrgID:        orgID,
		ActorType:    actorType,
		ActorID:      actorID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Payload:      string(payloadBytes),
	}).Error
}

func parseFunnelID(value string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
}

func getFunnelDomainByID(tx *gorm.DB, id int64) (*FunnelDomain, error) {
	domain := &FunnelDomain{}
	if err := tx.First(domain, id).Error; err != nil {
		return nil, err
	}
	return domain, nil
}

func getFunnelCertByDomainID(tx *gorm.DB, domainID int64) (*FunnelCert, error) {
	cert := &FunnelCert{}
	if err := tx.Where("domain_id = ?", domainID).First(cert).Error; err != nil {
		return nil, err
	}
	return cert, nil
}

func ensureFunnelDomainVisibility(domain *FunnelDomain) map[string]any {
	if domain == nil {
		return nil
	}
	return domainActionPayload(domain, nil)
}

func ensureFunnelCertVisibility(cert *FunnelCert) map[string]any {
	if cert == nil {
		return nil
	}
	return certActionPayload(cert, nil)
}

func (c *Cockpit) authFunnelActor(w http.ResponseWriter, r *http.Request) bool {
	if c.superAdmin == nil {
		c.doAPIResponse(w, "noadmin", nil)
		return false
	}
	authCookie, err := r.Cookie("mirage_cockpit_auth")
	authCode, ok := c.authCache.Get("AuthCode")
	if err != nil || !ok || authCookie.Value != authCode.(string) {
		c.doAPIResponse(w, "unauthorized", nil)
		return false
	}
	return true
}

func (c *Cockpit) currentFunnelSysCfg() (*SysConfig, error) {
	sysCfg := c.GetSysCfg()
	if sysCfg == nil {
		return nil, fmt.Errorf("获取系统配置失败")
	}
	if strings.TrimSpace(sysCfg.FunnelCfg.ManagedBaseDomain) == "" && strings.ToLower(strings.TrimSpace(sysCfg.FunnelCfg.ManagedDNSProvider)) != FunnelManagedDNSProviderDNSMgr {
		sysCfg.FunnelCfg.ManagedBaseDomain = sysCfg.Basedomain
	}
	return sysCfg, nil
}

func (c *Cockpit) funnelConfigEnvelope(sysCfg *SysConfig) (map[string]any, error) {
	cfg, err := effectiveFunnelPlatformConfig(sysCfg)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"config":                  cfg,
		"effectiveIngressTargets": effectiveFunnelIngressTargets(cfg),
	}, nil
}

func (c *Cockpit) ensureDefaultServerEdge(sysCfg *SysConfig) (*FunnelEdge, error) {
	cfg, err := effectiveFunnelPlatformConfig(sysCfg)
	if err != nil {
		return nil, err
	}
	desired := buildDefaultServerEdge(cfg)
	return upsertFunnelEdge(c.db, &desired)
}

func (c *Cockpit) renderFunnelActionError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	c.doAPIResponse(w, err.Error(), nil)
}

func parseFunnelBody(r *http.Request, out interface{}) error {
	return json.NewDecoder(r.Body).Decode(out)
}

func isFunnelMutationAllowed(userRole int64) bool {
	return userRole == RoleOwner
}

func funnelStatusStamp() time.Time {
	return time.Now().UTC()
}

func funnelAuditActionForEdge(edgeAction string) string {
	return edgeAction
}

func currentOrgOwnerID(c *Cockpit, orgID int64) (int64, error) {
	users, err := c.ListTenantUsers(orgID)
	if err != nil {
		return 0, err
	}
	for _, user := range users {
		if user.Role == RoleOwner {
			return user.ID, nil
		}
	}
	return 0, fmt.Errorf("tenant owner not found")
}

func serializeFunnelDomainResponse(domain *FunnelDomain) map[string]any {
	return funnelDomainToMap(domain)
}

func serializeFunnelCertResponse(cert *FunnelCert) map[string]any {
	return funnelCertToMap(cert)
}

func serializeFunnelEdgeResponse(edge *FunnelEdge) map[string]any {
	return funnelEdgeToMap(edge)
}

func serializeFunnelServiceResponse(service *FunnelService) map[string]any {
	return funnelServiceToMap(service)
}

func funnelResponse(data map[string]any) map[string]any {
	return data
}

func funnelDomainActionData(domain *FunnelDomain, extra map[string]any) map[string]any {
	return domainActionPayload(domain, extra)
}

func funnelCertActionData(cert *FunnelCert, extra map[string]any) map[string]any {
	return certActionPayload(cert, extra)
}

func funnelDomainWithVerifiedFlag(domain *FunnelDomain) map[string]any {
	return domainActionPayload(domain, map[string]any{
		"verificationDeferred": true,
	})
}

func funnelCertWithRenewDeferred(cert *FunnelCert) map[string]any {
	return certActionPayload(cert, map[string]any{
		"renewDeferred": true,
	})
}

func funnelEdgeWithSyncDeferred(edge *FunnelEdge) map[string]any {
	return map[string]any{
		"edge":         funnelEdgeToMap(edge),
		"syncDeferred": true,
	}
}

func funnelDomainsResponse(domains []FunnelDomain) []map[string]any {
	items := make([]map[string]any, 0, len(domains))
	for i := range domains {
		items = append(items, funnelDomainToMap(&domains[i]))
	}
	return items
}

func funnelEdgesResponse(edges []FunnelEdge) []map[string]any {
	items := make([]map[string]any, 0, len(edges))
	for i := range edges {
		items = append(items, funnelEdgeToMap(&edges[i]))
	}
	return items
}

func funnelServiceStatusResponse(service *FunnelService, domain *FunnelDomain, cert *FunnelCert, edge *FunnelEdge) map[string]any {
	return map[string]any{
		"service":     funnelServiceToMap(service),
		"domain":      funnelDomainToMap(domain),
		"cert":        funnelCertToMap(cert),
		"currentEdge": funnelEdgeToMap(edge),
		"lastError":   service.LastError,
	}
}

func funnelDomainFromRequest(req FunnelPlatformConfigRequest) FunnelPlatformConfig {
	return FunnelPlatformConfig{
		ManagedBaseDomain:       req.ManagedBaseDomain,
		DefaultEdgeMode:         req.DefaultEdgeMode,
		DefaultListenerMode:     req.DefaultListenerMode,
		DirectBindAddrs:         req.DirectBindAddrs,
		DirectBindPorts:         req.DirectBindPorts,
		TrustedProxyCIDRs:       req.TrustedProxyCIDRs,
		ManagedDNSProvider:      req.ManagedDNSProvider,
		ManagedDNSAPIBaseURL:    req.ManagedDNSAPIBaseURL,
		ManagedDNSUID:           req.ManagedDNSUID,
		ManagedDNSAPIKey:        req.ManagedDNSAPIKey,
		ManagedDNSSkipTLSVerify: req.ManagedDNSSkipTLSVerify,
	}
}

func safeFirstPathSegment(pathValue string) string {
	if pathValue == "" {
		return ""
	}
	return strings.Trim(strings.SplitN(path.Clean("/"+strings.TrimSpace(pathValue)), "/", 3)[1], "/")
}
