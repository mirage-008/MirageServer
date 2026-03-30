package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type dnsMgrTestState struct {
	t                     *testing.T
	zoneID                int64
	zoneName              string
	minTTL                string
	records               map[string]dnsMgrRecordItem
	nextID                int64
	recordNotFoundOnEmpty bool
}

func newDNSMgrTestServer(t *testing.T) (*dnsMgrTestState, *httptest.Server) {
	t.Helper()
	state := &dnsMgrTestState{
		t:        t,
		zoneID:   1,
		zoneName: defaultFunnelDNSMgrBaseDomain,
		minTTL:   "60",
		records:  make(map[string]dnsMgrRecordItem),
		nextID:   100,
	}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	return state, server
}

func (s *dnsMgrTestState) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.t.Helper()
	if err := r.ParseForm(); err != nil {
		s.t.Fatalf("ParseForm(): %v", err)
	}
	if got := r.Form.Get("uid"); got != "1000" {
		s.t.Fatalf("uid = %q", got)
	}
	timestamp, err := strconv.ParseInt(r.Form.Get("timestamp"), 10, 64)
	if err != nil {
		s.t.Fatalf("timestamp parse: %v", err)
	}
	if got, want := r.Form.Get("sign"), dnsMgrAPISign(1000, timestamp, "secret"); got != want {
		s.t.Fatalf("sign = %q, want %q", got, want)
	}

	switch {
	case r.URL.Path == "/api/domain":
		kw := strings.TrimSpace(r.Form.Get("kw"))
		rows := []dnsMgrDomainRow{}
		if kw == "" || strings.EqualFold(kw, s.zoneName) {
			rows = append(rows, dnsMgrDomainRow{
				ID:   s.zoneID,
				Name: s.zoneName,
			})
		}
		s.writeJSON(w, dnsMgrDomainListResponse{
			Total: len(rows),
			Rows:  rows,
		})
	case r.URL.Path == "/api/domain/1":
		s.writeJSON(w, dnsMgrDomainInfoResponse{
			Data: dnsMgrDomainInfo{
				ID:   s.zoneID,
				Name: s.zoneName,
				RecordLine: []dnsMgrRecordLine{{
					ID:   0,
					Name: "默认",
				}},
				MinTTL: s.minTTL,
			},
		})
	case r.URL.Path == "/api/record/data/1":
		subdomain := strings.TrimSpace(r.Form.Get("subdomain"))
		rows := make([]dnsMgrRecordItem, 0, len(s.records))
		for _, record := range s.records {
			if subdomain != "" && !strings.EqualFold(record.Name, subdomain) {
				continue
			}
			rows = append(rows, record)
		}
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].RecordID < rows[j].RecordID
		})
		if len(rows) == 0 && s.recordNotFoundOnEmpty {
			s.writeJSON(w, dnsMgrRecordListResponse{
				dnsMgrAPIEnvelope: dnsMgrAPIEnvelope{
					Code: 1,
					Msg:  "record not found",
				},
			})
			return
		}
		s.writeJSON(w, dnsMgrRecordListResponse{
			Total: len(rows),
			Rows:  rows,
		})
	case r.URL.Path == "/api/record/add/1":
		recordID := strconv.FormatInt(s.nextID, 10)
		s.nextID++
		s.records[recordID] = dnsMgrRecordItem{
			RecordID: recordID,
			Domain:   s.zoneName,
			Name:     r.Form.Get("name"),
			Type:     r.Form.Get("type"),
			Value:    r.Form.Get("value"),
			Line:     r.Form.Get("line"),
			Status:   "1",
			TTL:      mustAtoi(s.t, r.Form.Get("ttl")),
			Remark:   r.Form.Get("remark"),
		}
		s.writeJSON(w, dnsMgrMutationResponse{})
	case r.URL.Path == "/api/record/update/1":
		recordID := r.Form.Get("recordid")
		record := s.records[recordID]
		record.RecordID = recordID
		record.Domain = s.zoneName
		record.Name = r.Form.Get("name")
		record.Type = r.Form.Get("type")
		record.Value = r.Form.Get("value")
		record.Line = r.Form.Get("line")
		record.TTL = mustAtoi(s.t, r.Form.Get("ttl"))
		record.Remark = r.Form.Get("remark")
		if record.Status == "" {
			record.Status = "1"
		}
		s.records[recordID] = record
		s.writeJSON(w, dnsMgrMutationResponse{})
	case r.URL.Path == "/api/record/status/1":
		recordID := r.Form.Get("recordid")
		record := s.records[recordID]
		record.Status = r.Form.Get("status")
		s.records[recordID] = record
		s.writeJSON(w, dnsMgrMutationResponse{})
	case r.URL.Path == "/api/record/delete/1":
		delete(s.records, r.Form.Get("recordid"))
		s.writeJSON(w, dnsMgrMutationResponse{})
	default:
		http.NotFound(w, r)
	}
}

