package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/acme/autocert"
	"gorm.io/gorm"
	"tailscale.com/tsnet"
)

const (
	funnelRuntimeReloadInterval = 15 * time.Second
	funnelServiceLogLimit       = 100
	funnelEdgeUserPrefix        = "funnel-edge-"
	funnelRuntimeStateDir       = "funnel-state"
)

type funnelListenerKey struct {
	Port int
	TLS  bool
}

type funnelLogEntry struct {
	At      time.Time
	Level   string
	Message string
}

type funnelRuntimeStatus struct {
	TargetMachineOnline bool

	ServiceConfigStatus  string
	ServiceEdgeStatus    string
	ServiceBackendStatus string
	ServiceLastError     string

	DomainStatus    string
	DomainDNSStatus string
	DomainLastError string

	CertStatus     string
	CertIssuer     string
	CertNotBefore  *time.Time
	CertNotAfter   *time.Time
	CertRenewAfter *time.Time
	CertLastError  string
}

type funnelRoute struct {
	ServiceID       int64
	OrgID           int64
	DomainID        int64
	Domain          string
	ListenPort      int
	ListenProto     string
	MountPath       string
	BackendType     string
	BackendScheme   string
	BackendAddress  string
	TargetMachineID int64
	Proxy           *httputil.ReverseProxy
}

type funnelPortRoutes struct {
	RoutesByHost map[string][]*funnelRoute
}

type funnelTCPPortRoutes struct {
	DefaultRoute *funnelRoute
	RoutesByHost map[string]*funnelRoute
}

type funnelRuntimeSnapshot struct {
	PlainPorts    map[int]*funnelPortRoutes
	TLSPorts      map[int]*funnelPortRoutes
	TCPPorts      map[int]*funnelTCPPortRoutes
	TLSTCPPorts   map[int]*funnelTCPPortRoutes
	TLSHosts      map[string]struct{}
	ServiceStatus map[int64]funnelRuntimeStatus
}

type funnelPortFamily string

const (
	funnelPortFamilyHTTPPlain funnelPortFamily = "http"
	funnelPortFamilyHTTPTLS   funnelPortFamily = "https"
	funnelPortFamilyTCPPlain  funnelPortFamily = "tcp"
	funnelPortFamilyTCPTLS    funnelPortFamily = "tls_terminated_tcp"
)

type funnelListenerState struct {
	Key       funnelListenerKey
	Server    *http.Server
	Listeners []net.Listener
}

type funnelTCPListenerState struct {
	Key       funnelListenerKey
	Listeners []net.Listener
	Cancel    context.CancelFunc
}

type funnelOrgDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	Close() error
}

type funnelOrgEdge struct {
	orgID  int64
	server *tsnet.Server
}

func (e *funnelOrgEdge) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return e.server.Dial(ctx, network, address)
}

func (e *funnelOrgEdge) Close() error {
	return e.server.Close()
}

type funnelRuntime struct {
	app    *Mirage
	ctx    context.Context
	cancel context.CancelFunc

	reloadCh chan string

	mu           sync.RWMutex
	closed       bool
	snapshot     *funnelRuntimeSnapshot
	listeners    map[funnelListenerKey]*funnelListenerState
	tcpListeners map[funnelListenerKey]*funnelTCPListenerState
	orgDialers   map[int64]funnelOrgDialer
	serviceLogs  map[int64][]funnelLogEntry

	certManager *autocert.Manager
	getCertFunc func(*tls.ClientHelloInfo) (*tls.Certificate, error)

	newOrgDialer func(ctx context.Context, orgID int64) (funnelOrgDialer, error)

	wg sync.WaitGroup
}

func newFunnelRuntime(app *Mirage) *funnelRuntime {
	ctx, cancel := context.WithCancel(app.ctx)
	rt := &funnelRuntime{
		app:          app,
		ctx:          ctx,
		cancel:       cancel,
		reloadCh:     make(chan string, 1),
		listeners:    make(map[funnelListenerKey]*funnelListenerState),
		tcpListeners: make(map[funnelListenerKey]*funnelTCPListenerState),
		orgDialers:   make(map[int64]funnelOrgDialer),
		serviceLogs:  make(map[int64][]funnelLogEntry),
	}
	cacheDir := AbsolutePathFromConfigPath(filepath.Join(funnelRuntimeStateDir, "autocert"))
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			log.Error().Err(err).Str("dir", cacheDir).Msg("failed to create funnel autocert cache dir")
		}
	}
	rt.certManager = &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(cacheDir),
		HostPolicy: rt.autocertHostPolicy,
	}
	rt.getCertFunc = rt.getFunnelCertificate
	rt.newOrgDialer = rt.newTSNetOrgDialer
	return rt
}

func (h *Mirage) setFunnelRuntime(rt *funnelRuntime) {
	h.funnelRuntimeMu.Lock()
	defer h.funnelRuntimeMu.Unlock()
	h.funnelRuntime = rt
}

func (h *Mirage) currentFunnelRuntime() *funnelRuntime {
	h.funnelRuntimeMu.RLock()
	defer h.funnelRuntimeMu.RUnlock()
	return h.funnelRuntime
}

func (h *Mirage) requestFunnelRuntimeReload() {
	if rt := h.currentFunnelRuntime(); rt != nil {
		rt.requestReload("api")
	}
}

func (rt *funnelRuntime) start() error {
	rt.wg.Add(1)
	go rt.run()
	rt.requestReload("startup")
	return nil
}

func (rt *funnelRuntime) close() {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return
	}
	rt.closed = true
	rt.mu.Unlock()

	rt.cancel()
	rt.wg.Wait()
}

