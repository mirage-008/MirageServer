package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"github.com/patrickmn/go-cache"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

type fakeManagedFunnelDNSProvider struct {
	ensured   []string
	deleted   []string
	ensureErr error
	deleteErr error
	lookupErr error
	lookup    managedFunnelDNSLookupResult
}

func (f *fakeManagedFunnelDNSProvider) EnsureManagedDomain(_ context.Context, fqdn string) error {
	if f.ensureErr != nil {
		return f.ensureErr
	}
	f.ensured = append(f.ensured, fqdn)
	return nil
}

func (f *fakeManagedFunnelDNSProvider) DeleteManagedDomain(_ context.Context, fqdn string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, fqdn)
	return nil
}

func (f *fakeManagedFunnelDNSProvider) LookupManagedDomain(_ context.Context, fqdn string) (managedFunnelDNSLookupResult, error) {
	if f.lookupErr != nil {
		return managedFunnelDNSLookupResult{}, f.lookupErr
	}
	return f.lookup, nil
}

func newFunnelTenantTestMirage(t *testing.T) *Mirage {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "tenant-funnel-test.sqlite")
	db, err := gorm.Open(
		sqlite.Open(dbPath+"?_journal_mode=WAL&_busy_timeout=5000"),
		&gorm.Config{
			DisableForeignKeyConstraintWhenMigrating: true,
			Logger:                                   logger.Default.LogMode(logger.Silent),
		},
	)
	if err != nil {
		t.Fatalf("gorm.Open(): %v", err)
	}
	initSmokeSchema(t, db)
	if err := migrateFunnelTables(db); err != nil {
		t.Fatalf("migrateFunnelTables(): %v", err)
	}

	serverKey := key.NewMachine()
	serverKeyText, err := serverKey.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText(serverKey): %v", err)
	}

	sysCfg := &SysConfig{
		ServerURL:  "ctrl.example.test",
		ServerKey:  string(serverKeyText),
		Addr:       "127.0.0.1:8080",
		Mip4:       mustIPPrefix(t, "100.64.0.0/10"),
		Mip6:       mustIPPrefix(t, "fd7a:115c:a1e0::/48"),
		Basedomain: "mira.test",
		DerpUrl:    defaultRemoteDERPMapURL,
		FunnelCfg: FunnelPlatformConfig{
			ManagedBaseDomain:   "funnel.example.test",
			DefaultEdgeMode:     FunnelEdgeModeServer,
			DefaultListenerMode: FunnelListenerModeDirect,
			DirectBindAddrs:     StringList{"0.0.0.0", "::"},
			DirectBindPorts:     FunnelPortList{80, 443},
		},
	}
	if err := db.Create(sysCfg).Error; err != nil {
		t.Fatalf("Create(sysCfg): %v", err)
	}

	cfg, err := sysCfg.toSrvConfig()
	if err != nil {
		t.Fatalf("toSrvConfig(): %v", err)
	}
	app, err := NewMirage(cfg, db)
	if err != nil {
		t.Fatalf("NewMirage(): %v", err)
	}
	app.aCodeCache = cache.New(0, 0)
	app.stateCodeCache = cache.New(0, 0)
	app.controlCodeCache = cache.New(0, 0)
	app.machineControlCodeCache = cache.New(0, 0)
	app.tcdCache = cache.New(0, 0)

	owner := createTestUser(t, app, "owner@example.com", "Owner", "tenant-org", "Mirage")
	member := createTestUser(t, app, "member@example.com", "Member", "tenant-org", "Mirage")
	member.Role = RoleMember
	if err := app.db.Save(member).Error; err != nil {
		t.Fatalf("Save(member): %v", err)
	}
	machine := createTestMachine(t, app, owner, "tenant-machine", "100.64.0.20")
	createTestRoute(t, app, machine, "10.10.0.0/24", true, true)

	return app
}

