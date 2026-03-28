package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"tailscale.com/tailcfg"
	"tailscale.com/types/ipproto"
	"tailscale.com/types/netlogtype"
)

func TestInitFlowLogTables(t *testing.T) {
	t.Parallel()

	db := newFunnelTestDB(t)
	initSmokeSchema(t, db)
	if err := migrateFlowLogTables(db); err != nil {
		t.Fatalf("migrateFlowLogTables(): %v", err)
	}
	if !db.Migrator().HasTable(&FlowLogEntry{}) {
		t.Fatal("missing flow_log_entries table")
	}
	if !db.Migrator().HasColumn(&Machine{}, "data_plane_audit_log_id") {
		t.Fatal("missing machine data_plane_audit_log_id column")
	}
	if !db.Migrator().HasColumn(&Organization{}, "domain_audit_log_id") {
		t.Fatal("missing organization domain_audit_log_id column")
	}
}

func TestGenerateMapResponseFlowLogsDisabledByDefault(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	owner := createTestUser(t, app, "owner@example.com", "Owner", "flow-org", "Mirage")
	machine := createTestMachine(t, app, owner, "flow-node", "100.64.0.10")

	resp, err := app.generateMapResponse(tailcfg.MapRequest{Hostinfo: &tailcfg.Hostinfo{Hostname: machine.Hostname, OS: "linux"}}, machine, &mapResponseStreamState{})
	if err != nil {
		t.Fatalf("generateMapResponse(): %v", err)
	}
	if resp.Node.DataPlaneAuditLogID != "" {
		t.Fatalf("unexpected node audit log id: %q", resp.Node.DataPlaneAuditLogID)
	}
	if resp.DomainDataPlaneAuditLogID != "" {
		t.Fatalf("unexpected domain audit log id: %q", resp.DomainDataPlaneAuditLogID)
	}
	if _, ok := resp.Node.CapMap[tailcfg.CapabilityDataPlaneAuditLogs]; ok {
		t.Fatal("unexpected data plane audit logs capability")
	}
	if resp.Debug == nil || !resp.Debug.DisableLogTail {
		t.Fatalf("expected DisableLogTail=true, got %#v", resp.Debug)
	}
}

func TestGenerateMapResponseFlowLogsEnabled(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	app.cfg.FlowLogCfg = FlowLogConfig{Enabled: true, LogExitFlows: true}
	owner := createTestUser(t, app, "owner@example.com", "Owner", "flow-org", "Mirage")
	machine := createTestMachine(t, app, owner, "flow-node", "100.64.0.10")

	resp, err := app.generateMapResponse(tailcfg.MapRequest{Hostinfo: &tailcfg.Hostinfo{Hostname: machine.Hostname, OS: "linux"}}, machine, &mapResponseStreamState{})
	if err != nil {
		t.Fatalf("generateMapResponse(): %v", err)
	}
	if resp.Node.DataPlaneAuditLogID == "" {
		t.Fatal("expected node audit log id")
	}
	if resp.DomainDataPlaneAuditLogID == "" {
		t.Fatal("expected domain audit log id")
	}
	if _, ok := resp.Node.CapMap[tailcfg.CapabilityDataPlaneAuditLogs]; !ok {
		t.Fatal("missing data plane audit logs capability")
	}
	if _, ok := resp.Node.CapMap[tailcfg.NodeAttrLogExitFlows]; !ok {
		t.Fatal("missing log-exit-flows attribute")
	}
	if resp.Debug == nil || resp.Debug.DisableLogTail {
		t.Fatalf("expected DisableLogTail=false, got %#v", resp.Debug)
	}

	var dbMachine Machine
	if err := app.db.First(&dbMachine, machine.ID).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	if dbMachine.DataPlaneAuditLogID == "" {
		t.Fatal("expected machine data plane audit log id to persist")
	}
	org, err := app.GetOrgnaizationByID(owner.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	if org.DomainAuditLogID == "" {
		t.Fatal("expected organization domain audit log id to persist")
	}
}

func TestPruneFlowLogsWorkerUsesRetentionDays(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	app.cfg.FlowLogCfg = FlowLogConfig{RetentionDays: 7}
	if err := migrateFlowLogTables(app.db); err != nil {
		t.Fatalf("migrateFlowLogTables(): %v", err)
	}

	oldEntry := FlowLogEntry{
		Collection: "tailtraffic.log.tailscale.io",
		PrivateID:  "old",
		StartTime:  time.Now().UTC().AddDate(0, 0, -10),
		EndTime:    time.Now().UTC().AddDate(0, 0, -9),
		LoggedAt:   time.Now().UTC().AddDate(0, 0, -9),
	}
	newEntry := FlowLogEntry{
		Collection: "tailtraffic.log.tailscale.io",
		PrivateID:  "new",
		StartTime:  time.Now().UTC().AddDate(0, 0, -2),
		EndTime:    time.Now().UTC().AddDate(0, 0, -1),
		LoggedAt:   time.Now().UTC().AddDate(0, 0, -1),
	}
	if err := app.db.Create(&oldEntry).Error; err != nil {
		t.Fatalf("Create(oldEntry): %v", err)
	}
	if err := app.db.Create(&newEntry).Error; err != nil {
		t.Fatalf("Create(newEntry): %v", err)
	}

	if err := app.pruneFlowLogsWorker(); err != nil {
		t.Fatalf("pruneFlowLogsWorker(): %v", err)
	}

	if err := app.db.First(&FlowLogEntry{}, oldEntry.ID).Error; err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected old entry deleted, got err=%v", err)
	}
	if err := app.db.First(&FlowLogEntry{}, newEntry.ID).Error; err != nil {
		t.Fatalf("expected new entry retained: %v", err)
	}
}