func (rt *funnelRuntime) run() {
	defer rt.wg.Done()

	ticker := time.NewTicker(funnelRuntimeReloadInterval)
	defer ticker.Stop()
	defer rt.shutdownAll()

	for {
		select {
		case <-rt.ctx.Done():
			return
		case <-ticker.C:
			rt.reload("periodic")
		case <-rt.reloadCh:
			rt.reload("triggered")
		}
	}
}

func (rt *funnelRuntime) requestReload(reason string) {
	rt.mu.RLock()
	closed := rt.closed
	rt.mu.RUnlock()
	if closed {
		return
	}
	select {
	case rt.reloadCh <- reason:
	default:
	}
}

func (rt *funnelRuntime) reload(reason string) {
	snapshot, cfg, err := rt.buildSnapshot()
	if err != nil {
		log.Error().Err(err).Str("reason", reason).Msg("failed to rebuild funnel runtime snapshot")
		return
	}

	listenerErrors := rt.syncListeners(cfg, snapshot)
	for key, errText := range rt.syncTCPListeners(cfg, snapshot) {
		listenerErrors[key] = errText
	}
	if len(listenerErrors) > 0 {
		for key, bindErr := range listenerErrors {
			for _, route := range funnelRoutesForListener(snapshot, key) {
				status := snapshot.ServiceStatus[route.ServiceID]
				status.ServiceConfigStatus = FunnelServiceConfigStatusError
				status.ServiceEdgeStatus = FunnelServiceEdgeStatusUnavailable
				status.ServiceLastError = bindErr
				snapshot.ServiceStatus[route.ServiceID] = status
				rt.appendLog(route.ServiceID, "error", bindErr)
			}
		}
	}

	rt.mu.Lock()
	rt.snapshot = snapshot
	rt.mu.Unlock()

	if err := rt.updateServerEdgeStatus(cfg, len(listenerErrors) == 0); err != nil {
		log.Error().Err(err).Msg("failed to update server-edge status")
	}
}