func setTenantTestFunnelDNSMgrConfig(t *testing.T, app *Mirage) {
	t.Helper()

	sysCfg := &SysConfig{}
	if err := app.db.Order("id ASC").First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	sysCfg.FunnelCfg.ManagedDNSProvider = FunnelManagedDNSProviderDNSMgr
	sysCfg.FunnelCfg.ManagedBaseDomain = defaultFunnelDNSMgrBaseDomain
	sysCfg.FunnelCfg.ManagedDNSAPIBaseURL = "https://dnsmgr.example.test"
	sysCfg.FunnelCfg.ManagedDNSUID = 1000
	sysCfg.FunnelCfg.ManagedDNSAPIKey = "secret"
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}
}

func funnelTenantRequest(method, target string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "miragecontrol", Value: "tenant-test-auth"})
	return req
}

func serveTenantFunnel(t *testing.T, app *Mirage, method, target, token string, body []byte, vars map[string]string, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "miragecontrol", Value: token})
	if len(vars) > 0 {
		req = mux.SetURLVars(req, vars)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func funnelTenantResponse(t *testing.T, body []byte) (string, map[string]any) {
	t.Helper()
	var res struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("json.Unmarshal(response): %v", err)
	}
	if res.Status != "success" {
		return res.Status, nil
	}
	if len(res.Data) == 0 {
		return res.Status, nil
	}
	var data map[string]any
	if err := json.Unmarshal(res.Data, &data); err != nil {
		t.Fatalf("json.Unmarshal(data): %v", err)
	}
	return res.Status, data
}

func TestConsoleFunnelAuthzAndCreate(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)

	owner := createTestUser(t, app, "owner2@example.com", "Owner 2", "tenant-org-2", "Mirage")
	app.controlCodeCache.Set("tenant-owner-auth", ControlCacheItem{uid: tailcfg.UserID(owner.ID)}, time.Hour)
	rec := serveTenantFunnel(t, app, http.MethodGet, "/admin/api/funnel/domains", "tenant-owner-auth", nil, nil, app.CAPIGetFunnelDomains)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", rec.Code)
	}

	nonOwner := createTestUser(t, app, "member2@example.com", "Member 2", "tenant-org-2", "Mirage")
	nonOwner.Role = RoleMember
	if err := app.db.Save(nonOwner).Error; err != nil {
		t.Fatalf("Save(nonOwner): %v", err)
	}
	app.controlCodeCache.Set("tenant-member-auth", ControlCacheItem{uid: tailcfg.UserID(nonOwner.ID)}, time.Hour)
	rec = serveTenantFunnel(t, app, http.MethodPost, "/admin/api/funnel/domains", "tenant-member-auth", []byte(`{"domain":"custom.example.test","domainType":"custom"}`), nil, app.CAPIPostFunnelDomains)
	if !strings.Contains(rec.Body.String(), "权限不足") {
		t.Fatalf("expected owner-only failure, got %s", rec.Body.String())
	}

	rec = serveTenantFunnel(t, app, http.MethodPost, "/admin/api/funnel/domains", "tenant-owner-auth", []byte(`{"domain":"custom.example.test","domainType":"custom","listenerMode":"direct","edgeMode":"server_edge","tlsMode":"platform_managed"}`), nil, app.CAPIPostFunnelDomains)
	status, data := funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected status: %s", status)
	}
	domain := data["domain"].(map[string]any)
	if domain["status"] != FunnelDomainStatusPendingDNS {
		t.Fatalf("domain status = %#v", domain["status"])
	}
	if data["validationInstructions"] == nil {
		t.Fatal("expected validation instructions")
	}
	domainID, _ := strconv.ParseInt(domain["id"].(string), 10, 64)

	rec = serveTenantFunnel(t, app, http.MethodPost, "/admin/api/funnel/domains/"+strconv.FormatInt(domainID, 10)+"/verify", "tenant-owner-auth", []byte(`{"domainId":`+strconv.FormatInt(domainID, 10)+`}`), map[string]string{"id": strconv.FormatInt(domainID, 10)}, app.CAPIVerifyFunnelDomain)
	status, data = funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected verify status: %s", status)
	}
	if data["verificationDeferred"] != true {
		t.Fatalf("verificationDeferred = %#v", data["verificationDeferred"])
	}
}

