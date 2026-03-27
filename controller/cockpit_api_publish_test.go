package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newPublishTestCockpit(t *testing.T) *Cockpit {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "cockpit-publish-test.sqlite")
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

	sysCfg := &SysConfig{
		ServerURL:  "ctrl.example.test",
		Addr:       "127.0.0.1:8080",
		Mip4:       mustIPPrefix(t, "100.64.0.0/10"),
		Mip6:       mustIPPrefix(t, "fd7a:115c:a1e0::/48"),
		Basedomain: "mira.test",
		DerpUrl:    defaultRemoteDERPMapURL,
	}
	if err := db.Create(sysCfg).Error; err != nil {
		t.Fatalf("Create(sysCfg): %v", err)
	}

	cockpit, err := NewCockpit("127.0.0.1:8080", make(chan CtrlMsg), make(chan CtrlMsg), db)
	if err != nil {
		t.Fatalf("NewCockpit(): %v", err)
	}

	return cockpit
}

func TestGetPublishInfoUsesSameOriginUploadPath(t *testing.T) {
	t.Parallel()

	cockpit := newPublishTestCockpit(t)
	req := httptest.NewRequest(http.MethodGet, "/cockpit/api/publish", nil)
	rec := httptest.NewRecorder()

	cockpit.GetPublishInfo(rec, req)

	var res struct {
		Status string          `json:"status"`
		Data   PublishInfoData `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("json.Unmarshal(response): %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("unexpected status: %q", res.Status)
	}
	if res.Data.UploadURL != "/cockpit/api/publish" {
		t.Fatalf("UploadURL = %q, want %q", res.Data.UploadURL, "/cockpit/api/publish")
	}
}