func (s *dnsMgrTestState) writeJSON(w http.ResponseWriter, body any) {
	s.t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.t.Fatalf("json.NewEncoder(): %v", err)
	}
}

func mustAtoi(t *testing.T, raw string) int {
	t.Helper()
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("strconv.Atoi(%q): %v", raw, err)
	}
	return v
}

func TestDNSMgrManagedFunnelDNSProviderEnsureAndDelete(t *testing.T) {
	t.Parallel()

	state, server := newDNSMgrTestServer(t)
	defer server.Close()

	provider, err := newManagedFunnelDNSProvider(FunnelPlatformConfig{
		ManagedBaseDomain:    defaultFunnelDNSMgrBaseDomain,
		ManagedDNSProvider:   FunnelManagedDNSProviderDNSMgr,
		ManagedDNSAPIBaseURL: server.URL,
		ManagedDNSUID:        1000,
		ManagedDNSAPIKey:     "secret",
	})
	if err != nil {
		t.Fatalf("newManagedFunnelDNSProvider(): %v", err)
	}

	fqdn := "machine-1-org." + defaultFunnelDNSMgrBaseDomain
	if err := provider.EnsureManagedDomain(context.Background(), fqdn); err != nil {
		t.Fatalf("EnsureManagedDomain(): %v", err)
	}
	if len(state.records) != 1 {
		t.Fatalf("record count = %d", len(state.records))
	}

	var record dnsMgrRecordItem
	for _, item := range state.records {
		record = item
	}
	if record.Name != "machine-1-org" {
		t.Fatalf("record name = %q", record.Name)
	}
	if record.Type != dnsMgrManagedRecordType {
		t.Fatalf("record type = %q", record.Type)
	}
	if record.Value != defaultFunnelDNSMgrBaseDomain {
		t.Fatalf("record value = %q", record.Value)
	}

	record.Value = "stale.example.test"
	record.Status = "0"
	state.records[record.RecordID] = record
	state.records["999"] = dnsMgrRecordItem{
		RecordID: "999",
		Domain:   state.zoneName,
		Name:     record.Name,
		Type:     dnsMgrManagedRecordType,
		Value:    "extra.example.test",
		Status:   "1",
		TTL:      60,
	}
	if err := provider.EnsureManagedDomain(context.Background(), fqdn); err != nil {
		t.Fatalf("EnsureManagedDomain(reconcile): %v", err)
	}
	if len(state.records) != 1 {
		t.Fatalf("record count after reconcile = %d", len(state.records))
	}
	record = state.records[record.RecordID]
	if record.Value != defaultFunnelDNSMgrBaseDomain {
		t.Fatalf("record value after reconcile = %q", record.Value)
	}
	if record.Status != "1" {
		t.Fatalf("record status after reconcile = %q", record.Status)
	}

	if err := provider.DeleteManagedDomain(context.Background(), fqdn); err != nil {
		t.Fatalf("DeleteManagedDomain(): %v", err)
	}
	if len(state.records) != 0 {
		t.Fatalf("record count after delete = %d", len(state.records))
	}
}