func TestConsoleFunnelManagedServiceAndStatus(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)

	owner := createTestUser(t, app, "owner3@example.com", "Owner 3", "tenant-org-3", "Mirage")
	machine := createTestMachine(t, app, owner, "tenant-machine-3", "100.64.0.21")
	app.controlCodeCache.Set("tenant-test-auth", ControlCacheItem{uid: tailcfg.UserID(owner.ID)}, time.Hour)

	rec := serveTenantFunnel(t, app, http.MethodPost, "/admin/api/funnel/services", "tenant-test-auth", []byte(`{
		"machineId":`+strconv.FormatInt(machine.ID, 10)+`,
		"domainMode":"managed",
		"listenProto":"https",
		"listenPort":443,
		"mountPath":"/",
		"backendType":"http_proxy",
		"backendScheme":"http",
		"backendPort":8080
	}`), nil, app.CAPIPostFunnelServices)
	status, data := funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected create status: %s", status)
	}
	service := data["service"].(map[string]any)
	if service["configStatus"] != FunnelServiceConfigStatusPending {
		t.Fatalf("configStatus = %#v", service["configStatus"])
	}
	if service["edgeStatus"] != FunnelServiceEdgeStatusPending {
		t.Fatalf("edgeStatus = %#v", service["edgeStatus"])
	}
	if data["domain"] == nil || data["cert"] == nil {
		t.Fatal("expected domain and cert payload")
	}
	serviceID, _ := strconv.ParseInt(service["id"].(string), 10, 64)

	rec = serveTenantFunnel(t, app, http.MethodGet, "/admin/api/funnel/services", "tenant-test-auth", nil, nil, app.CAPIGetFunnelServices)
	status, data = funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected list status: %s", status)
	}
	services := data["services"].([]any)
	if len(services) == 0 {
		t.Fatal("expected service list")
	}

	rec = serveTenantFunnel(t, app, http.MethodGet, "/admin/api/funnel/services/"+strconv.FormatInt(serviceID, 10)+"/status", "tenant-test-auth", nil, map[string]string{"id": strconv.FormatInt(serviceID, 10)}, app.CAPIGetFunnelServiceStatus)
	status, data = funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected status endpoint: %s", status)
	}
	if data["domain"] == nil || data["cert"] == nil || data["currentEdge"] == nil {
		t.Fatal("expected full service status payload")
	}
}

