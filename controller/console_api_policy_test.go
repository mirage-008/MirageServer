package controller

import (
	"net/netip"
	"strings"
	"testing"
)

func TestParseACLPolicyDocumentSupportsHUJSON(t *testing.T) {
	t.Parallel()

	raw := `{
// comment
"groups": {
  "dev": ["alice@example.com",],
},
"hosts": {
  "db": "100.64.0.10",
},
}`

	policy, err := parseACLPolicyDocument(raw)
	if err != nil {
		t.Fatalf("parseACLPolicyDocument() error = %v", err)
	}
	if got := policy.Groups["dev"]; len(got) != 1 || got[0] != "alice@example.com" {
		t.Fatalf("unexpected groups: %#v", policy.Groups)
	}
	if got := policy.Hosts["db"].String(); got != "100.64.0.10/32" {
		t.Fatalf("unexpected host prefix: %s", got)
	}
}

func TestNormalizeACLPolicyDocumentCanonicalizesCollections(t *testing.T) {
	t.Parallel()

	policy, err := normalizeACLPolicyDocument(ACLPolicy{
		Groups: Groups{
			" Dev ": []string{" bob@example.com ", "alice@example.com", "alice@example.com"},
		},
		Hosts: Hosts{
			" DB ": mustPolicyPrefix(t, "100.64.0.10/32"),
		},
		TagOwners: TagOwners{
			" Tag:Prod ": []string{" group:Dev ", "alice@example.com", "alice@example.com"},
		},
		ACLs: []ACL{{
			Action:       " ACCEPT ",
			Protocol:     " TCP ",
			Sources:      []string{" alice@example.com "},
			Destinations: []string{" tag:prod:443 "},
		}},
		AutoApprovers: AutoApprovers{
			Routes: map[string][]string{
				"10.0.0.1/24": {" group:dev ", "alice@example.com", "alice@example.com"},
			},
			ExitNode: []string{" alice@example.com ", "alice@example.com"},
		},
		SSHs: []SSH{{
			Action:       " CHECK ",
			Sources:      []string{" alice@example.com "},
			Destinations: []string{" tag:prod "},
			Users:        []string{" root "},
			CheckPeriod:  " 1h ",
		}},
	})
	if err != nil {
		t.Fatalf("normalizeACLPolicyDocument() error = %v", err)
	}

	if _, ok := policy.Groups["group:dev"]; !ok {
		t.Fatalf("expected normalized group name, got %#v", policy.Groups)
	}
	if _, ok := policy.Hosts["db"]; !ok {
		t.Fatalf("expected normalized host name, got %#v", policy.Hosts)
	}
	if owners := policy.TagOwners["tag:prod"]; len(owners) != 2 || owners[0] != "alice@example.com" || owners[1] != "group:dev" {
		t.Fatalf("unexpected tag owners: %#v", policy.TagOwners["tag:prod"])
	}
	if routeOwners := policy.AutoApprovers.Routes["10.0.0.0/24"]; len(routeOwners) != 2 {
		t.Fatalf("unexpected route approvers: %#v", routeOwners)
	}
	if len(policy.AutoApprovers.ExitNode) != 1 || policy.AutoApprovers.ExitNode[0] != "alice@example.com" {
		t.Fatalf("unexpected exit node approvers: %#v", policy.AutoApprovers.ExitNode)
	}
	if len(policy.ACLs) != 1 || policy.ACLs[0].Action != "accept" || policy.ACLs[0].Protocol != "tcp" {
		t.Fatalf("unexpected ACL rules: %#v", policy.ACLs)
	}
	if len(policy.SSHs) != 1 || policy.SSHs[0].Action != "check" || policy.SSHs[0].CheckPeriod != "1h" {
		t.Fatalf("unexpected SSH rules: %#v", policy.SSHs)
	}
}

func TestRenderACLPolicyDocumentUsesEmptyCollections(t *testing.T) {
	t.Parallel()

	raw, err := renderACLPolicyDocument(emptyACLPolicy())
	if err != nil {
		t.Fatalf("renderACLPolicyDocument() error = %v", err)
	}
	for _, want := range []string{
		`"groups": {}`,
		`"hosts": {}`,
		`"tagOwners": {}`,
		`"acls": []`,
		`"tests": []`,
		`"routes": {}`,
		`"exitNode": []`,
		`"ssh": []`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("rendered policy missing %s: %s", want, raw)
		}
	}
}

func mustPolicyPrefix(t *testing.T, raw string) netip.Prefix {
	t.Helper()
	prefix, err := normalizeACLHostPrefix(raw)
	if err != nil {
		t.Fatalf("normalizeACLHostPrefix(%q): %v", raw, err)
	}
	return prefix
}
