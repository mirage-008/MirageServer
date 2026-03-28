package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newFunnelTestCockpit(t *testing.T) *Cockpit {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "cockpit-funnel-test.sqlite")
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
	if err := db.Create(&SysAdmin{}).Error; err != nil {
		t.Fatalf("Create(SysAdmin): %v", err)
	}

	sysCfg := &SysConfig{
		ServerURL:  "ctrl.example.test",
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

	cockpit, err := NewCockpit("127.0.0.1:8080", make(chan CtrlMsg), make(chan CtrlMsg), db)
	if err != nil {
		t.Fatalf("NewCockpit(): %v", err)
	}
	cockpit.authCache.Set("AuthCode", "funnel-test-auth", time.Hour)
	return cockpit
}

func funnelAuthedRequest(method, target string, body []byte) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "mirage_cockpit_auth", Value: "funnel-test-auth"})
	return req
}

func decodeFunnelAPIResponse(t *testing.T, body []byte) (string, map[string]any) {
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

func TestCockpitFunnelAuthzAndConfig(t *testing.T) {
	t.Parallel()

	cockpit := newFunnelTestCockpit(t)
	router := cockpit.createRouter()

	t.Run("unauthorized", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cockpit/api/funnel/config", nil))
		if !strings.Contains(rec.Body.String(), "error-unauthorized") {
			t.Fatalf("expected unauthorized response, got %s", rec.Body.String())
		}
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodGet, "/cockpit/api/funnel/config", nil))
	status, data := decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected status: %s", status)
	}
	config := data["config"].(map[string]any)
	if config["managedBaseDomain"] != "funnel.example.test" {
		t.Fatalf("managedBaseDomain = %#v", config["managedBaseDomain"])
	}
	if config["defaultEdgeMode"] != FunnelEdgeModeServer {
		t.Fatalf("defaultEdgeMode = %#v", config["defaultEdgeMode"])
	}
	if config["defaultListenerMode"] != FunnelListenerModeDirect {
		t.Fatalf("defaultListenerMode = %#v", config["defaultListenerMode"])
	}
	targets := data["effectiveIngressTargets"].([]any)
	if len(targets) == 0 {
		t.Fatal("expected ingress targets")
	}

	postBody, _ := json.Marshal(FunnelPlatformConfigRequest{
		ManagedBaseDomain:   "https://PUBLIC.EXAMPLE.COM/",
		DefaultEdgeMode:     FunnelEdgeModeRemote,
		DefaultListenerMode: FunnelListenerModeBehindProxy,
		DirectBindAddrs:     StringList{" 127.0.0.1 ", "0.0.0.0"},
		DirectBindPorts:     FunnelPortList{443, 80, 443},
		TrustedProxyCIDRs:   StringList{"10.0.0.0/24", " 10.0.0.0/24 "},
	})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodPost, "/cockpit/api/funnel/config", postBody))
	status, data = decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected post status: %s", status)
	}
	config = data["config"].(map[string]any)
	if config["managedBaseDomain"] != "public.example.com" {
		t.Fatalf("managedBaseDomain = %#v", config["managedBaseDomain"])
	}
	addrs := config["directBindAddrs"].([]any)
	if len(addrs) != 2 {
		t.Fatalf("directBindAddrs = %#v", addrs)
	}
	ports := config["directBindPorts"].([]any)
	if len(ports) != 2 {
		t.Fatalf("directBindPorts = %#v", ports)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodGet, "/cockpit/api/funnel/edges", nil))
	status, data = decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected edge status: %s", status)
	}
	edges := data["edges"].([]any)
	if len(edges) == 0 {
		t.Fatal("expected at least one edge")
	}
	edge := edges[0].(map[string]any)
	if edge["edgeType"] != FunnelEdgeTypeServer {
		t.Fatalf("edgeType = %#v", edge["edgeType"])
	}
}

