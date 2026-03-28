package controller

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type funnelRemoteRuntimeSnapshot struct {
	PlainPorts map[int]*funnelPortRoutes
	TLSPorts   map[int]*funnelPortRoutes
	TLSHosts   map[string]struct{}
}

type funnelRemoteRuntime struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.RWMutex
	closed    bool
	snapshot  *funnelRemoteRuntimeSnapshot
	listeners map[funnelListenerKey]*funnelListenerState

	bindAddrs   []string
	dialContext func(ctx context.Context, network, address string) (net.Conn, error)
	getCertFunc func(*tls.ClientHelloInfo) (*tls.Certificate, error)

	wg sync.WaitGroup
}

func newFunnelRemoteRuntime(parent context.Context, bindAddrs []string) *funnelRemoteRuntime {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	addrs := normalizeStringList(bindAddrs)
	if len(addrs) == 0 {
		addrs = []string{"0.0.0.0", "::"}
	}
	return &funnelRemoteRuntime{
		ctx:       ctx,
		cancel:    cancel,
		listeners: make(map[funnelListenerKey]*funnelListenerState),
		bindAddrs: addrs,
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	}
}

func (rt *funnelRemoteRuntime) close() {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return
	}
	rt.closed = true
	rt.mu.Unlock()

	rt.cancel()
	rt.shutdownAll()
	rt.wg.Wait()
}

func (rt *funnelRemoteRuntime) applyPayload(payload *FunnelEdgeSyncPayload) error {
	rt.mu.RLock()
	closed := rt.closed
	rt.mu.RUnlock()
	if closed {
		return fmt.Errorf("remote funnel runtime is closed")
	}

	snapshot, err := rt.buildSnapshot(payload)
	if err != nil {
		return err
	}

	listenerErrors := rt.syncListeners(snapshot)

	rt.mu.Lock()
	rt.snapshot = snapshot
	rt.mu.Unlock()

	if len(listenerErrors) == 0 {
		return nil
	}

	keys := make([]funnelListenerKey, 0, len(listenerErrors))
	for key := range listenerErrors {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Port != keys[j].Port {
			return keys[i].Port < keys[j].Port
		}
		if keys[i].TLS == keys[j].TLS {
			return false
		}
		return !keys[i].TLS && keys[j].TLS
	})

	messages := make([]string, 0, len(keys))
	for _, key := range keys {
		messages = append(messages, listenerErrors[key])
	}
	return fmt.Errorf("failed to apply funnel remote payload: %s", strings.Join(messages, "; "))
}