func TestDNSMgrRecordItemAcceptsNumericRecordID(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"RecordId":123456,"Domain":"mirage.mm.md","Name":"demo","Type":"CNAME","Value":"mirage.mm.md","Line":"默认","Status":"1","TTL":60,"Remark":"Mirage Funnel managed"}`)

	var item dnsMgrRecordItem
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("json.Unmarshal(): %v", err)
	}
	if item.RecordID != "123456" {
		t.Fatalf("RecordID = %q, want %q", item.RecordID, "123456")
	}
	if item.Name != "demo" {
		t.Fatalf("Name = %q", item.Name)
	}
}

func TestDNSMgrRecordNameForDomain(t *testing.T) {
	t.Parallel()

	got, err := dnsMgrRecordNameForDomain("node-1.mirage.mm.md", "mirage.mm.md")
	if err != nil {
		t.Fatalf("dnsMgrRecordNameForDomain(): %v", err)
	}
	if got != "node-1" {
		t.Fatalf("record name = %q", got)
	}
}

func TestDNSMgrManagedFunnelDNSProviderLookup(t *testing.T) {
	t.Parallel()

	state, server := newDNSMgrTestServer(t)
	defer server.Close()

	provider, err := newManagedFunnelDNSProvider(FunnelPlatformConfig{
		ManagedBaseDomain:    defaultFunnelDNSMgrBaseDomain,
		ManagedDNSProvider:   FunnelManagedDNSProviderDNSMgr,
		ManagedDNSAPIBaseURL: server.URL,
		ManagedDNSUID:        1000,
		ManagedDNSAPIKey:     "secret",
	})
	if err != nil {
		t.Fatalf("newManagedFunnelDNSProvider(): %v", err)
	}

	fqdn := "machine-2-org." + defaultFunnelDNSMgrBaseDomain
	result, err := provider.LookupManagedDomain(context.Background(), fqdn)
	if err != nil {
		t.Fatalf("LookupManagedDomain(): %v", err)
	}
	if result.Ready {
		t.Fatal("expected missing record to be not ready")
	}

	state.records["200"] = dnsMgrRecordItem{
		RecordID: "200",
		Domain:   state.zoneName,
		Name:     "machine-2-org",
		Type:     dnsMgrManagedRecordType,
		Value:    defaultFunnelDNSMgrBaseDomain,
		Status:   "0",
		TTL:      60,
	}
	result, err = provider.LookupManagedDomain(context.Background(), fqdn)
	if err != nil {
		t.Fatalf("LookupManagedDomain(paused): %v", err)
	}
	if result.Ready || !strings.Contains(result.Message, "暂停") {
		t.Fatalf("paused lookup result = %#v", result)
	}

	state.records["200"] = dnsMgrRecordItem{
		RecordID: "200",
		Domain:   state.zoneName,
		Name:     "machine-2-org",
		Type:     dnsMgrManagedRecordType,
		Value:    defaultFunnelDNSMgrBaseDomain,
		Status:   "1",
		TTL:      60,
	}
	result, err = provider.LookupManagedDomain(context.Background(), fqdn)
	if err != nil {
		t.Fatalf("LookupManagedDomain(ready): %v", err)
	}
	if !result.Ready {
		t.Fatalf("ready lookup result = %#v", result)
	}
}

func TestDNSMgrManagedFunnelDNSProviderLookupAcceptsTrailingDotTarget(t *testing.T) {
	t.Parallel()

	state, server := newDNSMgrTestServer(t)
	defer server.Close()

	provider, err := newManagedFunnelDNSProvider(FunnelPlatformConfig{
		ManagedBaseDomain:    defaultFunnelDNSMgrBaseDomain,
		ManagedDNSProvider:   FunnelManagedDNSProviderDNSMgr,
		ManagedDNSAPIBaseURL: server.URL,
		ManagedDNSUID:        1000,
		ManagedDNSAPIKey:     "secret",
	})
	if err != nil {
		t.Fatalf("newManagedFunnelDNSProvider(): %v", err)
	}

	fqdn := "machine-2-org." + defaultFunnelDNSMgrBaseDomain
	state.records["200"] = dnsMgrRecordItem{
		RecordID: "200",
		Domain:   state.zoneName,
		Name:     "machine-2-org",
		Type:     dnsMgrManagedRecordType,
		Value:    defaultFunnelDNSMgrBaseDomain + ".",
		Status:   "1",
		TTL:      60,
	}

	result, err := provider.LookupManagedDomain(context.Background(), fqdn)
	if err != nil {
		t.Fatalf("LookupManagedDomain(trailing dot): %v", err)
	}
	if !result.Ready {
		t.Fatalf("trailing-dot lookup result = %#v", result)
	}

	if err := provider.DeleteManagedDomain(context.Background(), fqdn); err != nil {
		t.Fatalf("DeleteManagedDomain(trailing dot): %v", err)
	}
	if len(state.records) != 0 {
		t.Fatalf("record count after delete = %d, want 0", len(state.records))
	}
}

func TestDNSMgrManagedFunnelDNSProviderLookupTreatsRecordNotFoundAsNotReady(t *testing.T) {
	t.Parallel()

	state, server := newDNSMgrTestServer(t)
	state.recordNotFoundOnEmpty = true
	defer server.Close()

	provider, err := newManagedFunnelDNSProvider(FunnelPlatformConfig{
		ManagedBaseDomain:    defaultFunnelDNSMgrBaseDomain,
		ManagedDNSProvider:   FunnelManagedDNSProviderDNSMgr,
		ManagedDNSAPIBaseURL: server.URL,
		ManagedDNSUID:        1000,
		ManagedDNSAPIKey:     "secret",
	})
	if err != nil {
		t.Fatalf("newManagedFunnelDNSProvider(): %v", err)
	}

	result, err := provider.LookupManagedDomain(context.Background(), "machine-404-org."+defaultFunnelDNSMgrBaseDomain)
	if err != nil {
		t.Fatalf("LookupManagedDomain(): %v", err)
	}
	if result.Ready {
		t.Fatal("expected record not found to be treated as not ready")
	}
	if !strings.Contains(result.Message, "未找到托管 DNS 记录") {
		t.Fatalf("lookup message = %q", result.Message)
	}
}

func TestDNSMgrManagedFunnelDNSProviderUpsertTXTRecord(t *testing.T) {
	t.Parallel()

	state, server := newDNSMgrTestServer(t)
	state.zoneName = "mira.test"
	defer server.Close()

	provider, err := newManagedFunnelDNSProvider(FunnelPlatformConfig{
		ManagedBaseDomain:    defaultFunnelDNSMgrBaseDomain,
		ManagedDNSProvider:   FunnelManagedDNSProviderDNSMgr,
		ManagedDNSAPIBaseURL: server.URL,
		ManagedDNSUID:        1000,
		ManagedDNSAPIKey:     "secret",
	})
	if err != nil {
		t.Fatalf("newManagedFunnelDNSProvider(): %v", err)
	}

	txtProvider, ok := provider.(managedFunnelDNSChallengeProvider)
	if !ok {
		t.Fatalf("provider type %T does not implement managedFunnelDNSChallengeProvider", provider)
	}

	fqdn := "_acme-challenge.tenant-machine.tenant-org.mira.test"
	if err := txtProvider.UpsertTXTRecord(context.Background(), fqdn, "challenge-token-1"); err != nil {
		t.Fatalf("UpsertTXTRecord(create): %v", err)
	}
	if len(state.records) != 1 {
		t.Fatalf("record count = %d, want 1", len(state.records))
	}

	var record dnsMgrRecordItem
	for _, item := range state.records {
		record = item
	}
	if record.Name != "_acme-challenge.tenant-machine.tenant-org" {
		t.Fatalf("record name = %q", record.Name)
	}
	if record.Type != dnsMgrACMERecordType {
		t.Fatalf("record type = %q, want %q", record.Type, dnsMgrACMERecordType)
	}
	if record.Value != "challenge-token-1" {
		t.Fatalf("record value = %q", record.Value)
	}

	record.Status = "0"
	record.Remark = "stale"
	state.records[record.RecordID] = record
	state.records["999"] = dnsMgrRecordItem{
		RecordID: "999",
		Domain:   state.zoneName,
		Name:     record.Name,
		Type:     dnsMgrACMERecordType,
		Value:    "old-value",
		Status:   "1",
		TTL:      60,
		Remark:   dnsMgrACMERecordRemark,
	}
	if err := txtProvider.UpsertTXTRecord(context.Background(), fqdn, "challenge-token-2"); err != nil {
		t.Fatalf("UpsertTXTRecord(reconcile): %v", err)
	}
	if len(state.records) != 1 {
		t.Fatalf("record count after reconcile = %d, want 1", len(state.records))
	}
	record = state.records[record.RecordID]
	if record.Value != "challenge-token-2" {
		t.Fatalf("record value after reconcile = %q", record.Value)
	}
	if record.Status != "1" {
		t.Fatalf("record status after reconcile = %q", record.Status)
	}
	if record.Remark != dnsMgrACMERecordRemark {
		t.Fatalf("record remark after reconcile = %q", record.Remark)
	}
}

func TestDNSMgrManagedFunnelDNSProviderResolvesManagedZoneFromDomain(t *testing.T) {
	t.Parallel()

	state, server := newDNSMgrTestServer(t)
	state.zoneName = "tenant-org.mira.test"
	defer server.Close()

	provider, err := newManagedFunnelDNSProvider(FunnelPlatformConfig{
		ManagedBaseDomain:    defaultFunnelDNSMgrBaseDomain,
		ManagedDNSProvider:   FunnelManagedDNSProviderDNSMgr,
		ManagedDNSAPIBaseURL: server.URL,
		ManagedDNSUID:        1000,
		ManagedDNSAPIKey:     "secret",
	})
	if err != nil {
		t.Fatalf("newManagedFunnelDNSProvider(): %v", err)
	}

	fqdn := "tenant-machine.tenant-org.mira.test"
	if err := provider.EnsureManagedDomain(context.Background(), fqdn); err != nil {
		t.Fatalf("EnsureManagedDomain(): %v", err)
	}
	if len(state.records) != 1 {
		t.Fatalf("record count = %d, want 1", len(state.records))
	}
	for _, record := range state.records {
		if record.Domain != state.zoneName {
			t.Fatalf("record domain = %q, want %q", record.Domain, state.zoneName)
		}
		if record.Name != "tenant-machine" {
			t.Fatalf("record name = %q, want %q", record.Name, "tenant-machine")
		}
	}
}