func TestCockpitFunnelDeferredActions(t *testing.T) {
	t.Parallel()

	cockpit := newFunnelTestCockpit(t)
	router := cockpit.createRouter()

	domain := &FunnelDomain{
		OrgID:        1,
		Domain:       "custom.example.test",
		DomainType:   FunnelDomainTypeCustom,
		Status:       FunnelDomainStatusPendingDNS,
		DNSStatus:    FunnelDNSStatusPending,
		TLSMode:      FunnelTLSModePlatformManaged,
		ListenerMode: FunnelListenerModeDirect,
		EdgeMode:     FunnelEdgeModeServer,
	}
	if err := cockpit.db.Create(domain).Error; err != nil {
		t.Fatalf("Create(domain): %v", err)
	}
	cert := &FunnelCert{
		DomainID:   domain.ID,
		CertStatus: FunnelCertStatusPending,
	}
	if err := cockpit.db.Create(cert).Error; err != nil {
		t.Fatalf("Create(cert): %v", err)
	}

	edge := buildDefaultServerEdge(FunnelPlatformConfig{
		ManagedBaseDomain:   "funnel.example.test",
		DefaultEdgeMode:     FunnelEdgeModeServer,
		DefaultListenerMode: FunnelListenerModeDirect,
		DirectBindAddrs:     StringList{"0.0.0.0", "::"},
		DirectBindPorts:     FunnelPortList{80, 443},
	})
	if err := cockpit.db.Create(&edge).Error; err != nil {
		t.Fatalf("Create(edge): %v", err)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodPost, "/cockpit/api/funnel/edges/"+strconv.FormatInt(edge.ID, 10)+"/sync", nil))
	status, data := decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected sync status: %s", status)
	}
	if data["syncDeferred"] != true {
		t.Fatalf("syncDeferred = %#v", data["syncDeferred"])
	}

	body, _ := json.Marshal(FunnelDomainVerifyRequest{DomainID: domain.ID})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodPost, "/cockpit/api/funnel/domains/verify", body))
	status, data = decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected domain verify status: %s", status)
	}
	if data["verificationDeferred"] != true {
		t.Fatalf("verificationDeferred = %#v", data["verificationDeferred"])
	}
	if data["domain"] == nil {
		t.Fatal("expected domain payload")
	}

	body, _ = json.Marshal(FunnelCertRenewRequest{DomainID: domain.ID})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodPost, "/cockpit/api/funnel/certs/renew", body))
	status, data = decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected cert renew status: %s", status)
	}
	if data["renewDeferred"] != true {
		t.Fatalf("renewDeferred = %#v", data["renewDeferred"])
	}
	if data["cert"] == nil {
		t.Fatal("expected cert payload")
	}

	var auditCount int64
	if err := cockpit.db.Model(&FunnelAudit{}).Count(&auditCount).Error; err != nil {
		t.Fatalf("Count(FunnelAudit): %v", err)
	}
	if auditCount < 3 {
		t.Fatalf("expected audit rows, got %d", auditCount)
	}
}

func TestCockpitRemoteEdgeSyncPayload(t *testing.T) {
	t.Parallel()

	cockpit := newFunnelTestCockpit(t)
	router := cockpit.createRouter()

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
	if err := cockpit.db.Create(edge).Error; err != nil {
		t.Fatalf("Create(edge): %v", err)
	}

	domain := &FunnelDomain{
		OrgID:               1,
		Domain:              "remote.example.test",
		DomainType:          FunnelDomainTypeCustom,
		Status:              FunnelDomainStatusActive,
		DNSStatus:           FunnelDNSStatusReady,
		TLSMode:             FunnelTLSModePlatformManaged,
		ListenerMode:        FunnelListenerModeDirect,
		EdgeMode:            FunnelEdgeModeRemote,
		EdgeTargetID:        edge.EdgeNodeID,
		ValidationMethod:    "dns-txt",
		ValidationTarget:    "_mirage-funnel.remote.example.test",
		ValidationToken:     "token",
		ValidationCheckedAt: &now,
	}
	if err := cockpit.db.Create(domain).Error; err != nil {
		t.Fatalf("Create(domain): %v", err)
	}
	cert := &FunnelCert{
		DomainID:   domain.ID,
		CertStatus: FunnelCertStatusPending,
	}
	if err := cockpit.db.Create(cert).Error; err != nil {
		t.Fatalf("Create(cert): %v", err)
	}
	service := &FunnelService{
		OrgID:            1,
		MachineID:        100,
		DomainID:         domain.ID,
		Enabled:          true,
		Public:           true,
		ListenProto:      FunnelListenProtoHTTP,
		ListenPort:       80,
		MountPath:        "/",
		BackendType:      FunnelBackendTypeHTTPProxy,
		BackendScheme:    "http",
		BackendTailnetIP: "100.64.0.20",
		BackendPort:      8080,
		ConfigStatus:     FunnelServiceConfigStatusPending,
		EdgeStatus:       FunnelServiceEdgeStatusPending,
		BackendStatus:    FunnelServiceBackendStatusUnknown,
	}
	if err := cockpit.db.Create(service).Error; err != nil {
		t.Fatalf("Create(service): %v", err)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodPost, "/cockpit/api/funnel/edges/"+strconv.FormatInt(edge.ID, 10)+"/sync", nil))
	status, data := decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected sync status: %s", status)
	}
	if data["syncDeferred"] != false {
		t.Fatalf("syncDeferred = %#v", data["syncDeferred"])
	}
	if data["serviceCount"] != float64(1) {
		t.Fatalf("serviceCount = %#v", data["serviceCount"])
	}
	services, ok := data["services"].([]any)
	if !ok || len(services) != 1 {
		t.Fatalf("services = %#v", data["services"])
	}
	item := services[0].(map[string]any)
	publicInfo := item["public"].(map[string]any)
	if publicInfo["host"] != domain.Domain {
		t.Fatalf("public host = %#v", publicInfo["host"])
	}
	edgeMap := data["edge"].(map[string]any)
	if edgeMap["edgeNodeId"] != edge.EdgeNodeID {
		t.Fatalf("edgeNodeId = %#v", edgeMap["edgeNodeId"])
	}
}
