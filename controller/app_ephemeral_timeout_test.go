package controller

import (
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newEphemeralTimeoutTestMirage(t *testing.T) *Mirage {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "ephemeral-timeout-test.sqlite")
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

	for _, model := range []any{
		&Organization{},
		&User{},
		&PreAuthKey{},
		&Machine{},
	} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatalf("AutoMigrate(%T): %v", model, err)
		}
	}

	return &Mirage{
		cfg: &Config{},
		db:  db,
	}
}

func TestExpireEphemeralNodesWorkerUsesConfiguredTimeout(t *testing.T) {
	t.Parallel()

	app := newEphemeralTimeoutTestMirage(t)
	app.cfg.EphemeralNodeInactivityTimeout = 2 * time.Minute

	org := &Organization{
		Name:               "ephemeral-org",
		Provider:           "Mirage",
		ExpiryDuration:     DefaultExpireTime,
		FileSharingEnabled: boolPtr(true),
		MagicDnsDomain:     "ephemeral-org.example.test",
	}
	if err := app.db.Create(org).Error; err != nil {
		t.Fatalf("Create(org): %v", err)
	}

	user := &User{
		Name:           "ephemeral-user",
		Display_Name:   "Ephemeral User",
		OrganizationID: org.ID,
		Role:           RoleOwner,
	}
	if err := app.db.Create(user).Error; err != nil {
		t.Fatalf("Create(user): %v", err)
	}

	lastSeen := time.Now().UTC().Add(-2 * time.Minute)
	authKey := &PreAuthKey{
		Key:       "test-auth-key",
		UserID:    user.ID,
		Ephemeral: true,
	}
	if err := app.db.Create(authKey).Error; err != nil {
		t.Fatalf("Create(authKey): %v", err)
	}

	machine := &Machine{
		MachineKey:  "machine-key",
		NodeKey:     "node-key",
		DiscoKey:    "disco-key",
		Hostname:    "ephemeral-node",
		GivenName:   "ephemeral-node",
		UserID:      user.ID,
		AuthKeyID:   uint(authKey.ID),
		LastSeen:    &lastSeen,
		Endpoints:   StringList{"127.0.0.1:12345"},
		IPAddresses: MachineAddresses{netip.MustParseAddr("100.64.0.50")},
	}
	if err := app.db.Create(machine).Error; err != nil {
		t.Fatalf("Create(machine): %v", err)
	}

	app.expireEphemeralNodesWorker()

	var remaining int64
	if err := app.db.Model(&Machine{}).Where("id = ?", machine.ID).Count(&remaining).Error; err != nil {
		t.Fatalf("Count(machine): %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected ephemeral machine to be removed when configured timeout elapses, got %d remaining rows", remaining)
	}
}

func TestNormalizeEphemeralNodeInactivityTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input time.Duration
		want  time.Duration
	}{
		{
			name:  "default when unset",
			input: 0,
			want:  EphemeralNodeInactivityTimeout,
		},
		{
			name:  "clamp below keepalive floor",
			input: time.Minute,
			want:  minimumEphemeralNodeInactivityTimeout,
		},
		{
			name:  "keep valid custom timeout",
			input: 2 * time.Minute,
			want:  2 * time.Minute,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := normalizeEphemeralNodeInactivityTimeout(tt.input); got != tt.want {
				t.Fatalf("normalizeEphemeralNodeInactivityTimeout(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSysConfigEphemeralTimeoutNormalization(t *testing.T) {
	t.Parallel()

	base := SysConfig{
		ServerURL:  "ctrl.example.test",
		Addr:       "127.0.0.1:8080",
		Mip4:       mustIPPrefix(t, "100.64.0.0/10"),
		Mip6:       mustIPPrefix(t, "fd7a:115c:a1e0::/48"),
		Basedomain: "mira.net",
	}

	t.Run("general cfg reports normalized default", func(t *testing.T) {
		t.Parallel()

		got := base.toGeneralCfg().EphemeralNodeInactivityTimeout
		if got != EphemeralNodeInactivityTimeout {
			t.Fatalf("toGeneralCfg() timeout = %v, want %v", got, EphemeralNodeInactivityTimeout)
		}
	})

	t.Run("srv config clamps too-small timeout", func(t *testing.T) {
		t.Parallel()

		sysCfg := base
		sysCfg.EphemeralNodeInactivityTimeout = time.Minute

		cfg, err := sysCfg.toSrvConfig()
		if err != nil {
			t.Fatalf("toSrvConfig() returned error: %v", err)
		}
		if cfg.EphemeralNodeInactivityTimeout != minimumEphemeralNodeInactivityTimeout {
			t.Fatalf("toSrvConfig() timeout = %v, want %v", cfg.EphemeralNodeInactivityTimeout, minimumEphemeralNodeInactivityTimeout)
		}
	})
}
