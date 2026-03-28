package controller

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestFunnelRemoteEdgeWorkerSyncOnceMarksHealthyAndServesHTTP(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	sysCfg.FunnelCfg.DefaultEdgeMode = FunnelEdgeModeRemote
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}

	now := time.Now().UTC()
	edge := &FunnelEdge{
		EdgeType:     FunnelEdgeTypeNavi,
		EdgeNodeID:   "navi-healthy",
		Hostname:     "navi-healthy.example.test",
		SyncEndpoint: "/cockpit/api/funnel/edges/navi-healthy/sync",
		ListenerCapabilities: FunnelListenerCapabilities{
			SupportsHTTP: true,
			SupportsWS:   true,
		},
		HealthStatus: FunnelEdgeHealthUnknown,
		Allocatable:  true,
	}
	if err := app.db.Create(edge).Error; err != nil {
		t.Fatalf("Create(edge): %v", err)
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf("path=%s host=%s", r.URL.Path, r.Host))
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("url.Parse(backend): %v", err)
	}
	backendPort, err := strconv.Atoi(backendURL.Port())
	if err != nil {
		t.Fatalf("Atoi(backend port): %v", err)
	}

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	machine.LastSeen = &now
	if err := app.db.Save(machine).Error; err != nil {
		t.Fatalf("Save(machine): %v", err)
	}

	listenPort := freeFunnelTestPort(t)
	service, domain, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:      machine.ID,
		DomainMode:     "managed",
		ListenProto:    FunnelListenProtoHTTP,
		ListenPort:     listenPort,
		MountPath:      "/app",
		BackendType:    FunnelBackendTypeHTTPProxy,
		BackendScheme:  "http",
		BackendPort:    backendPort,
		BackendTailnet: backendURL.Hostname(),
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}

	worker := newFunnelRemoteEdgeWorker(app.db, edge.ID, []string{"127.0.0.1"})
	defer worker.close()

	if err := worker.syncOnce(); err != nil {
		t.Fatalf("worker.syncOnce(): %v", err)
	}

	storedEdge := &FunnelEdge{}
	if err := app.db.First(storedEdge, edge.ID).Error; err != nil {
		t.Fatalf("First(edge): %v", err)
	}
	if storedEdge.HealthStatus != FunnelEdgeHealthHealthy {
		t.Fatalf("edge.HealthStatus = %q, want %q", storedEdge.HealthStatus, FunnelEdgeHealthHealthy)
	}
	if storedEdge.LastSeen == nil {
		t.Fatal("expected edge.LastSeen to be set")
	}

	client := &http.Client{Timeout: 2 * time.Second}
	var (
		respBody string
		lastErr  error
	)
	for attempt := 0; attempt < 30; attempt++ {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/app/hello", listenPort), nil)
		if err != nil {
			t.Fatalf("http.NewRequest(): %v", err)
		}
		req.Host = domain.Domain
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("ReadAll(response): %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
			time.Sleep(100 * time.Millisecond)
			continue
		}
		respBody = string(body)
		lastErr = nil
		break
	}
	if lastErr != nil {
		t.Fatalf("request through remote worker runtime failed: %v", lastErr)
	}
	if want := "path=/hello host=" + domain.Domain; respBody != want {
		t.Fatalf("runtime response = %q, want %q", respBody, want)
	}

	status, err := app.buildTenantFunnelServiceStatus(service)
	if err != nil {
		t.Fatalf("buildTenantFunnelServiceStatus(): %v", err)
	}
	serviceMap := status["service"].(map[string]any)
	if got := serviceMap["configStatus"]; got != FunnelServiceConfigStatusActive {
		t.Fatalf("configStatus = %#v, want %q", got, FunnelServiceConfigStatusActive)
	}
	if got := serviceMap["edgeStatus"]; got != FunnelServiceEdgeStatusApplied {
		t.Fatalf("edgeStatus = %#v, want %q", got, FunnelServiceEdgeStatusApplied)
	}
}

func TestFunnelRemoteEdgeWorkerSyncOnceMarksUnhealthyOnBindFailure(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	sysCfg.FunnelCfg.DefaultEdgeMode = FunnelEdgeModeRemote
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}

	now := time.Now().UTC()
	edge := &FunnelEdge{
		EdgeType:     FunnelEdgeTypeNavi,
		EdgeNodeID:   "navi-unhealthy",
		Hostname:     "navi-unhealthy.example.test",
		SyncEndpoint: "/cockpit/api/funnel/edges/navi-unhealthy/sync",
		ListenerCapabilities: FunnelListenerCapabilities{
			SupportsHTTP: true,
			SupportsWS:   true,
		},
		HealthStatus: FunnelEdgeHealthUnknown,
		Allocatable:  true,
	}
	if err := app.db.Create(edge).Error; err != nil {
		t.Fatalf("Create(edge): %v", err)
	}

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	machine.LastSeen = &now
	if err := app.db.Save(machine).Error; err != nil {
		t.Fatalf("Save(machine): %v", err)
	}

	listenPort := freeFunnelTestPort(t)
	service, _, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:      machine.ID,
		DomainMode:     "managed",
		ListenProto:    FunnelListenProtoHTTP,
		ListenPort:     listenPort,
		MountPath:      "/",
		BackendType:    FunnelBackendTypeHTTPProxy,
		BackendScheme:  "http",
		BackendPort:    8080,
		BackendTailnet: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}

	blocker, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)))
	if err != nil {
		t.Fatalf("Listen(blocker): %v", err)
	}
	defer blocker.Close()

	worker := newFunnelRemoteEdgeWorker(app.db, edge.ID, []string{"127.0.0.1"})
	defer worker.close()

	if err := worker.syncOnce(); err == nil {
		t.Fatal("expected worker.syncOnce() to fail on bind conflict")
	}

	storedEdge := &FunnelEdge{}
	if err := app.db.First(storedEdge, edge.ID).Error; err != nil {
		t.Fatalf("First(edge): %v", err)
	}
	if storedEdge.HealthStatus != FunnelEdgeHealthUnhealthy {
		t.Fatalf("edge.HealthStatus = %q, want %q", storedEdge.HealthStatus, FunnelEdgeHealthUnhealthy)
	}
	if storedEdge.LastSeen == nil {
		t.Fatal("expected edge.LastSeen to be set")
	}

	status, err := app.buildTenantFunnelServiceStatus(service)
	if err != nil {
		t.Fatalf("buildTenantFunnelServiceStatus(): %v", err)
	}
	serviceMap := status["service"].(map[string]any)
	if got := serviceMap["configStatus"]; got != FunnelServiceConfigStatusPending {
		t.Fatalf("configStatus = %#v, want %q", got, FunnelServiceConfigStatusPending)
	}
	if got := serviceMap["edgeStatus"]; got != FunnelServiceEdgeStatusUnavailable {
		t.Fatalf("edgeStatus = %#v, want %q", got, FunnelServiceEdgeStatusUnavailable)
	}
	if got := serviceMap["lastError"]; got != "当前remote-edge不健康" {
		t.Fatalf("lastError = %#v", got)
	}
}