func (rt *funnelRuntime) buildSnapshot() (*funnelRuntimeSnapshot, FunnelPlatformConfig, error) {
	cfg, err := effectiveFunnelPlatformConfigFromDB(rt.app.db, rt.app.cfg.BaseDomain)
	if err != nil {
		return nil, FunnelPlatformConfig{}, err
	}

	snapshot := &funnelRuntimeSnapshot{
		PlainPorts:    make(map[int]*funnelPortRoutes),
		TLSPorts:      make(map[int]*funnelPortRoutes),
		TCPPorts:      make(map[int]*funnelTCPPortRoutes),
		TLSTCPPorts:   make(map[int]*funnelTCPPortRoutes),
		TLSHosts:      make(map[string]struct{}),
		ServiceStatus: make(map[int64]funnelRuntimeStatus),
	}

	var services []FunnelService
	if err := rt.app.db.Order("created_at ASC").Find(&services).Error; err != nil {
		return nil, FunnelPlatformConfig{}, err
	}
	if len(services) == 0 {
		return snapshot, cfg, nil
	}

	domainIDs := make([]int64, 0, len(services))
	machineIDs := make([]int64, 0, len(services))
	for _, service := range services {
		domainIDs = append(domainIDs, service.DomainID)
		machineIDs = append(machineIDs, service.MachineID)
	}

	var domains []FunnelDomain
	if err := rt.app.db.Where("id IN ?", domainIDs).Find(&domains).Error; err != nil {
		return nil, FunnelPlatformConfig{}, err
	}
	domainByID := make(map[int64]*FunnelDomain, len(domains))
	for i := range domains {
		domain := domains[i]
		domainByID[domain.ID] = &domain
	}

	var certs []FunnelCert
	if err := rt.app.db.Where("domain_id IN ?", domainIDs).Find(&certs).Error; err != nil {
		return nil, FunnelPlatformConfig{}, err
	}
	certByDomainID := make(map[int64]*FunnelCert, len(certs))
	for i := range certs {
		cert := certs[i]
		certByDomainID[cert.DomainID] = &cert
	}

	var machines []Machine
	if err := rt.app.db.Preload("User").Where("id IN ?", machineIDs).Find(&machines).Error; err != nil {
		return nil, FunnelPlatformConfig{}, err
	}
	machineByID := make(map[int64]*Machine, len(machines))
	for i := range machines {
		machine := machines[i]
		machineByID[machine.ID] = &machine
	}

	allowedPorts := make(map[int]struct{}, len(cfg.DirectBindPorts))
	for _, port := range cfg.DirectBindPorts {
		allowedPorts[port] = struct{}{}
	}

	seenRouteKeys := make(map[string]int64)
	portFamilies := make(map[int]funnelPortFamily)

	for i := range services {
		service := services[i]
		domain := domainByID[service.DomainID]
		cert := certByDomainID[service.DomainID]
		machine := machineByID[service.MachineID]

		status := funnelRuntimeStatus{
			ServiceConfigStatus:  service.ConfigStatus,
			ServiceEdgeStatus:    service.EdgeStatus,
			ServiceBackendStatus: service.BackendStatus,
			ServiceLastError:     service.LastError,
		}
		if domain != nil {
			status.DomainStatus = domain.Status
			status.DomainDNSStatus = domain.DNSStatus
			status.DomainLastError = domain.LastError
		}
		if cert != nil {
			status.CertStatus = cert.CertStatus
			status.CertIssuer = cert.Issuer
			status.CertNotBefore = cert.NotBefore
			status.CertNotAfter = cert.NotAfter
			status.CertRenewAfter = cert.RenewAfter
			status.CertLastError = cert.LastError
		} else {
			status.CertStatus = FunnelCertStatusPending
		}
		if machine != nil {
			status.TargetMachineOnline = machine.isOnline()
		}

		switch {
		case !service.Enabled:
			status.ServiceConfigStatus = FunnelServiceConfigStatusDisabled
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusUnavailable
			status.ServiceBackendStatus = FunnelServiceConfigStatusDisabled
			snapshot.ServiceStatus[service.ID] = status
			continue
		case domain == nil:
			status.ServiceConfigStatus = FunnelServiceConfigStatusError
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusUnavailable
			status.ServiceBackendStatus = FunnelServiceBackendStatusError
			status.ServiceLastError = "Funnel域名不存在"
			snapshot.ServiceStatus[service.ID] = status
			continue
		case domain.EdgeMode != FunnelEdgeModeServer:
			status.ServiceConfigStatus = FunnelServiceConfigStatusPending
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusPending
			status.ServiceBackendStatus = FunnelServiceBackendStatusUnknown
			status.ServiceLastError = "当前服务未分配到server-edge"
			snapshot.ServiceStatus[service.ID] = status
			continue
		}

		portFamily, ok := funnelPortFamilyForProto(service.ListenProto)
		if !ok {
			status.ServiceConfigStatus = FunnelServiceConfigStatusPending
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusPending
			status.ServiceBackendStatus = FunnelServiceBackendStatusUnknown
			status.ServiceLastError = "当前server-edge尚未支持该协议"
			snapshot.ServiceStatus[service.ID] = status
			continue
		}

		if _, ok := allowedPorts[service.ListenPort]; !ok {
			status.ServiceConfigStatus = FunnelServiceConfigStatusError
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusUnavailable
			status.ServiceBackendStatus = FunnelServiceBackendStatusError
			status.ServiceLastError = fmt.Sprintf("监听端口 %d 未在 Funnel 平台允许列表中", service.ListenPort)
			snapshot.ServiceStatus[service.ID] = status
			continue
		}

		switch portFamily {
		case funnelPortFamilyHTTPPlain, funnelPortFamilyHTTPTLS:
			if service.BackendType != FunnelBackendTypeHTTPProxy {
				status.ServiceConfigStatus = FunnelServiceConfigStatusPending
				status.ServiceEdgeStatus = FunnelServiceEdgeStatusPending
				status.ServiceBackendStatus = FunnelServiceBackendStatusUnknown
				status.ServiceLastError = "当前server-edge仅支持HTTP/HTTPS/WS/WSS协议使用HTTP代理后端"
				snapshot.ServiceStatus[service.ID] = status
				continue
			}
		case funnelPortFamilyTCPPlain, funnelPortFamilyTCPTLS:
			if service.BackendType != FunnelBackendTypeTCPProxy {
				status.ServiceConfigStatus = FunnelServiceConfigStatusPending
				status.ServiceEdgeStatus = FunnelServiceEdgeStatusPending
				status.ServiceBackendStatus = FunnelServiceBackendStatusUnknown
				status.ServiceLastError = "当前server-edge仅支持TCP类协议使用TCP代理后端"
				snapshot.ServiceStatus[service.ID] = status
				continue
			}
		default:
			status.ServiceConfigStatus = FunnelServiceConfigStatusPending
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusPending
			status.ServiceBackendStatus = FunnelServiceBackendStatusUnknown
			status.ServiceLastError = "当前server-edge尚未支持该协议"
			snapshot.ServiceStatus[service.ID] = status
			continue
		}

		if existingFamily, ok := portFamilies[service.ListenPort]; ok && existingFamily != portFamily {
			status.ServiceConfigStatus = FunnelServiceConfigStatusError
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusUnavailable
			status.ServiceBackendStatus = FunnelServiceBackendStatusError
			status.ServiceLastError = fmt.Sprintf("监听端口 %d 已被 %s 协议族占用", service.ListenPort, existingFamily)
			snapshot.ServiceStatus[service.ID] = status
			continue
		}
		portFamilies[service.ListenPort] = portFamily

		domainReady := domain.DomainType == FunnelDomainTypeManaged || domain.ValidationCheckedAt != nil
		if domainReady {
			status.DomainDNSStatus = FunnelDNSStatusReady
			if funnelPortFamilyUsesTLS(portFamily) && (status.DomainStatus == "" || status.DomainStatus == FunnelDomainStatusPendingDNS) {
				status.DomainStatus = FunnelDomainStatusPendingCert
			} else if !funnelPortFamilyUsesTLS(portFamily) {
				status.DomainStatus = FunnelDomainStatusActive
			}
		} else {
			status.DomainDNSStatus = FunnelDNSStatusPending
			status.DomainStatus = FunnelDomainStatusPendingDNS
			status.ServiceConfigStatus = FunnelServiceConfigStatusPending
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusPending
			status.ServiceBackendStatus = FunnelServiceBackendStatusUnknown
			status.ServiceLastError = "域名尚未完成验证"
			snapshot.ServiceStatus[service.ID] = status
			continue
		}

		if service.BackendTailnetIP == "" || service.BackendPort <= 0 {
			status.ServiceConfigStatus = FunnelServiceConfigStatusError
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusUnavailable
			status.ServiceBackendStatus = FunnelServiceBackendStatusError
			status.ServiceLastError = "后端地址配置不完整"
			snapshot.ServiceStatus[service.ID] = status
			continue
		}

		if status.TargetMachineOnline {
			status.ServiceBackendStatus = FunnelServiceBackendStatusUnknown
		} else {
			status.ServiceBackendStatus = FunnelServiceBackendStatusDegraded
			status.ServiceLastError = "目标设备当前离线"
		}

		if funnelPortFamilyUsesTLS(portFamily) && strings.EqualFold(domain.TLSMode, FunnelTLSModeBringYourOwn) {
			status.ServiceConfigStatus = FunnelServiceConfigStatusPending
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusPending
			status.ServiceLastError = "自带证书模式尚未实现"
			snapshot.ServiceStatus[service.ID] = status
			continue
		}

		route := &funnelRoute{
			ServiceID:       service.ID,
			OrgID:           service.OrgID,
			DomainID:        domain.ID,
			Domain:          normalizeFunnelBaseDomain(domain.Domain),
			ListenPort:      service.ListenPort,
			ListenProto:     service.ListenProto,
			MountPath:       normalizeFunnelMountPath(service.MountPath),
			BackendType:     service.BackendType,
			BackendScheme:   strings.ToLower(strings.TrimSpace(service.BackendScheme)),
			BackendAddress:  net.JoinHostPort(service.BackendTailnetIP, strconv.Itoa(service.BackendPort)),
			TargetMachineID: service.MachineID,
		}
		if route.BackendType == FunnelBackendTypeHTTPProxy && route.BackendScheme == "" {
			route.BackendScheme = "http"
		}

		listenerKey := funnelListenerKey{Port: service.ListenPort}
		if funnelPortFamilyUsesTLS(portFamily) {
			listenerKey.TLS = true
			snapshot.TLSHosts[route.Domain] = struct{}{}
			if status.CertStatus == "" {
				status.CertStatus = FunnelCertStatusPending
			}
		}

		routeKey := funnelRuntimeRouteKey(portFamily, route)
		if existingID, ok := seenRouteKeys[routeKey]; ok {
			status.ServiceConfigStatus = FunnelServiceConfigStatusError
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusUnavailable
			status.ServiceBackendStatus = FunnelServiceBackendStatusError
			status.ServiceLastError = fmt.Sprintf("与服务 %d 存在重复的公开路由", existingID)
			snapshot.ServiceStatus[service.ID] = status
			continue
		}
		seenRouteKeys[routeKey] = service.ID

		switch portFamily {
		case funnelPortFamilyHTTPPlain, funnelPortFamilyHTTPTLS:
			route.Proxy = rt.newReverseProxy(route)
			portRoutes := snapshot.PlainPorts[service.ListenPort]
			if listenerKey.TLS {
				portRoutes = snapshot.TLSPorts[service.ListenPort]
			}
			if portRoutes == nil {
				portRoutes = &funnelPortRoutes{RoutesByHost: make(map[string][]*funnelRoute)}
				if listenerKey.TLS {
					snapshot.TLSPorts[service.ListenPort] = portRoutes
				} else {
					snapshot.PlainPorts[service.ListenPort] = portRoutes
				}
			}
			hostRoutes := append(portRoutes.RoutesByHost[route.Domain], route)
			sort.SliceStable(hostRoutes, func(i, j int) bool {
				return len(hostRoutes[i].MountPath) > len(hostRoutes[j].MountPath)
			})
			portRoutes.RoutesByHost[route.Domain] = hostRoutes
		case funnelPortFamilyTCPPlain:
			portRoutes := snapshot.TCPPorts[service.ListenPort]
			if portRoutes == nil {
				portRoutes = &funnelTCPPortRoutes{}
				snapshot.TCPPorts[service.ListenPort] = portRoutes
			}
			portRoutes.DefaultRoute = route
		case funnelPortFamilyTCPTLS:
			portRoutes := snapshot.TLSTCPPorts[service.ListenPort]
			if portRoutes == nil {
				portRoutes = &funnelTCPPortRoutes{}
				snapshot.TLSTCPPorts[service.ListenPort] = portRoutes
			}
			portRoutes.DefaultRoute = route
		default:
			status.ServiceConfigStatus = FunnelServiceConfigStatusPending
			status.ServiceEdgeStatus = FunnelServiceEdgeStatusPending
			status.ServiceBackendStatus = FunnelServiceBackendStatusUnknown
			status.ServiceLastError = "当前server-edge尚未支持该协议"
			snapshot.ServiceStatus[service.ID] = status
			continue
		}

		status.ServiceConfigStatus = FunnelServiceConfigStatusActive
		status.ServiceEdgeStatus = FunnelServiceEdgeStatusApplied
		if funnelPortFamilyUsesTLS(portFamily) {
			if status.CertStatus == FunnelCertStatusReady {
				status.DomainStatus = FunnelDomainStatusActive
			} else {
				status.DomainStatus = FunnelDomainStatusPendingCert
			}
		} else {
			status.DomainStatus = FunnelDomainStatusActive
		}
		snapshot.ServiceStatus[service.ID] = status
	}

	return snapshot, cfg, nil
}

