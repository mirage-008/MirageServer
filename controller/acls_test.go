package controller

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
)

func TestExpandAliasWildcardUsesTailnetRanges(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}

	got, err := h.expandAlias(false, nil, 0, ACLPolicy{}, "*", false)
	if err != nil {
		t.Fatalf("expandAlias returned error: %v", err)
	}

	want := []string{
		tsaddr.CGNATRange().String(),
		tsaddr.TailscaleULARange().String(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected wildcard expansion: got %v want %v", got, want)
	}
}

func TestExpandAliasAutogroupMemberSkipsTaggedMachines(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := []Machine{
		{
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.1")},
		},
		{
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.2")},
			ForcedTags:  StringList{"tag:server"},
		},
	}

	got, err := h.expandAlias(false, machines, 0, ACLPolicy{}, AutoGroupMember, false)
	if err != nil {
		t.Fatalf("expandAlias returned error: %v", err)
	}

	want := []string{"100.64.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected autogroup:member expansion: got %v want %v", got, want)
	}
}

func TestGenerateACLRulesRejectsInvalidAutogroupSelfSource(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	policy := ACLPolicy{
		ACLs: []ACL{{
			Action:       "accept",
			Sources:      []string{"tag:client"},
			Destinations: []string{"autogroup:self:*"},
		}},
	}

	_, _, err := h.generateACLRules(nil, &User{Name: "alice"}, policy, false)
	if !errors.Is(err, errInvalidAutoGroupSelfSource) {
		t.Fatalf("expected autogroup:self validation error, got %v", err)
	}
}

func TestGenerateACLRulesSkipsAutogroupInternetOnlyRule(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	policy := ACLPolicy{
		ACLs: []ACL{{
			Action:       "accept",
			Sources:      []string{"*"},
			Destinations: []string{"autogroup:internet:*"},
		}},
	}

	got, _, err := h.generateACLRules(nil, &User{}, policy, false)
	if err != nil {
		t.Fatalf("generateACLRules returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no filter rules for autogroup:internet, got %v", got)
	}
}

func TestGenerateACLRulesKeepsNonInternetDestinations(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	policy := ACLPolicy{
		Hosts: Hosts{
			"internal": mustPrefix(t, "100.64.0.10/32"),
		},
		ACLs: []ACL{{
			Action:       "accept",
			Sources:      []string{"*"},
			Destinations: []string{"autogroup:internet:*", "internal:443"},
		}},
	}

	got, _, err := h.generateACLRules(nil, &User{}, policy, false)
	if err != nil {
		t.Fatalf("generateACLRules returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one filter rule, got %d", len(got))
	}

	want := tailcfg.FilterRule{
		SrcIPs: []string{
			tsaddr.CGNATRange().String(),
			tsaddr.TailscaleULARange().String(),
		},
		DstPorts: []tailcfg.NetPortRange{{
			IP: "100.64.0.10/32",
			Ports: tailcfg.PortRange{
				First: 443,
				Last:  443,
			},
		}},
	}

	if !reflect.DeepEqual(got[0].SrcIPs, want.SrcIPs) {
		t.Fatalf("unexpected source IPs: got %v want %v", got[0].SrcIPs, want.SrcIPs)
	}
	if !reflect.DeepEqual(got[0].DstPorts, want.DstPorts) {
		t.Fatalf("unexpected destination ports: got %v want %v", got[0].DstPorts, want.DstPorts)
	}
}

func TestContainsAddressesMatchesPrefixes(t *testing.T) {
	t.Parallel()

	inputs := []string{
		tsaddr.CGNATRange().String(),
		tsaddr.TailscaleULARange().String(),
	}

	if !containsAddresses(inputs, []string{"100.64.0.2"}) {
		t.Fatalf("expected IPv4 tailnet address to match wildcard-expanded prefixes")
	}
	if !containsAddresses(inputs, []string{"fd7a:115c:a1e0::2"}) {
		t.Fatalf("expected IPv6 tailnet address to match wildcard-expanded prefixes")
	}
	if containsAddresses(inputs, []string{"192.168.1.2"}) {
		t.Fatalf("did not expect RFC1918 address to match tailnet prefixes")
	}
}

func mustAddr(t *testing.T, raw string) netip.Addr {
	t.Helper()

	addr, err := netip.ParseAddr(raw)
	if err != nil {
		t.Fatalf("failed to parse addr %q: %v", raw, err)
	}

	return addr
}

func TestGenerateSSHRulesWithPolicyFiltersByDestinationHostAlias(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()

	rules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{"db"},
		Users:        []string{"root"},
	}}, &machines[1])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesNone(rules) {
		t.Fatalf("expected ssh rule to match host destination")
	}
	if !sshRuleHasPrincipals(sshRulesGet(rules, 0), []string{"100.64.0.1"}) {
		t.Fatalf("unexpected principals: %v", sshRulePrincipalList(sshRulesGet(rules, 0)))
	}
	if !sshRuleActionHasUser(sshRulesGet(rules, 0), "root") {
		t.Fatalf("expected ssh user root to be allowed")
	}
	if !sshRuleActionAccept(sshRulesGet(rules, 0)) {
		t.Fatalf("expected accept action")
	}
	if !sshRuleActionForward(sshRulesGet(rules, 0)) {
		t.Fatalf("expected local port forwarding to remain enabled")
	}
}