func (rt *funnelRemoteRuntime) buildSnapshot(payload *FunnelEdgeSyncPayload) (*funnelRemoteRuntimeSnapshot, error) {
	snapshot := &funnelRemoteRuntimeSnapshot{
		PlainPorts: make(map[int]*funnelPortRoutes),
		TLSPorts:   make(map[int]*funnelPortRoutes),
		TLSHosts:   make(map[string]struct{}),
	}
	if payload == nil || len(payload.Services) == 0 {
		return snapshot, nil
	}

	seenRouteKeys := make(map[string]int64)
	portFamilies := make(map[int]funnelPortFamily)

	for i := range payload.Services {
		item := payload.Services[i]
		listenProto := strings.ToLower(strings.TrimSpace(item.Public.ListenProto))
		if !funnelHTTPFamilyProto(listenProto) {
			return nil, fmt.Errorf("remote funnel runtime does not support protocol %q", item.Public.ListenProto)
		}
		if item.Backend.Network != "" && !strings.EqualFold(strings.TrimSpace(item.Backend.Network), "tcp") {
			return nil, fmt.Errorf("remote funnel runtime does not support backend network %q", item.Backend.Network)
		}

		portFamily, ok := funnelPortFamilyForProto(listenProto)
		if !ok {
			return nil, fmt.Errorf("unsupported funnel listen proto %q", item.Public.ListenProto)
		}
		if existingFamily, ok := portFamilies[item.Public.ListenPort]; ok && existingFamily != portFamily {
			return nil, fmt.Errorf("listen port %d is already assigned to %s family", item.Public.ListenPort, existingFamily)
		}
		portFamilies[item.Public.ListenPort] = portFamily

		host := normalizeFunnelBaseDomain(item.Public.Host)
		if host == "" {
			return nil, fmt.Errorf("remote funnel host is required")
		}
		if item.Public.ListenPort <= 0 || item.Public.ListenPort > 65535 {
			return nil, fmt.Errorf("invalid remote funnel listen port %d", item.Public.ListenPort)
		}
		if strings.TrimSpace(item.Backend.Address) == "" || item.Backend.Port <= 0 || item.Backend.Port > 65535 {
			return nil, fmt.Errorf("remote funnel backend address is incomplete")
		}

		backendScheme := strings.ToLower(strings.TrimSpace(item.Backend.Scheme))
		if backendScheme == "" {
			backendScheme = "http"
		}

		route := &funnelRoute{
			ServiceID:      funnelRemoteSyncServiceID(item, i),
			OrgID:          item.OrgID,
			Domain:         host,
			ListenPort:     item.Public.ListenPort,
			ListenProto:    listenProto,
			MountPath:      normalizeFunnelMountPath(item.Public.MountPath),
			BackendType:    FunnelBackendTypeHTTPProxy,
			BackendScheme:  backendScheme,
			BackendAddress: net.JoinHostPort(strings.TrimSpace(item.Backend.Address), strconv.Itoa(item.Backend.Port)),
		}

		routeKey := funnelRuntimeRouteKey(portFamily, route)
		if existingID, ok := seenRouteKeys[routeKey]; ok {
			return nil, fmt.Errorf("duplicate remote funnel route on port %d host %s path %s conflicts with service %d", route.ListenPort, route.Domain, route.MountPath, existingID)
		}
		seenRouteKeys[routeKey] = route.ServiceID
		route.Proxy = rt.newReverseProxy(route)

		if funnelPortFamilyUsesTLS(portFamily) {
			snapshot.TLSHosts[host] = struct{}{}
			portRoutes := snapshot.TLSPorts[route.ListenPort]
			if portRoutes == nil {
				portRoutes = &funnelPortRoutes{RoutesByHost: make(map[string][]*funnelRoute)}
				snapshot.TLSPorts[route.ListenPort] = portRoutes
			}
			hostRoutes := append(portRoutes.RoutesByHost[route.Domain], route)
			sort.SliceStable(hostRoutes, func(i, j int) bool {
				return len(hostRoutes[i].MountPath) > len(hostRoutes[j].MountPath)
			})
			portRoutes.RoutesByHost[route.Domain] = hostRoutes
			continue
		}

		portRoutes := snapshot.PlainPorts[route.ListenPort]
		if portRoutes == nil {
			portRoutes = &funnelPortRoutes{RoutesByHost: make(map[string][]*funnelRoute)}
			snapshot.PlainPorts[route.ListenPort] = portRoutes
		}
		hostRoutes := append(portRoutes.RoutesByHost[route.Domain], route)
		sort.SliceStable(hostRoutes, func(i, j int) bool {
			return len(hostRoutes[i].MountPath) > len(hostRoutes[j].MountPath)
		})
		portRoutes.RoutesByHost[route.Domain] = hostRoutes
	}

	return snapshot, nil
}