func TestFlowLogCollectorAndTenantQuery(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	app.cfg.FlowLogCfg = FlowLogConfig{Enabled: true}
	if err := migrateFlowLogTables(app.db); err != nil {
		t.Fatalf("migrateFlowLogTables(): %v", err)
	}

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	machine, err := app.GetMachineByGivenName(owner.ID, "tenant-machine")
	if err != nil {
		t.Fatalf("GetMachineByGivenName(): %v", err)
	}
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

	otherUser := createTestUser(t, app, "other@example.com", "Other", "other-org", "Mirage")
	otherMachine := createTestMachine(t, app, otherUser, "other-node", "100.64.0.30")
	otherOrg, err := app.GetOrgnaizationByID(otherUser.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(other): %v", err)
	}
	otherNodeLogID, err := app.ensureMachineDataPlaneAuditLogID(otherMachine)
	if err != nil {
		t.Fatalf("ensureMachineDataPlaneAuditLogID(other): %v", err)
	}
	otherDomainLogID, err := app.ensureOrganizationDomainAuditLogID(otherOrg)
	if err != nil {
		t.Fatalf("ensureOrganizationDomainAuditLogID(other): %v", err)
	}

	router := mux.NewRouter()
	app.initRouter(router)

	start := time.Now().UTC().Add(-10 * time.Second).Round(time.Second)
	end := start.Add(5 * time.Second)
	body := marshalFlowLogUpload(t, tailcfg.StableNodeID(strconv.FormatInt(machine.ID, 10)), start, end, "100.64.0.20", "100.64.0.21")
	req := httptest.NewRequest(http.MethodPost, "/c/"+flowLogCollectionTailtraffic+"/"+nodeLogID+"?copyId="+domainLogID, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("collector status=%d body=%s", rec.Code, rec.Body.String())
	}

	otherBody := marshalFlowLogUpload(t, tailcfg.StableNodeID(strconv.FormatInt(otherMachine.ID, 10)), start, end, "100.64.0.30", "100.64.0.31")
	req = httptest.NewRequest(http.MethodPost, "/c/"+flowLogCollectionTailtraffic+"/"+otherNodeLogID+"?copyId="+otherDomainLogID, bytes.NewReader(otherBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("other collector status=%d body=%s", rec.Code, rec.Body.String())
	}

	app.controlCodeCache.Set("tenant-test-auth", ControlCacheItem{uid: tailcfg.UserID(owner.ID)}, time.Hour)
	rec = serveTenantFunnel(t, app, http.MethodGet, "/admin/api/flow-logs?limit=10", "tenant-test-auth", nil, nil, app.CAPIGetFlowLogs)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant query status=%d body=%s", rec.Code, rec.Body.String())
	}
	status, data := funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("tenant query status=%s body=%s", status, rec.Body.String())
	}
	entries := data["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 tenant entry, got %d", len(entries))
	}
	entry := entries[0].(map[string]any)
	machineData := entry["machine"].(map[string]any)
	if got := int64(machineData["id"].(float64)); got != machine.ID {
		t.Fatalf("machine id = %d, want %d", got, machine.ID)
	}
	if !entry["hasPhysicalTraffic"].(bool) {
		t.Fatal("expected physical traffic summary")
	}

	rec = serveTenantFunnel(t, app, http.MethodGet, "/admin/api/flow-logs/summary?bucket=hour", "tenant-test-auth", nil, nil, app.CAPIGetFlowLogSummary)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant summary status=%d body=%s", rec.Code, rec.Body.String())
	}
	status, data = funnelTenantResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("tenant summary status=%s body=%s", status, rec.Body.String())
	}
	summary := data["summary"].(map[string]any)
	if got := int(summary["entryCount"].(float64)); got != 1 {
		t.Fatalf("summary entryCount=%d want 1", got)
	}
	if summary["bucketMode"] != flowLogBucketHour {
		t.Fatalf("bucketMode=%v", summary["bucketMode"])
	}
	virtualTraffic := summary["virtualTraffic"].(map[string]any)
	if got := uint64(virtualTraffic["txBytes"].(float64)); got != 300 {
		t.Fatalf("virtual txBytes=%d want 300", got)
	}
	buckets := summary["buckets"].([]any)
	if len(buckets) != 1 {
		t.Fatalf("expected 1 summary bucket, got %d", len(buckets))
	}
}