func funnelPortFamilyForProto(proto string) (funnelPortFamily, bool) {
	switch proto {
	case FunnelListenProtoHTTP, FunnelListenProtoWS:
		return funnelPortFamilyHTTPPlain, true
	case FunnelListenProtoHTTPS, FunnelListenProtoWSS:
		return funnelPortFamilyHTTPTLS, true
	case FunnelListenProtoTCP:
		return funnelPortFamilyTCPPlain, true
	case FunnelListenProtoTLSTerminatedTCP:
		return funnelPortFamilyTCPTLS, true
	default:
		return "", false
	}
}

func funnelPortFamilyUsesTLS(family funnelPortFamily) bool {
	return family == funnelPortFamilyHTTPTLS || family == funnelPortFamilyTCPTLS
}

func funnelRuntimeRouteKey(family funnelPortFamily, route *funnelRoute) string {
	switch family {
	case funnelPortFamilyHTTPPlain, funnelPortFamilyHTTPTLS:
		return fmt.Sprintf("%s:%d:%s:%s", family, route.ListenPort, route.Domain, route.MountPath)
	case funnelPortFamilyTCPTLS:
		return fmt.Sprintf("%s:%d", family, route.ListenPort)
	case funnelPortFamilyTCPPlain:
		return fmt.Sprintf("%s:%d", family, route.ListenPort)
	default:
		return fmt.Sprintf("%s:%d:%s:%s", family, route.ListenPort, route.Domain, route.MountPath)
	}
}