func TestConsoleFunnelPatchEnableDisableLogs(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)

	owner := createTestUser(t, app, "owner4@example.com", "Owner 4", "tenant-org-4", "Mirage")
	machine := createTestMachine(t, app, owner, "tenant-machine-4", "100.64.0.22")
	app.controlCodeCache.Set("tenant-test-auth", ControlCacheItem{uid: tailcfg.UserID(owner.ID)}, time.Hour)
	domain, err := app.createManagedFunnelDomain(owner, allocateManagedFunnelDomain(&owner.Organization, machine, FunnelPlatformConfig{ManagedBaseDomain: app.cfg.BaseDomain}), 443, FunnelEdgeModeServer, FunnelListenerModeDirect)
	if err != nil {
		t.Fatalf("createManagedFunnelDomain(): %v", err)
	}
	service, _, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:     machine.ID,
		DomainID:      domain.ID,
		ListenProto:   FunnelListenProtoHTTPS,
		ListenPort:    443,
		MountPath:     "/",
		BackendType:   FunnelBackendTypeHTTPProxy,
		BackendScheme: "http",
		BackendPort:   8080,
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}
	if domain == nil || service == nil {
		t.Fatal("expected service and domain")
	}

	rec := serveTenantFunnel(t, app, http.MethodPatch, "/admin/api/funnel/services/"+strconv.FormatInt(service.ID, 10), "tenant-test-auth", []byte(`{"mountPath":"/app","backendScheme":"http","backendPort":8081,"enabled":false}`), map[string]string{"id": strconv.FormatInt(service.ID, 10)}, app.CAPIPatchFunnelService)
	status, data := funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected patch status: %s", status)
	}
	svc := data["service"].(map[string]any)
	if svc["mountPath"] != "/app" {
		t.Fatalf("mountPath = %#v", svc["mountPath"])
	}

	rec = serveTenantFunnel(t, app, http.MethodPost, "/admin/api/funnel/services/"+strconv.FormatInt(service.ID, 10)+"/disable", "tenant-test-auth", nil, map[string]string{"id": strconv.FormatInt(service.ID, 10)}, app.CAPIDisableFunnelService)
	status, data = funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected disable status: %s", status)
	}
	svc = data["service"].(map[string]any)
	if svc["configStatus"] != FunnelServiceConfigStatusDisabled {
		t.Fatalf("disabled configStatus = %#v", svc["configStatus"])
	}

	rec = serveTenantFunnel(t, app, http.MethodPost, "/admin/api/funnel/services/"+strconv.FormatInt(service.ID, 10)+"/enable", "tenant-test-auth", nil, map[string]string{"id": strconv.FormatInt(service.ID, 10)}, app.CAPIEnableFunnelService)
	status, data = funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected enable status: %s", status)
	}
	svc = data["service"].(map[string]any)
	if svc["configStatus"] != FunnelServiceConfigStatusPending {
		t.Fatalf("enabled configStatus = %#v", svc["configStatus"])
	}

	rec = serveTenantFunnel(t, app, http.MethodGet, "/admin/api/funnel/services/"+strconv.FormatInt(service.ID, 10)+"/logs", "tenant-test-auth", nil, map[string]string{"id": strconv.FormatInt(service.ID, 10)}, app.CAPIGetFunnelServiceLogs)
	status, data = funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected logs status: %s", status)
	}
	logs := data["logs"].([]any)
	if len(logs) != 0 {
		t.Fatalf("expected empty logs, got %#v", logs)
	}
}

func TestConsoleFunnelManagedServiceUsesRemoteEdgeDefault(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	sysCfg.FunnelCfg.DefaultEdgeMode = FunnelEdgeModeRemote
	sysCfg.FunnelCfg.DefaultListenerMode = FunnelListenerModeDirect
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}

	now := time.Now().UTC()
	edge := &FunnelEdge{
		EdgeType:     FunnelEdgeTypeNavi,
		EdgeNodeID:   "navi-1",
		Hostname:     "navi-1.example.test",
		SyncEndpoint: "/cockpit/api/funnel/edges/navi-1/sync",
		ListenerCapabilities: FunnelListenerCapabilities{
			SupportsHTTP: true,
			SupportsWS:   true,
		},
		HealthStatus: FunnelEdgeHealthHealthy,
		LastSeen:     &now,
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

	service, domain, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:     machine.ID,
		DomainMode:    "managed",
		ListenProto:   FunnelListenProtoHTTP,
		ListenPort:    80,
		MountPath:     "/",
		BackendType:   FunnelBackendTypeHTTPProxy,
		BackendScheme: "http",
		BackendPort:   8080,
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}
	if domain.EdgeMode != FunnelEdgeModeRemote {
		t.Fatalf("domain.EdgeMode = %q, want %q", domain.EdgeMode, FunnelEdgeModeRemote)
	}
	if domain.EdgeTargetID != "" {
		t.Fatalf("domain.EdgeTargetID = %q, want empty", domain.EdgeTargetID)
	}

	status, err := app.buildTenantFunnelServiceStatus(service)
	if err != nil {
		t.Fatalf("buildTenantFunnelServiceStatus(): %v", err)
	}
	currentEdge := status["currentEdge"].(map[string]any)
	if currentEdge["edgeNodeId"] != edge.EdgeNodeID {
		t.Fatalf("currentEdge.edgeNodeId = %#v", currentEdge["edgeNodeId"])
	}
	serviceMap := status["service"].(map[string]any)
	if serviceMap["configStatus"] != FunnelServiceConfigStatusActive {
		t.Fatalf("configStatus = %#v", serviceMap["configStatus"])
	}
	if serviceMap["edgeStatus"] != FunnelServiceEdgeStatusApplied {
		t.Fatalf("edgeStatus = %#v", serviceMap["edgeStatus"])
	}
}

