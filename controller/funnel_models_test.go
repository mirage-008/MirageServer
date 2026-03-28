package controller

import (
	"database/sql/driver"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newFunnelTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "funnel-models-test.sqlite")
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

	return db
}

func TestInitFunnelTables(t *testing.T) {
	t.Parallel()

	db := newFunnelTestDB(t)
	if err := migrateFunnelTables(db); err != nil {
		t.Fatalf("migrateFunnelTables(): %v", err)
	}

	for _, model := range []any{
		&FunnelDomain{},
		&FunnelService{},
		&FunnelEdge{},
		&FunnelCert{},
		&FunnelAudit{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing table for %T", model)
		}
	}

	if !db.Migrator().HasColumn(&FunnelService{}, "listen_proto") {
		t.Fatal("missing listen_proto column")
	}
}

func TestFunnelModelsAssignStableID(t *testing.T) {
	t.Parallel()

	db := newFunnelTestDB(t)
	if err := migrateFunnelTables(db); err != nil {
		t.Fatalf("migrateFunnelTables(): %v", err)
	}

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
	if err := db.Create(domain).Error; err != nil {
		t.Fatalf("Create(domain): %v", err)
	}
	if domain.ID == 0 || domain.StableID == "" {
		t.Fatalf("expected domain stable id, got ID=%d StableID=%q", domain.ID, domain.StableID)
	}

	edge := &FunnelEdge{
		EdgeType:   FunnelEdgeTypeServer,
		EdgeNodeID: "mirage-server",
		Hostname:   "ctrl.example.test",
		PublicAddrs: FunnelPublicAddrList{
			{Network: "tcp", Address: "203.0.113.10", Port: 443},
		},
		ListenerCapabilities: FunnelListenerCapabilities{
			SupportsHTTP:       true,
			SupportsWS:         true,
			SupportsDirectBind: true,
		},
		HealthStatus:    FunnelEdgeHealthUnknown,
		Allocatable:     true,
		TrustProxyCIDRs: StringList{"10.0.0.0/24"},
	}
	if err := db.Create(edge).Error; err != nil {
		t.Fatalf("Create(edge): %v", err)
	}
	if edge.ID == 0 || edge.StableID == "" {
		t.Fatalf("expected edge stable id, got ID=%d StableID=%q", edge.ID, edge.StableID)
	}

	service := &FunnelService{
		OrgID:            1,
		MachineID:        2,
		DomainID:         domain.ID,
		Enabled:          true,
		Public:           true,
		ListenProto:      FunnelListenProtoHTTPS,
		ListenPort:       443,
		MountPath:        "/",
		BackendType:      FunnelBackendTypeHTTPProxy,
		BackendScheme:    "http",
		BackendTailnetIP: "100.64.0.2",
		BackendPort:      8080,
		ConfigStatus:     FunnelServiceConfigStatusPending,
		EdgeStatus:       FunnelServiceEdgeStatusPending,
		BackendStatus:    FunnelServiceBackendStatusUnknown,
	}
	if err := db.Create(service).Error; err != nil {
		t.Fatalf("Create(service): %v", err)
	}
	if service.ID == 0 || service.StableID == "" {
		t.Fatalf("expected service stable id, got ID=%d StableID=%q", service.ID, service.StableID)
	}

	cert := &FunnelCert{
		DomainID:   domain.ID,
		CertStatus: FunnelCertStatusPending,
	}
	if err := db.Create(cert).Error; err != nil {
		t.Fatalf("Create(cert): %v", err)
	}
	if cert.ID == 0 || cert.StableID == "" {
		t.Fatalf("expected cert stable id, got ID=%d StableID=%q", cert.ID, cert.StableID)
	}

	audit := &FunnelAudit{
		OrgID:        1,
		ActorType:    "system",
		ActorID:      "test",
		ResourceType: "service",
		ResourceID:   service.StableID,
		Action:       "service_created",
		Payload:      `{"ok":true}`,
		CreatedAt:    time.Now().UTC(),
	}
	if err := db.Create(audit).Error; err != nil {
		t.Fatalf("Create(audit): %v", err)
	}
	if audit.ID == 0 || audit.StableID == "" {
		t.Fatalf("expected audit stable id, got ID=%d StableID=%q", audit.ID, audit.StableID)
	}
}

func TestFunnelJSONValueTypesRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source interface {
			Value() (driver.Value, error)
		}
		target interface {
			Scan(interface{}) error
		}
	}{
		{
			name: "public addrs",
			source: FunnelPublicAddrList{
				{Network: "tcp", Address: "203.0.113.10", Port: 443},
			},
			target: new(FunnelPublicAddrList),
		},
		{
			name: "listener capabilities",
			source: FunnelListenerCapabilities{
				SupportsHTTP:       true,
				SupportsDirectBind: true,
			},
			target: new(FunnelListenerCapabilities),
		},
		{
			name: "platform config",
			source: FunnelPlatformConfig{
				ManagedBaseDomain:   "public.example.test",
				DefaultEdgeMode:     FunnelEdgeModeServer,
				DefaultListenerMode: FunnelListenerModeDirect,
				DirectBindAddrs:     StringList{"0.0.0.0", "::"},
				DirectBindPorts:     FunnelPortList{80, 443},
				TrustedProxyCIDRs:   StringList{"10.0.0.0/24"},
			},
			target: new(FunnelPlatformConfig),
		},
		{
			name:   "port list",
			source: FunnelPortList{80, 443},
			target: new(FunnelPortList),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw, err := tt.source.Value()
			if err != nil {
				t.Fatalf("Value(): %v", err)
			}
			if err := tt.target.Scan(raw); err != nil {
				t.Fatalf("Scan(): %v", err)
			}
		})
	}
}