func (rt *funnelRuntime) syncListeners(cfg FunnelPlatformConfig, snapshot *funnelRuntimeSnapshot) map[funnelListenerKey]string {
	desired := make(map[funnelListenerKey]struct{})
	for port := range snapshot.PlainPorts {
		desired[funnelListenerKey{Port: port}] = struct{}{}
	}
	for port := range snapshot.TLSPorts {
		desired[funnelListenerKey{Port: port, TLS: true}] = struct{}{}
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	errorsByKey := make(map[funnelListenerKey]string)

	for key, state := range rt.listeners {
		if _, ok := desired[key]; ok {
			continue
		}
		rt.closeListenerStateLocked(state)
		delete(rt.listeners, key)
	}

	for key := range desired {
		if _, ok := rt.listeners[key]; ok {
			continue
		}
		state, err := rt.startListenerState(key, cfg)
		if err != nil {
			errorsByKey[key] = err.Error()
			continue
		}
		rt.listeners[key] = state
	}

	return errorsByKey
}

func (rt *funnelRuntime) startListenerState(key funnelListenerKey, cfg FunnelPlatformConfig) (*funnelListenerState, error) {
	handler := rt.httpHandlerForListener(key)
	if !key.TLS && key.Port == 80 {
		handler = rt.certManager.HTTPHandler(handler)
	}

	server := &http.Server{
		Addr:         ":" + strconv.Itoa(key.Port),
		Handler:      handler,
		ReadTimeout:  HTTPReadTimeout,
		WriteTimeout: 0,
	}
	if key.TLS {
		server.TLSConfig = &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: rt.getCertFunc,
		}
	}

	state := &funnelListenerState{
		Key:    key,
		Server: server,
	}

	var bindErrs []string
	for _, bindAddr := range cfg.DirectBindAddrs {
		addr := net.JoinHostPort(bindAddr, strconv.Itoa(key.Port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			bindErrs = append(bindErrs, fmt.Sprintf("%s: %v", addr, err))
			continue
		}
		state.Listeners = append(state.Listeners, ln)
		rt.wg.Add(1)
		go func(listener net.Listener) {
			defer rt.wg.Done()
			var serveErr error
			if key.TLS {
				serveErr = server.ServeTLS(listener, "", "")
			} else {
				serveErr = server.Serve(listener)
			}
			if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
				log.Error().Err(serveErr).Int("port", key.Port).Bool("tls", key.TLS).Msg("funnel listener stopped unexpectedly")
			}
		}(ln)
	}

	if len(state.Listeners) == 0 {
		return nil, fmt.Errorf("Funnel监听端口 %d 绑定失败: %s", key.Port, strings.Join(bindErrs, "; "))
	}

	log.Info().Int("port", key.Port).Bool("tls", key.TLS).Msg("funnel listener started")
	return state, nil
}

func (rt *funnelRuntime) syncTCPListeners(cfg FunnelPlatformConfig, snapshot *funnelRuntimeSnapshot) map[funnelListenerKey]string {
	desired := make(map[funnelListenerKey]struct{})
	for port := range snapshot.TCPPorts {
		desired[funnelListenerKey{Port: port}] = struct{}{}
	}
	for port := range snapshot.TLSTCPPorts {
		desired[funnelListenerKey{Port: port, TLS: true}] = struct{}{}
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	errorsByKey := make(map[funnelListenerKey]string)

	for key, state := range rt.tcpListeners {
		if _, ok := desired[key]; ok {
			continue
		}
		rt.closeTCPListenerStateLocked(state)
		delete(rt.tcpListeners, key)
	}

	for key := range desired {
		if _, ok := rt.tcpListeners[key]; ok {
			continue
		}
		state, err := rt.startTCPListenerState(key, cfg)
		if err != nil {
			errorsByKey[key] = err.Error()
			continue
		}
		rt.tcpListeners[key] = state
	}

	return errorsByKey
}

func (rt *funnelRuntime) startTCPListenerState(key funnelListenerKey, cfg FunnelPlatformConfig) (*funnelTCPListenerState, error) {
	listenerCtx, cancel := context.WithCancel(rt.ctx)
	state := &funnelTCPListenerState{
		Key:    key,
		Cancel: cancel,
	}

	var bindErrs []string
	for _, bindAddr := range cfg.DirectBindAddrs {
		addr := net.JoinHostPort(bindAddr, strconv.Itoa(key.Port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			bindErrs = append(bindErrs, fmt.Sprintf("%s: %v", addr, err))
			continue
		}
		state.Listeners = append(state.Listeners, ln)
		rt.wg.Add(1)
		go rt.serveTCPListener(listenerCtx, key, ln)
	}

	if len(state.Listeners) == 0 {
		cancel()
		return nil, fmt.Errorf("Funnel监听端口 %d 绑定失败: %s", key.Port, strings.Join(bindErrs, "; "))
	}

	log.Info().Int("port", key.Port).Bool("tls", key.TLS).Msg("funnel tcp listener started")
	return state, nil
}

func (rt *funnelRuntime) serveTCPListener(ctx context.Context, key funnelListenerKey, listener net.Listener) {
	defer rt.wg.Done()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			log.Error().Err(err).Int("port", key.Port).Bool("tls", key.TLS).Msg("funnel tcp listener accept failed")
			return
		}
		rt.wg.Add(1)
		go func() {
			defer rt.wg.Done()
			rt.handleTCPConn(key, conn)
		}()
	}
}