func TestConsoleFunnelRemoteEdgeStaleStatusStaysPending(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	sysCfg.FunnelCfg.DefaultEdgeMode = FunnelEdgeModeRemote
	sysCfg.FunnelCfg.DefaultListenerMode = FunnelListenerModeDirect
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}

	stale := time.Now().UTC().Add(-funnelRemoteEdgeFreshnessWindow - time.Minute)
	edge := &FunnelEdge{
		EdgeType:     FunnelEdgeTypeNavi,
		EdgeNodeID:   "navi-stale",
		Hostname:     "navi-stale.example.test",
		SyncEndpoint: "/cockpit/api/funnel/edges/navi-stale/sync",
		ListenerCapabilities: FunnelListenerCapabilities{
			SupportsHTTP: true,
			SupportsWS:   true,
		},
		HealthStatus: FunnelEdgeHealthHealthy,
		LastSeen:     &stale,
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

	service, _, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
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

	status, err := app.buildTenantFunnelServiceStatus(service)
	if err != nil {
		t.Fatalf("buildTenantFunnelServiceStatus(): %v", err)
	}
	serviceMap := status["service"].(map[string]any)
	if serviceMap["configStatus"] != FunnelServiceConfigStatusPending {
		t.Fatalf("configStatus = %#v", serviceMap["configStatus"])
	}
	if serviceMap["edgeStatus"] != FunnelServiceEdgeStatusPending {
		t.Fatalf("edgeStatus = %#v", serviceMap["edgeStatus"])
	}
	if serviceMap["lastError"] != "当前remote-edge尚未完成最近一次同步" {
		t.Fatalf("lastError = %#v", serviceMap["lastError"])
	}
}

func TestCreateManagedFunnelDomainSyncsDNSProvider(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	setTenantTestFunnelDNSMgrConfig(t, app)

	fakeDNS := &fakeManagedFunnelDNSProvider{}
	app.newManagedFunnelDNSProvider = func(cfg FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
		if cfg.ManagedDNSProvider != FunnelManagedDNSProviderDNSMgr {
			t.Fatalf("ManagedDNSProvider = %q", cfg.ManagedDNSProvider)
		}
		return fakeDNS, nil
	}

	owner := createTestUser(t, app, "managed-owner@example.com", "Managed Owner", "managed-org", "Mirage")
	domainName := "machine-123-managed-org." + defaultFunnelDNSMgrBaseDomain
	domain, err := app.createManagedFunnelDomain(owner, domainName, 443, FunnelEdgeModeServer, FunnelListenerModeDirect)
	if err != nil {
		t.Fatalf("createManagedFunnelDomain(): %v", err)
	}
	if len(fakeDNS.ensured) != 1 || fakeDNS.ensured[0] != domain.Domain {
		t.Fatalf("ensured = %#v", fakeDNS.ensured)
	}
}