func TestCockpitFlowLogConfigAPI(t *testing.T) {
	t.Parallel()

	cockpit := newFunnelTestCockpit(t)
	router := cockpit.createRouter()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodGet, "/cockpit/api/flow-logs/config", nil))
	status, data := decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected status: %s", status)
	}
	config := data["config"].(map[string]any)
	if config["enabled"].(bool) {
		t.Fatal("flow logs should default to disabled")
	}
	if got := int(config["retentionDays"].(float64)); got != flowLogDefaultRetentionDays {
		t.Fatalf("retentionDays=%d want %d", got, flowLogDefaultRetentionDays)
	}
	if !strings.Contains(data["logTargetHint"].(string), "http://") {
		t.Fatalf("unexpected logTargetHint: %#v", data["logTargetHint"])
	}

	rec = httptest.NewRecorder()
	body := []byte(`{"enabled":true,"logExitFlows":true,"retentionDays":14}`)
	router.ServeHTTP(rec, funnelAuthedRequest(http.MethodPost, "/cockpit/api/flow-logs/config", body))
	status, data = decodeFunnelAPIResponse(t, rec.Body.Bytes())
	if status != "success" {
		t.Fatalf("unexpected status after update: %s", status)
	}
	config = data["config"].(map[string]any)
	if !config["enabled"].(bool) || !config["logExitFlows"].(bool) {
		t.Fatalf("unexpected updated config: %#v", config)
	}
	if got := int(config["retentionDays"].(float64)); got != 14 {
		t.Fatalf("retentionDays=%d want 14", got)
	}
}

func marshalFlowLogUpload(t *testing.T, nodeID tailcfg.StableNodeID, start, end time.Time, src, dst string) []byte {
	t.Helper()

	entry := struct {
		Logtail struct {
			ClientTime time.Time `json:"client_time"`
		} `json:"logtail"`
		netlogtype.Message
	}{}
	entry.Logtail.ClientTime = end
	entry.Message = netlogtype.Message{
		NodeID: nodeID,
		Start:  start,
		End:    end,
		VirtualTraffic: []netlogtype.ConnectionCounts{
			{
				Connection: netlogtype.Connection{
					Proto: ipproto.TCP,
					Src:   netip.MustParseAddrPort(src + ":1234"),
					Dst:   netip.MustParseAddrPort(dst + ":443"),
				},
				Counts: netlogtype.Counts{
					TxPackets: 3,
					TxBytes:   300,
					RxPackets: 2,
					RxBytes:   200,
				},
			},
		},
		PhysicalTraffic: []netlogtype.ConnectionCounts{
			{
				Connection: netlogtype.Connection{
					Proto: ipproto.UDP,
					Src:   netip.MustParseAddrPort(src + ":0"),
					Dst:   netip.MustParseAddrPort("198.51.100.5:41641"),
				},
				Counts: netlogtype.Counts{
					TxPackets: 1,
					TxBytes:   80,
				},
			},
		},
	}
	body, err := json.Marshal([]any{entry})
	if err != nil {
		t.Fatalf("json.Marshal(): %v", err)
	}
	return body
}