func (rt *funnelRuntime) closeListenerStateLocked(state *funnelListenerState) {
	if state == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), HTTPShutdownTimeout)
	defer cancel()
	if err := state.Server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error().Err(err).Int("port", state.Key.Port).Bool("tls", state.Key.TLS).Msg("failed to shutdown funnel listener")
	}
	for _, listener := range state.Listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Error().Err(err).Int("port", state.Key.Port).Bool("tls", state.Key.TLS).Msg("failed to close funnel listener")
		}
	}
}

func (rt *funnelRuntime) closeTCPListenerStateLocked(state *funnelTCPListenerState) {
	if state == nil {
		return
	}
	if state.Cancel != nil {
		state.Cancel()
	}
	for _, listener := range state.Listeners {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Error().Err(err).Int("port", state.Key.Port).Bool("tls", state.Key.TLS).Msg("failed to close funnel tcp listener")
		}
	}
}

func (rt *funnelRuntime) shutdownAll() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	for key, state := range rt.listeners {
		rt.closeListenerStateLocked(state)
		delete(rt.listeners, key)
	}
	for key, state := range rt.tcpListeners {
		rt.closeTCPListenerStateLocked(state)
		delete(rt.tcpListeners, key)
	}
	for orgID, dialer := range rt.orgDialers {
		if err := dialer.Close(); err != nil {
			log.Error().Err(err).Int64("org_id", orgID).Msg("failed to close funnel org dialer")
		}
		delete(rt.orgDialers, orgID)
	}
}

func (rt *funnelRuntime) httpHandlerForListener(key funnelListenerKey) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := rt.matchRoute(key, r)
		if !ok || route == nil {
			http.NotFound(w, r)
			return
		}
		route.Proxy.ServeHTTP(w, r)
	})
}

func (rt *funnelRuntime) matchRoute(key funnelListenerKey, r *http.Request) (*funnelRoute, bool) {
	rt.mu.RLock()
	snapshot := rt.snapshot
	rt.mu.RUnlock()
	if snapshot == nil {
		return nil, false
	}

	host := normalizeFunnelRequestHost(r.Host)
	if host == "" {
		return nil, false
	}
	portRoutes := snapshot.PlainPorts[key.Port]
	if key.TLS {
		portRoutes = snapshot.TLSPorts[key.Port]
	}
	if portRoutes == nil {
		return nil, false
	}
	routes := portRoutes.RoutesByHost[host]
	if len(routes) == 0 {
		return nil, false
	}
	for _, route := range routes {
		if funnelPathMatches(route.MountPath, r.URL.Path) {
			return route, true
		}
	}
	return nil, false
}

func funnelRoutesForListener(snapshot *funnelRuntimeSnapshot, key funnelListenerKey) []*funnelRoute {
	if snapshot == nil {
		return nil
	}

	var routes []*funnelRoute
	if key.TLS {
		if portRoutes := snapshot.TLSPorts[key.Port]; portRoutes != nil {
			for _, hostRoutes := range portRoutes.RoutesByHost {
				routes = append(routes, hostRoutes...)
			}
		}
		if portRoutes := snapshot.TLSTCPPorts[key.Port]; portRoutes != nil && portRoutes.DefaultRoute != nil {
			routes = append(routes, portRoutes.DefaultRoute)
		}
		return routes
	}

	if portRoutes := snapshot.PlainPorts[key.Port]; portRoutes != nil {
		for _, hostRoutes := range portRoutes.RoutesByHost {
			routes = append(routes, hostRoutes...)
		}
	}
	if portRoutes := snapshot.TCPPorts[key.Port]; portRoutes != nil && portRoutes.DefaultRoute != nil {
		routes = append(routes, portRoutes.DefaultRoute)
	}
	return routes
}

func (rt *funnelRuntime) matchTCPRoute(key funnelListenerKey, serverName string) (*funnelRoute, bool) {
	rt.mu.RLock()
	snapshot := rt.snapshot
	rt.mu.RUnlock()
	if snapshot == nil {
		return nil, false
	}

	if key.TLS {
		portRoutes := snapshot.TLSTCPPorts[key.Port]
		if portRoutes == nil || portRoutes.DefaultRoute == nil {
			return nil, false
		}
		host := normalizeFunnelBaseDomain(serverName)
		if host == "" || host != portRoutes.DefaultRoute.Domain {
			return nil, false
		}
		return portRoutes.DefaultRoute, true
	}

	portRoutes := snapshot.TCPPorts[key.Port]
	if portRoutes == nil || portRoutes.DefaultRoute == nil {
		return nil, false
	}
	return portRoutes.DefaultRoute, true
}

func (rt *funnelRuntime) handleTCPConn(key funnelListenerKey, conn net.Conn) {
	defer conn.Close()

	if key.TLS {
		rt.handleTLSTCPConn(key, conn)
		return
	}

	route, ok := rt.matchTCPRoute(key, "")
	if !ok || route == nil {
		return
	}
	if err := rt.proxyTCPConn(conn, route); err != nil {
		rt.appendLog(route.ServiceID, "error", "代理TCP后端失败: "+err.Error())
	}
}

func (rt *funnelRuntime) handleTLSTCPConn(key funnelListenerKey, conn net.Conn) {
	var selectedRoute *funnelRoute
	tlsConn := tls.Server(conn, &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello == nil || strings.TrimSpace(hello.ServerName) == "" {
				log.Warn().Int("port", key.Port).Msg("funnel tls-tcp connection missing server name")
				return nil, fmt.Errorf("missing tls server name")
			}
			route, ok := rt.matchTCPRoute(key, hello.ServerName)
			if !ok || route == nil {
				log.Warn().Int("port", key.Port).Str("server_name", hello.ServerName).Msg("funnel tls-tcp route not found")
				return nil, fmt.Errorf("no tls-terminated-tcp route for host %s", hello.ServerName)
			}
			selectedRoute = route
			return rt.getCertFunc(hello)
		},
	})
	if err := tlsConn.Handshake(); err != nil {
		log.Warn().Err(err).Int("port", key.Port).Msg("funnel tls-tcp handshake failed")
		return
	}
	if selectedRoute == nil {
		return
	}
	if err := rt.proxyTCPConn(tlsConn, selectedRoute); err != nil {
		rt.appendLog(selectedRoute.ServiceID, "error", "代理TLS-TCP后端失败: "+err.Error())
	}
}