func (rt *funnelRemoteRuntime) syncListeners(snapshot *funnelRemoteRuntimeSnapshot) map[funnelListenerKey]string {
	desired := make(map[funnelListenerKey]struct{})
	for port := range snapshot.PlainPorts {
		desired[funnelListenerKey{Port: port}] = struct{}{}
	}
	for port := range snapshot.TLSPorts {
		desired[funnelListenerKey{Port: port, TLS: true}] = struct{}{}
	}

	errorsByKey := make(map[funnelListenerKey]string)
	toClose := make([]*funnelListenerState, 0)

	rt.mu.Lock()
	for key, state := range rt.listeners {
		if _, ok := desired[key]; ok {
			continue
		}
		toClose = append(toClose, state)
		delete(rt.listeners, key)
	}
	rt.mu.Unlock()

	for _, state := range toClose {
		rt.closeListenerState(state)
	}

	for key := range desired {
		rt.mu.RLock()
		_, ok := rt.listeners[key]
		rt.mu.RUnlock()
		if ok {
			continue
		}
		state, err := rt.startListenerState(key)
		if err != nil {
			errorsByKey[key] = err.Error()
			continue
		}
		rt.mu.Lock()
		if rt.closed {
			rt.mu.Unlock()
			rt.closeListenerState(state)
			errorsByKey[key] = fmt.Sprintf("remote funnel listen port %d stopped because runtime is closed", key.Port)
			continue
		}
		if _, ok := rt.listeners[key]; ok {
			rt.mu.Unlock()
			rt.closeListenerState(state)
			continue
		}
		rt.listeners[key] = state
		rt.mu.Unlock()
	}

	return errorsByKey
}

func (rt *funnelRemoteRuntime) startListenerState(key funnelListenerKey) (*funnelListenerState, error) {
	handler := rt.httpHandlerForListener(key)
	server := &http.Server{
		Addr:         ":" + strconv.Itoa(key.Port),
		Handler:      handler,
		ReadTimeout:  HTTPReadTimeout,
		WriteTimeout: 0,
	}
	if key.TLS {
		if rt.getCertFunc == nil {
			return nil, fmt.Errorf("remote funnel TLS certificate callback is not configured")
		}
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
	for _, bindAddr := range rt.bindAddrs {
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
				// Remote edge runtime has no persisted log sink yet; keep the listener alive until caller reloads.
			}
		}(ln)
	}

	if len(state.Listeners) == 0 {
		return nil, fmt.Errorf("remote funnel listen port %d bind failed: %s", key.Port, strings.Join(bindErrs, "; "))
	}

	return state, nil
}

func (rt *funnelRemoteRuntime) shutdownAll() {
	rt.mu.Lock()
	toClose := make([]*funnelListenerState, 0, len(rt.listeners))
	for key, state := range rt.listeners {
		toClose = append(toClose, state)
		delete(rt.listeners, key)
	}
	rt.mu.Unlock()

	for _, state := range toClose {
		rt.closeListenerState(state)
	}
}

func (rt *funnelRemoteRuntime) closeListenerState(state *funnelListenerState) {
	if state == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), HTTPShutdownTimeout)
	defer cancel()
	_ = state.Server.Shutdown(ctx)
	for _, listener := range state.Listeners {
		_ = listener.Close()
	}
}

func (rt *funnelRemoteRuntime) httpHandlerForListener(key funnelListenerKey) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := rt.matchRoute(key, r)
		if !ok || route == nil {
			http.NotFound(w, r)
			return
		}
		route.Proxy.ServeHTTP(w, r)
	})
}

func (rt *funnelRemoteRuntime) matchRoute(key funnelListenerKey, r *http.Request) (*funnelRoute, bool) {
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

func (rt *funnelRemoteRuntime) newReverseProxy(route *funnelRoute) *httputil.ReverseProxy {
	target := &url.URL{
		Scheme: route.BackendScheme,
		Host:   route.BackendAddress,
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return rt.dialContext(ctx, network, address)
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
			http.Error(w, "funnel backend unavailable", http.StatusBadGateway)
		},
	}
}

func funnelRemoteSyncServiceID(item FunnelEdgeSyncService, index int) int64 {
	if item.Service != nil {
		if rawID, ok := item.Service["id"]; ok {
			switch value := rawID.(type) {
			case string:
				if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
					return parsed
				}
			case float64:
				if value > 0 {
					return int64(value)
				}
			case int64:
				if value > 0 {
					return value
				}
			case int:
				if value > 0 {
					return int64(value)
				}
			}
		}
	}
	return int64(index + 1)
}
