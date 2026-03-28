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
	t        *testing.T
	zoneID   int64
	zoneName string
	minTTL   string
	records  map[string]dnsMgrRecordItem
	nextID   int64
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
		s.writeJSON(w, dnsMgrDomainListResponse{
			Total: 1,
			Rows: []dnsMgrDomainRow{{
				ID:   s.zoneID,
				Name: s.zoneName,
			}},
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