func (rt *funnelRuntime) proxyTCPConn(clientConn net.Conn, route *funnelRoute) error {
	ctx, cancel := context.WithTimeout(rt.ctx, 10*time.Second)
	defer cancel()

	dialer, err := rt.dialerForOrg(ctx, route.OrgID)
	if err != nil {
		return err
	}
	backendConn, err := dialer.DialContext(ctx, "tcp", route.BackendAddress)
	if err != nil {
		return err
	}
	defer backendConn.Close()

	errCh := make(chan error, 2)
	go func() {
		_, copyErr := io.Copy(backendConn, clientConn)
		errCh <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(clientConn, backendConn)
		errCh <- copyErr
	}()

	err = <-errCh
	if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (rt *funnelRuntime) newReverseProxy(route *funnelRoute) *httputil.ReverseProxy {
	target := &url.URL{
		Scheme: route.BackendScheme,
		Host:   route.BackendAddress,
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer, err := rt.dialerForOrg(ctx, route.OrgID)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, address)
		},
		ForceAttemptHTTP2: false,
	}
	if route.BackendScheme == "https" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}

	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host
			pr.Out.URL.Path = stripFunnelMountPath(route.MountPath, pr.In.URL.Path)
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			rt.appendLog(route.ServiceID, "error", "代理后端失败: "+err.Error())
			http.Error(w, "funnel backend unavailable", http.StatusBadGateway)
		},
	}
}

func (rt *funnelRuntime) dialerForOrg(ctx context.Context, orgID int64) (funnelOrgDialer, error) {
	rt.mu.RLock()
	dialer := rt.orgDialers[orgID]
	rt.mu.RUnlock()
	if dialer != nil {
		return dialer, nil
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if dialer = rt.orgDialers[orgID]; dialer != nil {
		return dialer, nil
	}
	dialer, err := rt.newOrgDialer(ctx, orgID)
	if err != nil {
		return nil, err
	}
	rt.orgDialers[orgID] = dialer
	return dialer, nil
}

func (rt *funnelRuntime) newTSNetOrgDialer(ctx context.Context, orgID int64) (funnelOrgDialer, error) {
	org, err := rt.app.GetOrgnaizationByID(orgID)
	if err != nil {
		return nil, err
	}
	user, err := rt.ensureOrgEdgeUser(org)
	if err != nil {
		return nil, err
	}
	authKey, err := rt.ensureOrgEdgeAuthKey(user)
	if err != nil {
		return nil, err
	}

	stateDir := AbsolutePathFromConfigPath(filepath.Join(funnelRuntimeStateDir, fmt.Sprintf("org-%d", orgID)))
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}

	server := &tsnet.Server{
		Dir:        stateDir,
		Hostname:   funnelEdgeUserPrefix + org.StableID,
		AuthKey:    authKey,
		ControlURL: normalizeFunnelControlURL(rt.app.cfg.ServerURL),
		Logf:       func(string, ...any) {},
		UserLogf:   func(string, ...any) {},
	}

	upCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := server.Up(upCtx); err != nil {
		_ = server.Close()
		return nil, err
	}

	return &funnelOrgEdge{
		orgID:  orgID,
		server: server,
	}, nil
}

func (rt *funnelRuntime) ensureOrgEdgeUser(org *Organization) (*User, error) {
	name := funnelEdgeUserPrefix + org.StableID
	user := &User{}
	if err := rt.app.db.Where("name = ? AND organization_id = ?", name, org.ID).First(user).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		return rt.app.CreateUser(name, "Funnel Edge", org.Name, org.Provider)
	}
	return user, nil
}

func (rt *funnelRuntime) ensureOrgEdgeAuthKey(user *User) (string, error) {
	keys, err := rt.app.ListPreAuthKeys(user.ID)
	if err != nil {
		return "", err
	}
	now := time.Now()
	for _, candidate := range keys {
		if !candidate.Reusable {
			continue
		}
		if candidate.Expiration != nil && candidate.Expiration.Before(now) {
			continue
		}
		return candidate.Key, nil
	}
	expiration := time.Now().Add(365 * 24 * time.Hour)
	key, err := rt.app.CreatePreAuthKey(user, true, false, &expiration, nil)
	if err != nil {
		return "", err
	}
	return key.Key, nil
}

func normalizeFunnelControlURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	host := raw
	if strings.Contains(raw, ":") {
		if splitHost, _, err := net.SplitHostPort(raw); err == nil {
			host = splitHost
		}
	}
	if host == "localhost" || host == "127.0.0.1" || net.ParseIP(host) != nil {
		return "http://" + raw
	}
	return "https://" + raw
}

func (rt *funnelRuntime) autocertHostPolicy(_ context.Context, host string) error {
	host = normalizeFunnelBaseDomain(host)
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rt.snapshot == nil {
		return fmt.Errorf("funnel runtime snapshot not ready")
	}
	if _, ok := rt.snapshot.TLSHosts[host]; !ok {
		return fmt.Errorf("host %s is not allowed for funnel tls", host)
	}
	return nil
}

func (rt *funnelRuntime) getFunnelCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, err := rt.certManager.GetCertificate(hello)
	if err != nil {
		return nil, err
	}
	if cert != nil && hello != nil && hello.ServerName != "" {
		rt.noteIssuedCertificate(hello.ServerName, cert)
	}
	return cert, nil
}

