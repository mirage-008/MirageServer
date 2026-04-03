package controller

import (
	"reflect"
	"testing"
)

func TestNormalizeAutoApproverAliases(t *testing.T) {
	t.Parallel()

	got := normalizeAutoApproverAliases([]string{" group:dev ", "alice", "", "alice", " autogroup:member "})
	want := []string{"alice", "autogroup:member", "group:dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected normalized aliases: got %#v want %#v", got, want)
	}
}

func TestNormalizeAutoApproverRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "masked prefix", raw: "10.0.0.7/24", want: "10.0.0.0/24"},
		{name: "ipv6 prefix", raw: "fd7a:115c:a1e0::1/48", want: "fd7a:115c:a1e0::/48"},
		{name: "invalid", raw: "not-a-prefix", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeAutoApproverRoute(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeAutoApproverRoute returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected route: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestParseAutoApproverPathSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "plain", raw: "10.0.0.0/24", want: "10.0.0.0/24"},
		{name: "escaped", raw: "10.0.0.0%2F24", want: "10.0.0.0/24"},
		{name: "empty", raw: "", wantErr: true},
		{name: "bad escape", raw: "%zz", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseAutoApproverPathSegment(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAutoApproverPathSegment returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected path segment: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestValidateAutoApproverApprovers(t *testing.T) {
	t.Parallel()

	if err := validateAutoApproverApprovers([]string{"alice"}); err != nil {
		t.Fatalf("validateAutoApproverApprovers returned error: %v", err)
	}
	if err := validateAutoApproverApprovers(nil); err == nil {
		t.Fatal("expected empty approvers to fail")
	}
}

func TestValidateAutoApproverAliases(t *testing.T) {
	t.Parallel()

	allowed := map[string]struct{}{
		"alice":         {},
		"group:dev":     {},
		"tag:web":       {},
		AutoGroupMember: {},
		AutoGroupOwner:  {},
		AutoGroupSelf:   {},
	}

	tests := []struct {
		name    string
		aliases []string
		wantErr string
	}{
		{name: "valid aliases", aliases: []string{"alice", "group:dev", "tag:web", AutoGroupMember}},
		{name: "missing group", aliases: []string{"group:ops"}, wantErr: "审批人中包含不存在的用户组: group:ops"},
		{name: "missing tag", aliases: []string{"tag:db"}, wantErr: "审批人中包含不存在的标签: tag:db"},
		{name: "unsupported autogroup", aliases: []string{"autogroup:unknown"}, wantErr: "审批人中包含不支持的自动组: autogroup:unknown"},
		{name: "unknown alias", aliases: []string{"nobody"}, wantErr: "审批人中包含未知别名: nobody"},
		{name: "cidr not allowed", aliases: []string{"10.0.0.0/24"}, wantErr: "审批人中包含未知别名: 10.0.0.0/24"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateAutoApproverAliases(tt.aliases, allowed)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateAutoApproverAliases returned error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("unexpected error: got %v want %q", err, tt.wantErr)
			}
		})
	}
}

func TestRemoveAutoApproverAlias(t *testing.T) {
	t.Parallel()

	aliases := []string{"alice", "group:dev", "tag:web"}
	got, removed := removeAutoApproverAlias(aliases, "group:dev")
	if !removed {
		t.Fatal("expected alias to be removed")
	}
	want := []string{"alice", "tag:web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected aliases after remove: got %#v want %#v", got, want)
	}

	got, removed = removeAutoApproverAlias(aliases, "missing")
	if removed {
		t.Fatal("did not expect alias to be removed")
	}
	if !reflect.DeepEqual(got, aliases) {
		t.Fatalf("unexpected aliases when nothing removed: got %#v want %#v", got, aliases)
	}
}

func TestAutoApproverRouteDataViaDisplay(t *testing.T) {
	t.Parallel()

	route := autoApproverRouteData("fd7a:115c:a1e0:b1a:0:7:c0a8:100/120", []string{"alice"})
	if !route.IsVia {
		t.Fatal("expected 4via6 route to be marked as via")
	}
	if route.ViaSiteID != 7 {
		t.Fatalf("ViaSiteID = %d, want 7", route.ViaSiteID)
	}
	if route.ViaOriginalPrefix != "192.168.1.0/24" {
		t.Fatalf("ViaOriginalPrefix = %q, want 192.168.1.0/24", route.ViaOriginalPrefix)
	}
	if route.DisplayLabel == route.Route {
		t.Fatalf("DisplayLabel should be enriched for via routes, got %q", route.DisplayLabel)
	}
}