func TestCreateManagedFunnelDomainRollsBackOnDNSFailure(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	setTenantTestFunnelDNSMgrConfig(t, app)

	fakeDNS := &fakeManagedFunnelDNSProvider{ensureErr: errors.New("dns create failed")}
	app.newManagedFunnelDNSProvider = func(FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
		return fakeDNS, nil
	}

	owner := createTestUser(t, app, "rollback-owner@example.com", "Rollback Owner", "rollback-org", "Mirage")
	domainName := "machine-456-rollback-org." + defaultFunnelDNSMgrBaseDomain
	if _, err := app.createManagedFunnelDomain(owner, domainName, 443, FunnelEdgeModeServer, FunnelListenerModeDirect); err == nil {
		t.Fatal("expected createManagedFunnelDomain to fail")
	}

	domain := &FunnelDomain{}
	if err := app.db.Where("domain = ?", domainName).First(domain).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected domain rollback, got err=%v domain=%+v", err, domain)
	}
}

func TestDeleteManagedFunnelDomainSyncsDNSProvider(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	setTenantTestFunnelDNSMgrConfig(t, app)

	fakeDNS := &fakeManagedFunnelDNSProvider{}
	app.newManagedFunnelDNSProvider = func(FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
		return fakeDNS, nil
	}

	owner := createTestUser(t, app, "delete-owner@example.com", "Delete Owner", "delete-org", "Mirage")
	domainName := "machine-789-delete-org." + defaultFunnelDNSMgrBaseDomain
	domain, err := app.createManagedFunnelDomain(owner, domainName, 443, FunnelEdgeModeServer, FunnelListenerModeDirect)
	if err != nil {
		t.Fatalf("createManagedFunnelDomain(): %v", err)
	}
	fakeDNS.ensured = nil

	if err := app.deleteFunnelDomainWithChildren(domain); err != nil {
		t.Fatalf("deleteFunnelDomainWithChildren(): %v", err)
	}
	if len(fakeDNS.deleted) != 1 || fakeDNS.deleted[0] != domain.Domain {
		t.Fatalf("deleted = %#v", fakeDNS.deleted)
	}
}

func TestVerifyManagedFunnelDomainUpdatesDNSState(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	setTenantTestFunnelDNSMgrConfig(t, app)

	fakeDNS := &fakeManagedFunnelDNSProvider{
		lookup: managedFunnelDNSLookupResult{
			Ready:   false,
			Message: "未找到托管 DNS 记录",
		},
	}
	app.newManagedFunnelDNSProvider = func(FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
		return fakeDNS, nil
	}

	owner := createTestUser(t, app, "verify-owner@example.com", "Verify Owner", "verify-org", "Mirage")
	domainName := "machine-900-verify-org." + defaultFunnelDNSMgrBaseDomain
	domain, err := app.createManagedFunnelDomain(owner, domainName, 443, FunnelEdgeModeServer, FunnelListenerModeDirect)
	if err != nil {
		t.Fatalf("createManagedFunnelDomain(): %v", err)
	}

	result, err := verifyManagedFunnelDomain(app.db, domain, fakeDNS)
	if err != nil {
		t.Fatalf("verifyManagedFunnelDomain(): %v", err)
	}
	if result.Ready {
		t.Fatal("expected managed domain verification to report not ready")
	}

	stored := &FunnelDomain{}
	if err := app.db.First(stored, domain.ID).Error; err != nil {
		t.Fatalf("First(domain): %v", err)
	}
	if stored.DNSStatus != FunnelDNSStatusError {
		t.Fatalf("DNSStatus = %q", stored.DNSStatus)
	}
	if stored.LastDNSError != "未找到托管 DNS 记录" {
		t.Fatalf("LastDNSError = %q", stored.LastDNSError)
	}
}

