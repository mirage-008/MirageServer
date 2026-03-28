package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func createTestNaviFunnelEdge(t *testing.T, app *Mirage, edgeNodeID string, naviKey key.MachinePublic) *FunnelEdge {
	t.Helper()

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	region := &NaviRegion{
		OrgID:      owner.OrganizationID,
		RegionCode: "test",
		RegionName: "Test Region",
	}
	if err := app.db.Create(region).Error; err != nil {
		t.Fatalf("Create(region): %v", err)
	}
	node := &NaviNode{
		ID:           edgeNodeID,
		NaviKey:      MachinePublicKeyStripPrefix(naviKey),
		NaviRegionID: region.ID,
		HostName:     edgeNodeID + ".example.test",
		IPv4:         "127.0.0.1",
		DERPPort:     443,
	}
	if err := app.db.Create(node).Error; err != nil {
		t.Fatalf("Create(node): %v", err)
	}
	edge := &FunnelEdge{
		StableID:     edgeNodeID,
		EdgeType:     FunnelEdgeTypeNavi,
		EdgeNodeID:   edgeNodeID,
		Hostname:     node.HostName,
		SyncEndpoint: defaultFunnelSyncEndpoint(FunnelEdgeTypeNavi),
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
	return edge
}

func TestNoiseNaviPullFunnelHandlerReturnsPayload(t *testing.T) {
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

	machineKey := key.NewMachine().Public()
	edge := createTestNaviFunnelEdge(t, app, "navi-sync-1", machineKey)

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	service, domain, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:      machine.ID,
		DomainMode:     "managed",
		ListenProto:    FunnelListenProtoHTTP,
		ListenPort:     80,
		MountPath:      "/",
		BackendType:    FunnelBackendTypeHTTPProxy,
		BackendScheme:  "http",
		BackendPort:    8080,
		BackendTailnet: "100.64.0.20",
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}

	reqBody, err := json.Marshal(tailcfg.MapRequest{
		Hostinfo: &tailcfg.Hostinfo{BackendLogID: edge.EdgeNodeID},
	})
	if err != nil {
		t.Fatalf("json.Marshal(request): %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/navi/funnel", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	ns := &noiseServer{
		mirage:     app,
		machineKey: machineKey,
	}
	ns.NoiseNaviPullFunnelHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload FunnelEdgeSyncPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload): %v", err)
	}
	if payload.ServiceCount != 1 {
		t.Fatalf("serviceCount = %d, want 1", payload.ServiceCount)
	}
	if got := payload.Edge["edgeNodeId"]; got != edge.EdgeNodeID {
		t.Fatalf("edge.edgeNodeId = %#v, want %q", got, edge.EdgeNodeID)
	}
	if len(payload.Services) != 1 {
		t.Fatalf("len(services) = %d, want 1", len(payload.Services))
	}
	if payload.Services[0].Public.Host != domain.Domain {
		t.Fatalf("public.host = %q, want %q", payload.Services[0].Public.Host, domain.Domain)
	}
	if payload.Services[0].Service["id"] != funnelServiceToMap(service)["id"] {
		t.Fatalf("service.id = %#v, want %#v", payload.Services[0].Service["id"], funnelServiceToMap(service)["id"])
	}

	storedEdge := &FunnelEdge{}
	if err := app.db.First(storedEdge, edge.ID).Error; err != nil {
		t.Fatalf("First(edge): %v", err)
	}
	if storedEdge.LastSeen == nil {
		t.Fatal("expected edge.LastSeen to be updated")
	}
	if storedEdge.HealthStatus != FunnelEdgeHealthUnknown {
		t.Fatalf("edge.HealthStatus = %q, want %q", storedEdge.HealthStatus, FunnelEdgeHealthUnknown)
	}
}

func TestNoiseNaviPullFunnelHandlerRejectsKeyMismatch(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	edge := createTestNaviFunnelEdge(t, app, "navi-sync-2", key.NewMachine().Public())

	reqBody, err := json.Marshal(tailcfg.MapRequest{
		Hostinfo: &tailcfg.Hostinfo{BackendLogID: edge.EdgeNodeID},
	})
	if err != nil {
		t.Fatalf("json.Marshal(request): %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/navi/funnel", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	ns := &noiseServer{
		mirage:     app,
		machineKey: key.NewMachine().Public(),
	}
	ns.NoiseNaviPullFunnelHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestBuildNaviFunnelEdgeSyncPayloadRequiresNaviEdge(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)

	region := &NaviRegion{OrgID: 1, RegionCode: "test", RegionName: "Test Region"}
	if err := app.db.Create(region).Error; err != nil {
		t.Fatalf("Create(region): %v", err)
	}
	node := &NaviNode{
		ID:           "navi-sync-3",
		NaviKey:      MachinePublicKeyStripPrefix(key.NewMachine().Public()),
		NaviRegionID: region.ID,
		HostName:     "navi-sync-3.example.test",
	}
	if err := app.db.Create(node).Error; err != nil {
		t.Fatalf("Create(node): %v", err)
	}
	edge := &FunnelEdge{
		StableID:   "server-as-node",
		EdgeType:   FunnelEdgeTypeServer,
		EdgeNodeID: node.ID,
	}
	if err := app.db.Create(edge).Error; err != nil {
		t.Fatalf("Create(edge): %v", err)
	}

	if _, err := buildNaviFunnelEdgeSyncPayload(app.db, node); err == nil {
		t.Fatal("expected buildNaviFunnelEdgeSyncPayload to reject non-navi edge")
	}
}