func TestGenerateSSHRulesWithPolicyFiltersByDestinationTagAlias(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()

	rules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{"tag:prod"},
		Users:        []string{"ubuntu"},
	}}, &machines[1])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesNone(rules) {
		t.Fatalf("expected ssh rule to match tag destination")
	}

	rules, err = h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{"tag:prod"},
		Users:        []string{"ubuntu"},
	}}, &machines[0])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesAny(rules) {
		t.Fatalf("did not expect tag destination rule to match non-tagged machine")
	}
}

func TestGenerateSSHRulesWithPolicyFiltersByDestinationGroupAlias(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()

	rules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{"group:ops"},
		Users:        []string{"ubuntu"},
	}}, &machines[2])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesNone(rules) {
		t.Fatalf("expected ssh rule to match group destination")
	}

	rules, err = h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{"group:ops"},
		Users:        []string{"ubuntu"},
	}}, &machines[1])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesAny(rules) {
		t.Fatalf("did not expect group destination rule to match non-group machine")
	}
}

func TestGenerateSSHRulesWithPolicyFiltersByDestinationUserAlias(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()

	rules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{"carol"},
		Users:        []string{"ubuntu"},
	}}, &machines[2])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesNone(rules) {
		t.Fatalf("expected ssh rule to match user destination")
	}

	rules, err = h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{"bob"},
		Users:        []string{"ubuntu"},
	}}, &machines[2])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesAny(rules) {
		t.Fatalf("did not expect user destination rule to match another user's machine")
	}
}

func TestGenerateSSHRulesWithPolicyPreservesCheckActionDuration(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()

	rules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "check",
		Sources:      []string{"alice"},
		Destinations: []string{"db"},
		Users:        []string{"root"},
		CheckPeriod:  "1h30m",
	}}, &machines[1])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesNone(rules) {
		t.Fatalf("expected ssh check rule to match target")
	}
	if got, want := sshRuleActionCheck(sshRulesGet(rules, 0)), 90*time.Minute; got != want {
		t.Fatalf("unexpected check duration: got %v want %v", got, want)
	}
	if !sshRuleActionAccept(sshRulesGet(rules, 0)) {
		t.Fatalf("expected check action to accept after approval")
	}
}

func TestGenerateSSHRulesWithPolicyDifferentTargetsReceiveDifferentRules(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()
	sshRules := []SSH{
		{
			Action:       "accept",
			Sources:      []string{"alice"},
			Destinations: []string{"db"},
			Users:        []string{"root"},
		},
		{
			Action:       "accept",
			Sources:      []string{"alice"},
			Destinations: []string{"group:ops"},
			Users:        []string{"ubuntu"},
		},
	}

	dbRules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, sshRules, &machines[1])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	opsRules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, sshRules, &machines[2])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}

	if sshRulesLength(dbRules) != 1 || !sshRuleActionHasUser(sshRulesGet(dbRules, 0), "root") {
		t.Fatalf("expected db machine to only receive db ssh rule, got %+v", dbRules)
	}
	if sshRulesLength(opsRules) != 1 || !sshRuleActionHasUser(sshRulesGet(opsRules, 0), "ubuntu") {
		t.Fatalf("expected ops machine to only receive ops ssh rule, got %+v", opsRules)
	}
}

func TestGenerateSSHRulesWithPolicyRejectsInvalidDestinationAlias(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()

	_, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{"group:missing"},
		Users:        []string{"root"},
	}}, &machines[1])
	if !errors.Is(err, errInvalidGroup) {
		t.Fatalf("expected invalid group error, got %v", err)
	}
}

func sshTestPolicy() ACLPolicy {
	return ACLPolicy{
		Groups: Groups{
			"group:ops": []string{"carol"},
		},
		Hosts: Hosts{
			"db": mustPrefixValue("100.64.0.2/32"),
		},
		TagOwners: TagOwners{
			"tag:prod": []string{"alice"},
		},
	}
}

func sshTestMachines(t *testing.T) []Machine {
	t.Helper()
	lastUpdate := time.Unix(1700000000, 0).UTC()

	alice := User{ID: 1, Name: "alice", OrganizationID: 1, Role: RoleOwner}
	bob := User{ID: 2, Name: "bob", OrganizationID: 1, Role: RoleMember}
	carol := User{ID: 3, Name: "carol", OrganizationID: 1, Role: RoleMember}

	return []Machine{
		{
			ID:                   11,
			UserID:               alice.ID,
			User:                 alice,
			GivenName:            "alice-laptop",
			Hostname:             "alice-laptop",
			IPAddresses:          MachineAddresses{mustAddr(t, "100.64.0.1")},
			LastSuccessfulUpdate: &lastUpdate,
		},
		{
			ID:                   12,
			UserID:               bob.ID,
			User:                 bob,
			GivenName:            "db-node",
			Hostname:             "db-node",
			IPAddresses:          MachineAddresses{mustAddr(t, "100.64.0.2")},
			ForcedTags:           StringList{"tag:prod"},
			LastSuccessfulUpdate: &lastUpdate,
		},
		{
			ID:                   13,
			UserID:               carol.ID,
			User:                 carol,
			GivenName:            "ops-node",
			Hostname:             "ops-node",
			IPAddresses:          MachineAddresses{mustAddr(t, "100.64.0.3")},
			LastSuccessfulUpdate: &lastUpdate,
		},
	}
}

func mustPrefixValue(raw string) netip.Prefix {
	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		panic(err)
	}
	return prefix
}

func mustPrefix(t *testing.T, raw string) netip.Prefix {
	t.Helper()

	prefix, err := netip.ParsePrefix(raw)
	if err != nil {
		t.Fatalf("failed to parse prefix %q: %v", raw, err)
	}

	return prefix
}