func (rt *funnelRuntime) noteIssuedCertificate(host string, cert *tls.Certificate) {
	if cert == nil {
		return
	}
	host = normalizeFunnelBaseDomain(host)
	go func() {
		if err := rt.persistIssuedCertificate(host, cert); err != nil {
			log.Error().Err(err).Str("host", host).Msg("failed to persist funnel certificate metadata")
		}
	}()
}

func (rt *funnelRuntime) persistIssuedCertificate(host string, cert *tls.Certificate) error {
	domain := &FunnelDomain{}
	if err := rt.app.db.Where("domain = ?", host).First(domain).Error; err != nil {
		return err
	}
	funnelCert := &FunnelCert{}
	if err := rt.app.db.Where("domain_id = ?", domain.ID).First(funnelCert).Error; err != nil {
		return err
	}
	if len(cert.Certificate) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return err
	}
	renewAfter := leaf.NotAfter.Add(-30 * 24 * time.Hour)
	funnelCert.Issuer = leaf.Issuer.String()
	funnelCert.CertStatus = FunnelCertStatusReady
	funnelCert.NotBefore = &leaf.NotBefore
	funnelCert.NotAfter = &leaf.NotAfter
	funnelCert.RenewAfter = &renewAfter
	funnelCert.LastError = ""
	if err := rt.app.db.Save(funnelCert).Error; err != nil {
		return err
	}
	domain.Status = FunnelDomainStatusActive
	domain.DNSStatus = FunnelDNSStatusReady
	domain.LastError = ""
	if err := rt.app.db.Save(domain).Error; err != nil {
		return err
	}
	rt.requestReload("cert-ready")
	return nil
}

func (rt *funnelRuntime) applyStatusOverlay(service *FunnelService, domain *FunnelDomain, cert *FunnelCert) {
	if service == nil {
		return
	}
	rt.mu.RLock()
	snapshot := rt.snapshot
	rt.mu.RUnlock()
	if snapshot == nil {
		return
	}
	status, ok := snapshot.ServiceStatus[service.ID]
	if !ok {
		return
	}
	service.TargetMachineOnline = status.TargetMachineOnline
	service.ConfigStatus = status.ServiceConfigStatus
	service.EdgeStatus = status.ServiceEdgeStatus
	service.BackendStatus = status.ServiceBackendStatus
	service.LastError = status.ServiceLastError
	if domain != nil {
		domain.Status = status.DomainStatus
		domain.DNSStatus = status.DomainDNSStatus
		domain.LastError = status.DomainLastError
	}
	if cert != nil {
		cert.CertStatus = status.CertStatus
		cert.Issuer = status.CertIssuer
		cert.NotBefore = status.CertNotBefore
		cert.NotAfter = status.CertNotAfter
		cert.RenewAfter = status.CertRenewAfter
		cert.LastError = status.CertLastError
	}
}

func (rt *funnelRuntime) logsForService(serviceID int64) []any {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	logs := rt.serviceLogs[serviceID]
	if len(logs) == 0 {
		return []any{}
	}
	result := make([]any, 0, len(logs))
	for _, entry := range logs {
		result = append(result, map[string]any{
			"time":    entry.At,
			"level":   entry.Level,
			"message": entry.Message,
		})
	}
	return result
}

func (rt *funnelRuntime) appendLog(serviceID int64, level, message string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	entries := append(rt.serviceLogs[serviceID], funnelLogEntry{
		At:      time.Now().UTC(),
		Level:   level,
		Message: message,
	})
	if len(entries) > funnelServiceLogLimit {
		entries = entries[len(entries)-funnelServiceLogLimit:]
	}
	rt.serviceLogs[serviceID] = entries
}

func (rt *funnelRuntime) updateServerEdgeStatus(cfg FunnelPlatformConfig, healthy bool) error {
	desired := buildDefaultServerEdge(cfg)
	now := funnelStatusStamp()
	desired.LastSeen = &now
	if healthy {
		desired.HealthStatus = FunnelEdgeHealthHealthy
	} else {
		desired.HealthStatus = FunnelEdgeHealthUnhealthy
	}
	_, err := upsertFunnelEdge(rt.app.db, &desired)
	return err
}

func markFunnelDomainVerified(tx *gorm.DB, domain *FunnelDomain) error {
	if domain == nil {
		return fmt.Errorf("未找到Funnel域名")
	}
	now := funnelStatusStamp()
	domain.ValidationCheckedAt = &now
	domain.DNSStatus = FunnelDNSStatusReady
	domain.LastDNSError = ""
	domain.LastError = ""
	if strings.EqualFold(domain.TLSMode, FunnelTLSModeBringYourOwn) {
		domain.Status = FunnelDomainStatusActive
	} else {
		domain.Status = FunnelDomainStatusPendingCert
	}
	return tx.Save(domain).Error
}

func normalizeFunnelRequestHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return normalizeFunnelBaseDomain(host)
}

func funnelPathMatches(mountPath, requestPath string) bool {
	if mountPath == "/" {
		return true
	}
	if !strings.HasPrefix(requestPath, mountPath) {
		return false
	}
	if len(requestPath) == len(mountPath) {
		return true
	}
	return requestPath[len(mountPath)] == '/'
}

func stripFunnelMountPath(mountPath, requestPath string) string {
	if mountPath == "/" {
		if requestPath == "" {
			return "/"
		}
		return requestPath
	}
	stripped := strings.TrimPrefix(requestPath, mountPath)
	if stripped == "" {
		return "/"
	}
	if !strings.HasPrefix(stripped, "/") {
		return "/" + stripped
	}
	return stripped
}
