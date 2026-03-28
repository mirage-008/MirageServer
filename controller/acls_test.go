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

func TestExpandAliasAutogroupMemberSkipsRequestTaggedMachines(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := []Machine{
		{
			User:        User{Name: "alice"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.1")},
		},
		{
			User:        User{Name: "alice"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.2")},
			HostInfo: HostInfo{
				RequestTags: []string{"tag:prod"},
			},
		},
	}
	policy := ACLPolicy{
		TagOwners: TagOwners{
			"tag:prod": []string{"alice"},
		},
	}

	got, err := h.expandAlias(false, machines, 0, policy, AutoGroupMember, false)
	if err != nil {
		t.Fatalf("expandAlias returned error: %v", err)
	}

	want := []string{"100.64.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected autogroup:member expansion: got %v want %v", got, want)
	}
}

func TestExpandAliasAutogroupSelfSkipsRequestTaggedMachines(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	user := createTestUser(t, app, "alice@example.com", "Alice", "acl-org", "Mirage")
	untagged := createTestMachine(t, app, user, "alice-laptop", "100.64.0.1")
	tagged := createTestMachine(t, app, user, "alice-server", "100.64.0.2")
	tagged.HostInfo = HostInfo{
		Hostname:    tagged.Hostname,
		RequestTags: []string{"tag:prod"},
	}
	if err := app.db.Save(tagged).Error; err != nil {
		t.Fatalf("Save(tagged): %v", err)
	}

	policy := ACLPolicy{
		TagOwners: TagOwners{
			"tag:prod": []string{user.Name},
		},
	}

	got, err := app.expandAlias(false, nil, user.ID, policy, AutoGroupSelf, false)
	if err != nil {
		t.Fatalf("expandAlias returned error: %v", err)
	}

	want := []string{untagged.IPAddresses.ToStringSlice()[0]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected autogroup:self expansion: got %v want %v", got, want)
	}
}

func TestExpandAliasAutogroupTaggedIncludesTaggedMachines(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := []Machine{
		{
			User:        User{Name: "alice"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.1")},
		},
		{
			User:        User{Name: "alice"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.2")},
			HostInfo: HostInfo{
				RequestTags: []string{"tag:prod"},
			},
		},
		{
			User:        User{Name: "bob"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.3")},
			ForcedTags:  StringList{"tag:db"},
		},
		{
			User:        User{Name: "carol"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.4")},
			HostInfo: HostInfo{
				RequestTags: []string{"tag:prod"},
			},
		},
		{
			User:        User{Name: "dave"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.5")},
			HostInfo: HostInfo{
				RequestTags: []string{"tag:ghost"},
			},
		},
	}
	policy := ACLPolicy{
		TagOwners: TagOwners{
			"tag:prod": []string{"alice"},
			"tag:db":   []string{"bob"},
		},
	}

	got, err := h.expandAlias(false, machines, 0, policy, AutoGroupTagged, false)
	if err != nil {
		t.Fatalf("expandAlias returned error: %v", err)
	}

	want := []string{"100.64.0.2", "100.64.0.3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected autogroup:tagged expansion: got %v want %v", got, want)
	}
}

func TestGenerateACLRulesExpandsAutogroupTaggedSource(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := []Machine{
		{
			User:        User{Name: "alice"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.1")},
		},
		{
			User:        User{Name: "alice"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.2")},
			HostInfo: HostInfo{
				RequestTags: []string{"tag:prod"},
			},
		},
		{
			User:        User{Name: "bob"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.3")},
			ForcedTags:  StringList{"tag:db"},
		},
		{
			User:        User{Name: "carol"},
			IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.4")},
			HostInfo: HostInfo{
				RequestTags: []string{"tag:ghost"},
			},
		},
	}
	policy := ACLPolicy{
		TagOwners: TagOwners{
			"tag:prod": []string{"alice"},
			"tag:db":   []string{"bob"},
		},
		ACLs: []ACL{{
			Action:       "accept",
			Sources:      []string{AutoGroupTagged},
			Destinations: []string{"*:*"},
		}},
	}

	got, _, err := h.generateACLRules(machines, &User{}, policy, false)
	if err != nil {
		t.Fatalf("generateACLRules returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one filter rule, got %d", len(got))
	}

	want := tailcfg.FilterRule{
		SrcIPs: []string{"100.64.0.2", "100.64.0.3"},
		DstPorts: []tailcfg.NetPortRange{{
			IP:    "*",
			Ports: tailcfg.PortRangeAny,
		}},
	}
	if !reflect.DeepEqual(got[0].SrcIPs, want.SrcIPs) {
		t.Fatalf("unexpected source IPs: got %v want %v", got[0].SrcIPs, want.SrcIPs)
	}
	if !reflect.DeepEqual(got[0].DstPorts, want.DstPorts) {
		t.Fatalf("unexpected destination ports: got %v want %v", got[0].DstPorts, want.DstPorts)
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

func TestGenerateACLPolicyDestPreservesWildcardDestination(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}

	got, err := h.generateACLPolicyDest(nil, 0, ACLPolicy{}, "*:*", false, false)
	if err != nil {
		t.Fatalf("generateACLPolicyDest returned error: %v", err)
	}

	want := []tailcfg.NetPortRange{{
		IP:    "*",
		Ports: tailcfg.PortRangeAny,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected wildcard destination expansion: got %v want %v", got, want)
	}
}

func TestGenerateACLRulesPreservesWildcardDestination(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	policy := ACLPolicy{
		ACLs: []ACL{{
			Action:       "accept",
			Sources:      []string{"*"},
			Destinations: []string{"*:*"},
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
			IP:    "*",
			Ports: tailcfg.PortRangeAny,
		}},
	}

	if !reflect.DeepEqual(got[0].SrcIPs, want.SrcIPs) {
		t.Fatalf("unexpected source IPs: got %v want %v", got[0].SrcIPs, want.SrcIPs)
	}
	if !reflect.DeepEqual(got[0].DstPorts, want.DstPorts) {
		t.Fatalf("unexpected wildcard destination: got %v want %v", got[0].DstPorts, want.DstPorts)
	}
}

func TestExitNodeInternetAccessRequiresEnabledRoutes(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	user := createTestUser(t, app, "alice@example.com", "Alice", "acl-org", "Mirage")
	client := createTestMachine(t, app, user, "client", "100.64.0.1")
	exitNode := createTestMachine(t, app, user, "exit-node", "100.64.0.2")
	exitNode.HostInfo = HostInfo{
		Hostname:    exitNode.Hostname,
		RoutableIPs: []netip.Prefix{ExitRouteV4, ExitRouteV6},
	}
	if err := app.db.Save(exitNode).Error; err != nil {
		t.Fatalf("Save(exit node hostinfo): %v", err)
	}
	createTestRoute(t, app, exitNode, ExitRouteV4.String(), false, false)
	createTestRoute(t, app, exitNode, ExitRouteV6.String(), false, false)

	org, err := app.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	org.AclPolicy = &ACLPolicy{
		ACLs: []ACL{{
			Action:       "accept",
			Sources:      []string{user.Name},
			Destinations: []string{"autogroup:internet:*"},
		}},
	}
	if err := app.SaveACLPolicyOfOrg(org); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(): %v", err)
	}

	enableSelf, err := app.UpdateACLRulesOfOrg(org, &client.User, client)
	if err != nil {
		t.Fatalf("UpdateACLRulesOfOrg(): %v", err)
	}
	client.User.Organization = *org

	peers, _, err := app.getValidPeers(client, enableSelf)
	if err != nil {
		t.Fatalf("getValidPeers(disabled exit routes): %v", err)
	}
	for _, peer := range peers {
		if peer.Hostname == exitNode.Hostname {
			t.Fatalf("did not expect disabled exit node to be visible, got peers=%+v", peers)
		}
	}

	reducedRules, packetFilters := packetFiltersForMachine(exitNode, nil, org.AclRules)
	if len(reducedRules) != 0 {
		t.Fatalf("expected no reduced packet filter rules before exit routes are enabled, got %+v", reducedRules)
	}
	if baseRules, ok := packetFilters["base"]; !ok || len(baseRules) != 0 {
		t.Fatalf("expected empty base packet filter chunk before exit routes are enabled, got %+v", packetFilters)
	}

	if err := app.enableRoutes(exitNode, ExitRouteV4.String(), ExitRouteV6.String()); err != nil {
		t.Fatalf("enableRoutes(exit node): %v", err)
	}

	peers, _, err = app.getValidPeers(client, enableSelf)
	if err != nil {
		t.Fatalf("getValidPeers(enabled exit routes): %v", err)
	}
	visible := false
	for _, peer := range peers {
		if peer.Hostname == exitNode.Hostname {
			visible = true
			break
		}
	}
	if !visible {
		t.Fatalf("expected enabled exit node to be visible, got peers=%+v", peers)
	}

	reducedRules, packetFilters = packetFiltersForMachine(exitNode, nil, org.AclRules)
	if len(reducedRules) != 0 {
		t.Fatalf("expected autogroup:internet to keep packet filter rules empty after exit routes are enabled, got %+v", reducedRules)
	}
	if baseRules, ok := packetFilters["base"]; !ok || len(baseRules) != 0 {
		t.Fatalf("expected empty base packet filter chunk after exit routes are enabled, got %+v", packetFilters)
	}
}

func TestSubnetRouterVisibilityRequiresEnabledRoutes(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	user := createTestUser(t, app, "alice@example.com", "Alice", "acl-org", "Mirage")
	client := createTestMachine(t, app, user, "client", "100.64.0.1")
	router := createTestMachine(t, app, user, "subnet-router", "100.64.0.2")
	router.HostInfo = HostInfo{
		Hostname:    router.Hostname,
		RoutableIPs: []netip.Prefix{mustPrefix(t, "10.10.0.0/24")},
	}
	if err := app.db.Save(router).Error; err != nil {
		t.Fatalf("Save(router hostinfo): %v", err)
	}
	createTestRoute(t, app, router, "10.10.0.0/24", false, false)

	org, err := app.GetOrgnaizationByID(user.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	org.AclPolicy = &ACLPolicy{
		ACLs: []ACL{{
			Action:       "accept",
			Sources:      []string{user.Name},
			Destinations: []string{"10.10.0.5:*"},
		}},
	}
	if err := app.SaveACLPolicyOfOrg(org); err != nil {
		t.Fatalf("SaveACLPolicyOfOrg(): %v", err)
	}

	enableSelf, err := app.UpdateACLRulesOfOrg(org, &client.User, client)
	if err != nil {
		t.Fatalf("UpdateACLRulesOfOrg(): %v", err)
	}
	client.User.Organization = *org

	peers, _, err := app.getValidPeers(client, enableSelf)
	if err != nil {
		t.Fatalf("getValidPeers(disabled subnet routes): %v", err)
	}
	for _, peer := range peers {
		if peer.Hostname == router.Hostname {
			t.Fatalf("did not expect disabled subnet router to be visible, got peers=%+v", peers)
		}
	}

	if err := app.enableRoutes(router, "10.10.0.0/24"); err != nil {
		t.Fatalf("enableRoutes(subnet router): %v", err)
	}

	peers, _, err = app.getValidPeers(client, enableSelf)
	if err != nil {
		t.Fatalf("getValidPeers(enabled subnet routes): %v", err)
	}
	visible := false
	for _, peer := range peers {
		if peer.Hostname == router.Hostname {
			visible = true
			break
		}
	}
	if !visible {
		t.Fatalf("expected enabled subnet router to be visible, got peers=%+v", peers)
	}
}

func TestReduceFilterRulesKeepsSubnetDestinationsForRouter(t *testing.T) {
	t.Parallel()

	router := &Machine{
		IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.2")},
		HostInfo: HostInfo{
			RoutableIPs: []netip.Prefix{mustPrefix(t, "10.10.0.0/24")},
		},
	}
	rules := []tailcfg.FilterRule{{
		SrcIPs: []string{"100.64.0.1"},
		DstPorts: []tailcfg.NetPortRange{{
			IP:    "10.10.0.5",
			Ports: tailcfg.PortRangeAny,
		}},
	}}

	reduced := reduceFilterRulesForMachine(router, rules)
	if len(reduced) != 1 {
		t.Fatalf("expected subnet destination to survive reduction, got %+v", reduced)
	}
	if got := reduced[0].DstPorts[0].IP; got != "10.10.0.5" {
		t.Fatalf("expected reduced destination to stay 10.10.0.5, got %q", got)
	}
}

func TestReduceFilterRulesKeepsWildcardDestination(t *testing.T) {
	t.Parallel()

	machine := &Machine{
		IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.2")},
	}
	rules := []tailcfg.FilterRule{{
		SrcIPs: []string{"100.64.0.1"},
		DstPorts: []tailcfg.NetPortRange{{
			IP:    "*",
			Ports: tailcfg.PortRangeAny,
		}},
	}}

	reduced := reduceFilterRulesForMachine(machine, rules)
	if len(reduced) != 1 {
		t.Fatalf("expected wildcard destination to survive reduction, got %+v", reduced)
	}
	if got := reduced[0].DstPorts[0].IP; got != "*" {
		t.Fatalf("expected wildcard destination to stay '*', got %q", got)
	}
}

func TestExpandMachineRoutesReturnsOnlyServedRoutes(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	user := createTestUser(t, app, "alice@example.com", "Alice", "acl-org", "Mirage")
	router := createTestMachine(t, app, user, "subnet-router", "100.64.0.2")

	createTestRoute(t, app, router, "10.10.0.0/24", true, true)
	createTestRoute(t, app, router, "10.20.0.0/24", true, false)
	createTestRoute(t, app, router, "10.30.0.0/24", false, false)
	createTestRoute(t, app, router, ExitRouteV4.String(), true, false)
	createTestRoute(t, app, router, ExitRouteV6.String(), true, false)

	got := app.expandMachineRoutes(*router)
	want := []string{
		"10.10.0.0/24",
		ExitRouteV4.String(),
		ExitRouteV6.String(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected expanded routes: got %v want %v", got, want)
	}
}

func TestDisableRouteMarksOrganizationStateChanged(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	user := createTestUser(t, app, "alice@example.com", "Alice", "acl-org", "Mirage")
	router := createTestMachine(t, app, user, "subnet-router", "100.64.0.2")
	route := createTestRoute(t, app, router, "10.10.0.0/24", true, true)

	before := app.getOrgLastStateChange(user.OrganizationID)
	if err := app.DisableRoute(route.ID); err != nil {
		t.Fatalf("DisableRoute(): %v", err)
	}
	after := app.getOrgLastStateChange(user.OrganizationID)
	if !after.After(before) {
		t.Fatalf("expected organization state change timestamp to advance after disabling route, before=%v after=%v", before, after)
	}

	updatedRoute, err := app.GetRoute(route.ID)
	if err != nil {
		t.Fatalf("GetRoute(): %v", err)
	}
	if updatedRoute.Enabled {
		t.Fatalf("expected route to be disabled, got %+v", updatedRoute)
	}
	if updatedRoute.IsPrimary {
		t.Fatalf("expected disabled route to no longer be primary, got %+v", updatedRoute)
	}
}

func TestProcessMachineRoutesWithdrawalMarksOrganizationStateChanged(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	user := createTestUser(t, app, "alice@example.com", "Alice", "acl-org", "Mirage")
	router := createTestMachine(t, app, user, "subnet-router", "100.64.0.2")
	createTestRoute(t, app, router, "10.10.0.0/24", true, true)
	router.HostInfo = HostInfo{Hostname: router.Hostname}

	before := app.getOrgLastStateChange(user.OrganizationID)
	if err := app.processMachineRoutes(router); err != nil {
		t.Fatalf("processMachineRoutes(): %v", err)
	}
	after := app.getOrgLastStateChange(user.OrganizationID)
	if !after.After(before) {
		t.Fatalf("expected organization state change timestamp to advance after route withdrawal, before=%v after=%v", before, after)
	}

	machineRoutes, err := app.GetMachineRoutes(router)
	if err != nil {
		t.Fatalf("GetMachineRoutes(): %v", err)
	}
	if len(machineRoutes) != 1 {
		t.Fatalf("expected one machine route, got %d", len(machineRoutes))
	}
	if machineRoutes[0].Advertised {
		t.Fatalf("expected route advertisement to be withdrawn, got %+v", machineRoutes[0])
	}
	if machineRoutes[0].Enabled {
		t.Fatalf("expected withdrawn route to be disabled, got %+v", machineRoutes[0])
	}
}

func TestHandlePrimarySubnetFailoverMarksBothOrganizationsChanged(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	ownerA := createTestUser(t, app, "alice@example.com", "Alice", "org-a", "Mirage")
	ownerB := createTestUser(t, app, "bob@example.com", "Bob", "org-b", "Mirage")
	routerA := createTestMachine(t, app, ownerA, "router-a", "100.64.0.2")
	routerB := createTestMachine(t, app, ownerB, "router-b", "100.64.0.3")
	createTestRoute(t, app, routerA, "10.10.0.0/24", true, true)
	createTestRoute(t, app, routerB, "10.10.0.0/24", true, false)

	offline := time.Now().Add(-2 * keepAliveInterval)
	routerA.LastSeen = &offline
	if err := app.db.Save(routerA).Error; err != nil {
		t.Fatalf("Save(routerA): %v", err)
	}
	online := time.Now().UTC()
	routerB.LastSeen = &online
	if err := app.db.Save(routerB).Error; err != nil {
		t.Fatalf("Save(routerB): %v", err)
	}

	beforeA := app.getOrgLastStateChange(ownerA.OrganizationID)
	beforeB := app.getOrgLastStateChange(ownerB.OrganizationID)
	if err := app.handlePrimarySubnetFailover(); err != nil {
		t.Fatalf("handlePrimarySubnetFailover(): %v", err)
	}
	afterA := app.getOrgLastStateChange(ownerA.OrganizationID)
	afterB := app.getOrgLastStateChange(ownerB.OrganizationID)
	if !afterA.After(beforeA) {
		t.Fatalf("expected org A state change timestamp to advance, before=%v after=%v", beforeA, afterA)
	}
	if !afterB.After(beforeB) {
		t.Fatalf("expected org B state change timestamp to advance, before=%v after=%v", beforeB, afterB)
	}

	routesA, err := app.GetMachineRoutes(routerA)
	if err != nil {
		t.Fatalf("GetMachineRoutes(routerA): %v", err)
	}
	routesB, err := app.GetMachineRoutes(routerB)
	if err != nil {
		t.Fatalf("GetMachineRoutes(routerB): %v", err)
	}
	if len(routesA) != 1 || routesA[0].IsPrimary {
		t.Fatalf("expected router A to lose primary route, got %+v", routesA)
	}
	if len(routesB) != 1 || !routesB[0].IsPrimary {
		t.Fatalf("expected router B to become primary, got %+v", routesB)
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

func TestGenerateSSHRulesWithPolicyAcceptsAutogroupTaggedAlias(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()
	rule := SSH{
		Action:       "accept",
		Sources:      []string{AutoGroupTagged},
		Destinations: []string{"tag:prod"},
		Users:        []string{"root"},
	}

	if err := h.validateSSHRulesForPolicy(machines, machines[0].User.ID, policy, []SSH{rule}); err != nil {
		t.Fatalf("validateSSHRulesForPolicy returned error: %v", err)
	}

	rules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{rule}, &machines[1])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy returned error: %v", err)
	}
	if sshRulesNone(rules) {
		t.Fatalf("expected ssh rule to match autogroup:tagged source")
	}
	if !sshRuleHasPrincipals(sshRulesGet(rules, 0), []string{"100.64.0.2"}) {
		t.Fatalf("unexpected principals for autogroup:tagged source: %v", sshRulePrincipalList(sshRulesGet(rules, 0)))
	}
}

func TestGenerateSSHRulesWithPolicyRejectsAutogroupTaggedSourceToUsernameDestination(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()
	rule := SSH{
		Action:       "accept",
		Sources:      []string{AutoGroupTagged},
		Destinations: []string{"alice"},
		Users:        []string{"root"},
	}

	err := h.validateSSHRulesForPolicy(machines, machines[0].User.ID, policy, []SSH{rule})
	if err == nil {
		t.Fatalf("expected validation error for autogroup:tagged source to username destination")
	}
}

func TestGenerateSSHRulesWithPolicyRejectsAutogroupTaggedSourceToAutoGroupSelfDestination(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()
	rule := SSH{
		Action:       "accept",
		Sources:      []string{AutoGroupTagged},
		Destinations: []string{AutoGroupSelf},
		Users:        []string{"root"},
	}

	err := h.validateSSHRulesForPolicy(machines, machines[0].User.ID, policy, []SSH{rule})
	if err == nil {
		t.Fatalf("expected validation error for autogroup:tagged source to autogroup:self destination")
	}
}

func TestGenerateSSHRulesWithPolicyRejectsAutogroupTaggedSourceToAutoGroupMemberDestination(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	policy := sshTestPolicy()
	rule := SSH{
		Action:       "accept",
		Sources:      []string{AutoGroupTagged},
		Destinations: []string{AutoGroupMember},
		Users:        []string{"root"},
	}

	err := h.validateSSHRulesForPolicy(machines, machines[0].User.ID, policy, []SSH{rule})
	if err == nil {
		t.Fatalf("expected validation error for autogroup:tagged source to autogroup:member destination")
	}
}

func TestGenerateSSHRulesWithPolicyRejectsTagSourceRestrictedDestinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dst  string
	}{
		{name: "username", dst: "alice"},
		{name: "autogroup self", dst: AutoGroupSelf},
		{name: "autogroup member", dst: AutoGroupMember},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := &Mirage{cfg: &Config{}}
			machines := sshTestMachines(t)
			policy := sshTestPolicy()
			rule := SSH{
				Action:       "accept",
				Sources:      []string{"tag:prod"},
				Destinations: []string{tt.dst},
				Users:        []string{"root"},
			}

			err := h.validateSSHRulesForPolicy(machines, machines[0].User.ID, policy, []SSH{rule})
			if err == nil {
				t.Fatalf("expected validation error for tag source to destination %q", tt.dst)
			}
		})
	}
}

func TestGenerateSSHRulesWithPolicyMatchesAutogroupTaggedDestinationOnlyForTaggedTargets(t *testing.T) {
	t.Parallel()

	h := &Mirage{cfg: &Config{}}
	machines := sshTestMachines(t)
	machines = append(machines, Machine{
		ID:          14,
		UserID:      machines[0].User.ID,
		User:        machines[0].User,
		GivenName:   "tagged-node",
		Hostname:    "tagged-node",
		IPAddresses: MachineAddresses{mustAddr(t, "100.64.0.4")},
		HostInfo: HostInfo{
			RequestTags: []string{"tag:prod"},
		},
		LastSuccessfulUpdate: machines[0].LastSuccessfulUpdate,
	})
	policy := sshTestPolicy()
	rule := SSH{
		Action:       "accept",
		Sources:      []string{"alice"},
		Destinations: []string{AutoGroupTagged},
		Users:        []string{"root"},
	}

	if err := h.validateSSHRulesForPolicy(machines, machines[0].User.ID, policy, []SSH{rule}); err != nil {
		t.Fatalf("validateSSHRulesForPolicy returned error: %v", err)
	}

	rules, err := h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{rule}, &machines[3])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy(tagged target) returned error: %v", err)
	}
	if sshRulesNone(rules) {
		t.Fatalf("expected ssh rule to match autogroup:tagged destination")
	}
	if !sshRuleHasPrincipals(sshRulesGet(rules, 0), []string{"100.64.0.1"}) {
		t.Fatalf("unexpected principals for autogroup:tagged destination: %v", sshRulePrincipalList(sshRulesGet(rules, 0)))
	}

	rules, err = h.generateSSHRulesWithPolicy(machines, machines[0].User.ID, policy, []SSH{rule}, &machines[0])
	if err != nil {
		t.Fatalf("generateSSHRulesWithPolicy(untagged target) returned error: %v", err)
	}
	if sshRulesAny(rules) {
		t.Fatalf("did not expect autogroup:tagged destination to match untagged target, got %+v", rules)
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
