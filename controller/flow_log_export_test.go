package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tailscale.com/tailcfg"
)

func TestFlowLogExportWorkerHTTP(t *testing.T) {
	t.Parallel()

	var authHeader string
	var gotCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		defer r.Body.Close()
		payload := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode(): %v", err)
		}
		gotCount = int(payload["count"].(float64))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	app := newShareInviteTestMirage(t)
	app.cfg.FlowLogCfg = FlowLogConfig{
		Enabled: true,
		Export: FlowLogExportConfig{
			Enabled:   true,
			Target:    flowLogExportTargetHTTP,
			URL:       server.URL,
			APIKey:    "webhook-secret",
			BatchSize: 10,
		},
	}
	if err := migrateFlowLogTables(app.db); err != nil {
		t.Fatalf("migrateFlowLogTables(): %v", err)
	}

	entry := storeFlowLogForExportTest(t, app, "http")
	if err := app.exportFlowLogsWorker(); err != nil {
		t.Fatalf("exportFlowLogsWorker(): %v", err)
	}
	if authHeader != "Bearer webhook-secret" {
		t.Fatalf("Authorization=%q", authHeader)
	}
	if gotCount != 1 {
		t.Fatalf("count=%d want 1", gotCount)
	}

	var stored FlowLogEntry
	if err := app.db.First(&stored, entry.ID).Error; err != nil {
		t.Fatalf("First(entry): %v", err)
	}
	if stored.ExportedAt == nil {
		t.Fatal("expected exported_at to be set")
	}
	if stored.LastExportAttemptAt == nil {
		t.Fatal("expected last_export_attempt_at to be set")
	}
	if stored.ExportAttempts != 1 {
		t.Fatalf("exportAttempts=%d want 1", stored.ExportAttempts)
	}
	if stored.ExportError != "" {
		t.Fatalf("unexpected export error: %q", stored.ExportError)
	}
}

func TestFlowLogExportWorkerElasticsearch(t *testing.T) {
	t.Parallel()

	var authHeader string
	var requestPath string
	var docLines int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		requestPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll(): %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		docLines = len(lines)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":false,"items":[]}`))
	}))
	defer server.Close()

	app := newShareInviteTestMirage(t)
	app.cfg.FlowLogCfg = FlowLogConfig{
		Enabled: true,
		Export: FlowLogExportConfig{
			Enabled:   true,
			Target:    flowLogExportTargetElasticsearch,
			URL:       server.URL,
			APIKey:    "es-secret",
			Index:     "tenant-flow-logs",
			BatchSize: 10,
		},
	}
	if err := migrateFlowLogTables(app.db); err != nil {
		t.Fatalf("migrateFlowLogTables(): %v", err)
	}

	entry := storeFlowLogForExportTest(t, app, "es")
	if err := app.exportFlowLogsWorker(); err != nil {
		t.Fatalf("exportFlowLogsWorker(): %v", err)
	}
	if authHeader != "ApiKey es-secret" {
		t.Fatalf("Authorization=%q", authHeader)
	}
	if requestPath != "/tenant-flow-logs/_bulk" {
		t.Fatalf("path=%q", requestPath)
	}
	if docLines != 2 {
		t.Fatalf("docLines=%d want 2", docLines)
	}

	var stored FlowLogEntry
	if err := app.db.First(&stored, entry.ID).Error; err != nil {
		t.Fatalf("First(entry): %v", err)
	}
	if stored.ExportedAt == nil {
		t.Fatal("expected exported_at to be set")
	}
}

func TestFlowLogExportWorkerFailureKeepsPending(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failure", http.StatusBadGateway)
	}))
	defer server.Close()

	app := newShareInviteTestMirage(t)
	app.cfg.FlowLogCfg = FlowLogConfig{
		Enabled: true,
		Export: FlowLogExportConfig{
			Enabled: true,
			Target:  flowLogExportTargetHTTP,
			URL:     server.URL,
		},
	}
	if err := migrateFlowLogTables(app.db); err != nil {
		t.Fatalf("migrateFlowLogTables(): %v", err)
	}

	entry := storeFlowLogForExportTest(t, app, "failure")
	if err := app.exportFlowLogsWorker(); err == nil {
		t.Fatal("expected exportFlowLogsWorker to fail")
	}

	var stored FlowLogEntry
	if err := app.db.First(&stored, entry.ID).Error; err != nil {
		t.Fatalf("First(entry): %v", err)
	}
	if stored.ExportedAt != nil {
		t.Fatal("expected exported_at to remain nil")
	}
	if stored.LastExportAttemptAt == nil {
		t.Fatal("expected last_export_attempt_at to be set")
	}
	if stored.ExportAttempts != 1 {
		t.Fatalf("exportAttempts=%d want 1", stored.ExportAttempts)
	}
	if !strings.Contains(stored.ExportError, "status=502") {
		t.Fatalf("unexpected export error: %q", stored.ExportError)
	}
}

func storeFlowLogForExportTest(t *testing.T, app *Mirage, suffix string) FlowLogEntry {
	t.Helper()

	owner := createTestUser(t, app, fmt.Sprintf("%s@example.com", suffix), "Owner "+suffix, "org-"+suffix, "Mirage")
	machine := createTestMachine(t, app, owner, "node-"+suffix, "100.64.10.10")
	org, err := app.GetOrgnaizationByID(owner.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}

	nodeLogID, err := app.ensureMachineDataPlaneAuditLogID(machine)
	if err != nil {
		t.Fatalf("ensureMachineDataPlaneAuditLogID(): %v", err)
	}
	domainLogID, err := app.ensureOrganizationDomainAuditLogID(org)
	if err != nil {
		t.Fatalf("ensureOrganizationDomainAuditLogID(): %v", err)
	}

	start := time.Now().UTC().Add(-30 * time.Second).Round(time.Second)
	end := start.Add(5 * time.Second)
	body := marshalFlowLogUpload(t, tailcfg.StableNodeID(fmt.Sprintf("%d", machine.ID)), start, end, "100.64.10.10", "100.64.10.20")
	if count, err := app.storeFlowLogBatch(flowLogCollectionTailtraffic, nodeLogID, domainLogID, body, time.Now().UTC()); err != nil {
		t.Fatalf("storeFlowLogBatch(): %v", err)
	} else if count != 1 {
		t.Fatalf("storeFlowLogBatch count=%d want 1", count)
	}

	var entry FlowLogEntry
	if err := app.db.Order("id desc").First(&entry).Error; err != nil {
		t.Fatalf("First(entry): %v", err)
	}
	return entry
}