func TestManagedFunnelDomainDNSFailureBlocksServiceReady(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	setTenantTestFunnelDNSMgrConfig(t, app)

	fakeDNS := &fakeManagedFunnelDNSProvider{
		lookup: managedFunnelDNSLookupResult{
			Ready:   false,
			Message: "托管 DNS 记录当前已暂停",
		},
	}
	app.newManagedFunnelDNSProvider = func(FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
		return fakeDNS, nil
	}

	owner := createTestUser(t, app, "status-owner@example.com", "Status Owner", "status-org", "Mirage")
	machine := createTestMachine(t, app, owner, "status-machine", "100.64.0.77")
	service, domain, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:      machine.ID,
		DomainMode:     "managed",
		ListenProto:    FunnelListenProtoHTTPS,
		ListenPort:     443,
		MountPath:      "/",
		BackendType:    FunnelBackendTypeHTTPProxy,
		BackendScheme:  "http",
		BackendPort:    8080,
		BackendTailnet: "100.64.0.77",
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}
	if _, err := verifyManagedFunnelDomain(app.db, domain, fakeDNS); err != nil {
		t.Fatalf("verifyManagedFunnelDomain(): %v", err)
	}

	status, err := app.buildTenantFunnelServiceStatus(service)
	if err != nil {
		t.Fatalf("buildTenantFunnelServiceStatus(): %v", err)
	}
	serviceMap := status["service"].(map[string]any)
	if serviceMap["configStatus"] != FunnelServiceConfigStatusPending {
		t.Fatalf("configStatus = %#v", serviceMap["configStatus"])
	}
	if serviceMap["lastError"] != "托管 DNS 记录当前已暂停" {
		t.Fatalf("lastError = %#v", serviceMap["lastError"])
	}
}

func TestConsoleVerifyManagedFunnelDomainReturnsLookupResult(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	setTenantTestFunnelDNSMgrConfig(t, app)

	fakeDNS := &fakeManagedFunnelDNSProvider{
		lookup: managedFunnelDNSLookupResult{
			Ready:   false,
			Message: "未找到托管 DNS 记录",
		},
	}
	app.newManagedFunnelDNSProvider = func(FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
		return fakeDNS, nil
	}

	owner := createTestUser(t, app, "api-verify-owner@example.com", "API Verify Owner", "api-verify-org", "Mirage")
	app.controlCodeCache.Set("api-verify-auth", ControlCacheItem{uid: tailcfg.UserID(owner.ID)}, time.Hour)
	domain, err := app.createManagedFunnelDomain(owner, "machine-901-api-verify-org."+defaultFunnelDNSMgrBaseDomain, 443, FunnelEdgeModeServer, FunnelListenerModeDirect)
	if err != nil {
		t.Fatalf("createManagedFunnelDomain(): %v", err)
	}

	rec := serveTenantFunnel(
		t,
		app,
		http.MethodPost,
		"/admin/api/funnel/domains/"+strconv.FormatInt(domain.ID, 10)+"/verify",
		"api-verify-auth",
		[]byte(`{"domainId":`+strconv.FormatInt(domain.ID, 10)+`}`),
		map[string]string{"id": strconv.FormatInt(domain.ID, 10)},
		app.CAPIVerifyFunnelDomain,
	)
	status, data := funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("status = %s body=%s", status, rec.Body.String())
	}
	if data["verified"] != false {
		t.Fatalf("verified = %#v", data["verified"])
	}
	if data["verificationMessage"] != "未找到托管 DNS 记录" {
		t.Fatalf("verificationMessage = %#v", data["verificationMessage"])
	}
}

func TestValidateFunnelListenBackendPair(t *testing.T) {
	t.Parallel()

	if err := validateFunnelListenBackendPair(FunnelListenProtoHTTP, FunnelBackendTypeHTTPProxy); err != nil {
		t.Fatalf("http/http_proxy should be valid: %v", err)
	}
	if err := validateFunnelListenBackendPair(FunnelListenProtoTCP, FunnelBackendTypeTCPProxy); err != nil {
		t.Fatalf("tcp/tcp_proxy should be valid: %v", err)
	}
	if err := validateFunnelListenBackendPair(FunnelListenProtoTCP, FunnelBackendTypeHTTPProxy); err == nil {
		t.Fatal("expected tcp/http_proxy to be rejected")
	}
	if err := validateFunnelListenBackendPair(FunnelListenProtoHTTPS, FunnelBackendTypeTCPProxy); err == nil {
		t.Fatal("expected https/tcp_proxy to be rejected")
	}
}
