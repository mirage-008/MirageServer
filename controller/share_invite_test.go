package controller

import (
	"encoding/json"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/patrickmn/go-cache"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"tailscale.com/types/key"
)

func newShareInviteTestMirage(t *testing.T) *Mirage {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "share-invite-test.sqlite")
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

	serverKey := key.NewMachine()
	serverKeyText, err := serverKey.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText(serverKey): %v", err)
	}

	sysCfg := &SysConfig{
		ServerURL:  "ctrl.example.test",
		ServerKey:  string(serverKeyText),
		Addr:       "127.0.0.1:8080",
		Mip4:       mustIPPrefix(t, "100.64.0.0/10"),
		Mip6:       mustIPPrefix(t, "fd7a:115c:a1e0::/48"),
		Basedomain: "mira.test",
		DerpUrl:    defaultRemoteDERPMapURL,
		DexSecret:  "test-secret",
	}
	if err := db.Create(sysCfg).Error; err != nil {
		t.Fatalf("Create(sysCfg): %v", err)
	}

	cfg, err := sysCfg.toSrvConfig()
	if err != nil {
		t.Fatalf("toSrvConfig(): %v", err)
	}

	app, err := NewMirage(cfg, db)
	if err != nil {
		t.Fatalf("NewMirage(): %v", err)
	}
	app.aCodeCache = cache.New(0, 0)
	app.stateCodeCache = cache.New(0, 0)
	app.controlCodeCache = cache.New(0, 0)
	app.machineControlCodeCache = cache.New(0, 0)
	app.tcdCache = cache.New(0, 0)

	return app
}

func createTestUser(t *testing.T, app *Mirage, name, displayName, orgName, provider string) *User {
	t.Helper()

	user, err := app.CreateUser(name, displayName, orgName, provider)
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", name, err)
	}

	return user
}

func createTestMachine(t *testing.T, app *Mirage, user *User, hostname string, addrs ...string) *Machine {
	t.Helper()

	machineKey := key.NewMachine()
	nodeKey := key.NewNode()
	discoKey := key.NewDisco()

	machine := &Machine{
		MachineKey:     MachinePublicKeyStripPrefix(machineKey.Public()),
		NodeKey:        NodePublicKeyStripPrefix(nodeKey.Public()),
		DiscoKey:       DiscoPublicKeyStripPrefix(discoKey.Public()),
		Hostname:       hostname,
		GivenName:      hostname,
		UserID:         user.ID,
		User:           *user,
		RegisterMethod: RegisterMethodAuthKey,
		HostInfo:       HostInfo{Hostname: hostname},
		Endpoints:      StringList{"127.0.0.1:12345"},
	}
	for _, rawAddr := range addrs {
		machine.IPAddresses = append(machine.IPAddresses, mustAddr(t, rawAddr))
	}

	if err := app.db.Create(machine).Error; err != nil {
		t.Fatalf("Create(machine %q): %v", hostname, err)
	}

	return machine
}

func createTestRoute(t *testing.T, app *Mirage, machine *Machine, prefix string, enabled bool, primary bool) Route {
	t.Helper()

	parsed, err := netip.ParsePrefix(prefix)
	if err != nil {
		t.Fatalf("ParsePrefix(%q): %v", prefix, err)
	}
	route := Route{
		MachineID:  machine.ID,
		Prefix:     IPPrefix(parsed),
		Advertised: true,
		Enabled:    enabled,
		IsPrimary:  primary,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := app.db.Create(&route).Error; err != nil {
		t.Fatalf("Create(route %q): %v", prefix, err)
	}

	return route
}

func getMachineShareByID(t *testing.T, app *Mirage, shareID int64) *MachineShare {
	t.Helper()

	share := &MachineShare{}
	if err := app.db.First(share, "id = ?", shareID).Error; err != nil {
		t.Fatalf("First(machine share %d): %v", shareID, err)
	}
	return share
}

func getOrgInviteByID(t *testing.T, app *Mirage, inviteID int64) *OrgInvite {
	t.Helper()

	invite := &OrgInvite{}
	if err := app.db.First(invite, "id = ?", inviteID).Error; err != nil {
		t.Fatalf("First(org invite %d): %v", inviteID, err)
	}
	return invite
}

func TestCreateAndRevokeOrgInvite(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceOwner := createTestUser(t, app, "owner@example.com", "Owner", "source-org", "Mirage")

	invite, err := app.CreateOrgInvite(sourceOwner.OrganizationID, sourceOwner.ID, "  Invitee@Example.com ")
	if err != nil {
		t.Fatalf("CreateOrgInvite(): %v", err)
	}
	if invite.TargetIdentity != "invitee@example.com" {
		t.Fatalf("unexpected normalized invite target: %q", invite.TargetIdentity)
	}
	if invite.Status != OrgInviteStatusPending {
		t.Fatalf("unexpected invite status: %q", invite.Status)
	}

	pendingInvites, err := app.ListPendingOrgInvitesByOrgID(sourceOwner.OrganizationID)
	if err != nil {
		t.Fatalf("ListPendingOrgInvitesByOrgID(): %v", err)
	}
	if len(pendingInvites) != 1 || pendingInvites[0].ID != invite.ID {
		t.Fatalf("unexpected pending invites: %+v", pendingInvites)
	}

	if err := app.RevokeOrgInvite(invite.ID, sourceOwner.OrganizationID); err != nil {
		t.Fatalf("RevokeOrgInvite(): %v", err)
	}

	revokedInvite := getOrgInviteByID(t, app, invite.ID)
	if revokedInvite.Status != OrgInviteStatusRevoked {
		t.Fatalf("expected revoked invite status, got %q", revokedInvite.Status)
	}
	if revokedInvite.RevokedAt == nil {
		t.Fatal("expected invite revoked timestamp to be set")
	}

	pendingInvites, err = app.ListPendingOrgInvitesByOrgID(sourceOwner.OrganizationID)
	if err != nil {
		t.Fatalf("ListPendingOrgInvitesByOrgID() after revoke: %v", err)
	}
	if len(pendingInvites) != 0 {
		t.Fatalf("expected no pending invites after revoke, got %+v", pendingInvites)
	}
}

func TestFindOrCreateNewUserForOIDCCallbackUsesPendingInvite(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	owner := createTestUser(t, app, "owner@example.com", "Owner", "invited-org", "Mirage")

	invite, err := app.CreateOrgInvite(owner.OrganizationID, owner.ID, "new-user@example.com")
	if err != nil {
		t.Fatalf("CreateOrgInvite(): %v", err)
	}

	user, err := app.findOrCreateNewUserForOIDCCallback(
		"new-user@example.com",
		"New User",
		"new-user@example.com.Aggregator",
		"Aggregator",
	)
	if err != nil {
		t.Fatalf("findOrCreateNewUserForOIDCCallback(): %v", err)
	}
	if user.OrganizationID != owner.OrganizationID {
		t.Fatalf("expected invited user to join org %d, got %d", owner.OrganizationID, user.OrganizationID)
	}
	if user.Role != RoleMember {
		t.Fatalf("expected invited user role %d, got %d", RoleMember, user.Role)
	}

	acceptedInvite := getOrgInviteByID(t, app, invite.ID)
	if acceptedInvite.Status != OrgInviteStatusAccepted {
		t.Fatalf("expected accepted invite status, got %q", acceptedInvite.Status)
	}
	if acceptedInvite.AcceptedUserID != user.ID {
		t.Fatalf("expected invite accepted by user %d, got %d", user.ID, acceptedInvite.AcceptedUserID)
	}
	if acceptedInvite.AcceptedAt == nil {
		t.Fatal("expected invite accepted timestamp to be set")
	}

	storedUser, err := app.GetUser("new-user@example.com", owner.Organization.Name, owner.Organization.Provider)
	if err != nil {
		t.Fatalf("GetUser(invited user): %v", err)
	}
	if storedUser.ID != user.ID {
		t.Fatalf("expected stored user ID %d, got %d", user.ID, storedUser.ID)
	}
}

func TestCreateAcceptAndRevokeMachineShareAffectsVisibility(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")
	targetMachine := createTestMachine(t, app, targetUser, "target-node", "100.64.0.2")

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, " Target@Example.com ")
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}
	if share.TargetIdentity != "target@example.com" {
		t.Fatalf("unexpected normalized share target: %q", share.TargetIdentity)
	}
	if share.Status != MachineShareStatusPending {
		t.Fatalf("unexpected share status: %q", share.Status)
	}

	acceptedShare, err := app.AcceptMachineShareByToken(share.ShareToken, targetUser)
	if err != nil {
		t.Fatalf("AcceptMachineShareByToken(): %v", err)
	}
	if acceptedShare.Status != MachineShareStatusAccepted {
		t.Fatalf("expected accepted share status, got %q", acceptedShare.Status)
	}
	if acceptedShare.TargetOrgID != targetUser.OrganizationID {
		t.Fatalf("expected target org %d, got %d", targetUser.OrganizationID, acceptedShare.TargetOrgID)
	}

	storedShare := getMachineShareByID(t, app, share.ID)
	if storedShare.Status != MachineShareStatusAccepted {
		t.Fatalf("expected stored accepted share status, got %q", storedShare.Status)
	}
	if storedShare.AcceptedAt == nil {
		t.Fatal("expected share accepted timestamp to be set")
	}

	visibleMachines, err := app.ListVisibleMachinesByOrgID(targetUser.OrganizationID)
	if err != nil {
		t.Fatalf("ListVisibleMachinesByOrgID(): %v", err)
	}
	if len(visibleMachines) != 2 {
		t.Fatalf("expected 2 visible machines, got %d (%+v)", len(visibleMachines), visibleMachines)
	}
	visibleByID := machinesByID(visibleMachines)
	sharedMachine, ok := visibleByID[sourceMachine.ID]
	if !ok {
		t.Fatalf("expected shared source machine %d to be visible", sourceMachine.ID)
	}
	if !sharedMachine.Shared {
		t.Fatal("expected source machine to be marked as shared in target visibility")
	}
	if _, ok := visibleByID[targetMachine.ID]; !ok {
		t.Fatalf("expected target-owned machine %d to remain visible", targetMachine.ID)
	}

	visible, err := app.IsMachineVisibleToOrg(sourceMachine, targetUser.OrganizationID)
	if err != nil {
		t.Fatalf("IsMachineVisibleToOrg(): %v", err)
	}
	if !visible {
		t.Fatal("expected accepted share to make source machine visible to target org")
	}

	externalUsers, err := app.ListExternalSharedUsersByOrgID(targetUser.OrganizationID)
	if err != nil {
		t.Fatalf("ListExternalSharedUsersByOrgID(): %v", err)
	}
	if len(externalUsers) != 1 || externalUsers[0].ID != sourceUser.ID {
		t.Fatalf("unexpected external shared users: %+v", externalUsers)
	}

	acceptedCount, err := app.CountAcceptedSharesBySourceMachine(sourceMachine.ID)
	if err != nil {
		t.Fatalf("CountAcceptedSharesBySourceMachine(): %v", err)
	}
	if acceptedCount != 1 {
		t.Fatalf("expected 1 accepted share, got %d", acceptedCount)
	}
	activeCount, err := app.CountActiveSharesBySourceMachine(sourceMachine.ID)
	if err != nil {
		t.Fatalf("CountActiveSharesBySourceMachine(): %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected 1 active share, got %d", activeCount)
	}

	sharePeers, err := app.ListSharePeersForMachine(targetMachine)
	if err != nil {
		t.Fatalf("ListSharePeersForMachine(): %v", err)
	}
	if len(sharePeers) != 1 || sharePeers[0].ID != sourceMachine.ID {
		t.Fatalf("unexpected share peers for target machine: %+v", sharePeers)
	}
	if !sharePeers[0].Shared {
		t.Fatal("expected share peer to be marked as shared")
	}

	if err := app.RevokeMachineShare(share.ID, sourceUser.OrganizationID); err != nil {
		t.Fatalf("RevokeMachineShare(): %v", err)
	}

	revokedShare := getMachineShareByID(t, app, share.ID)
	if revokedShare.Status != MachineShareStatusRevoked {
		t.Fatalf("expected revoked share status, got %q", revokedShare.Status)
	}
	if revokedShare.RevokedAt == nil {
		t.Fatal("expected share revoked timestamp to be set")
	}

	visibleMachines, err = app.ListVisibleMachinesByOrgID(targetUser.OrganizationID)
	if err != nil {
		t.Fatalf("ListVisibleMachinesByOrgID() after revoke: %v", err)
	}
	if len(visibleMachines) != 1 || visibleMachines[0].ID != targetMachine.ID {
		t.Fatalf("expected only target-owned machine after revoke, got %+v", visibleMachines)
	}

	visible, err = app.IsMachineVisibleToOrg(sourceMachine, targetUser.OrganizationID)
	if err != nil {
		t.Fatalf("IsMachineVisibleToOrg() after revoke: %v", err)
	}
	if visible {
		t.Fatal("expected revoked share to remove source machine visibility from target org")
	}
}

func TestAcceptPendingMachineSharesForUserAcceptsByIdentity(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}

	acceptedShares, err := app.AcceptPendingMachineSharesForUser(targetUser)
	if err != nil {
		t.Fatalf("AcceptPendingMachineSharesForUser(): %v", err)
	}
	if len(acceptedShares) != 1 {
		t.Fatalf("expected 1 accepted share, got %d", len(acceptedShares))
	}
	if acceptedShares[0].ID != share.ID {
		t.Fatalf("expected accepted share ID %d, got %d", share.ID, acceptedShares[0].ID)
	}
	if acceptedShares[0].Status != MachineShareStatusAccepted {
		t.Fatalf("expected accepted share status, got %q", acceptedShares[0].Status)
	}

	storedShare := getMachineShareByID(t, app, share.ID)
	if storedShare.Status != MachineShareStatusAccepted {
		t.Fatalf("expected stored accepted share status, got %q", storedShare.Status)
	}
	if storedShare.TargetUserID != targetUser.ID {
		t.Fatalf("expected target user ID %d, got %d", targetUser.ID, storedShare.TargetUserID)
	}
}

func TestSharedNodeKeepsRoutesAndExitNodeCapabilities(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	machine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")
	machine.HostInfo = HostInfo{
		Hostname:    machine.Hostname,
		RoutableIPs: []netip.Prefix{mustPrefix(t, "10.10.0.0/24"), ExitRouteV4, ExitRouteV6},
	}
	if err := app.db.Save(machine).Error; err != nil {
		t.Fatalf("Save(machine hostinfo): %v", err)
	}
	createTestRoute(t, app, machine, "10.10.0.0/24", true, true)
	createTestRoute(t, app, machine, "0.0.0.0/0", true, false)
	createTestRoute(t, app, machine, "::/0", true, false)

	ownedNode, err := app.toNode(*machine, false)
	if err != nil {
		t.Fatalf("toNode(owned): %v", err)
	}
	sharedNode, err := app.toNode(*machine, true)
	if err != nil {
		t.Fatalf("toNode(shared): %v", err)
	}

	subnetPrefix := mustPrefix(t, "10.10.0.0/24")
	if !containsPrefix(ownedNode.AllowedIPs, subnetPrefix) {
		t.Fatalf("expected owned node allowed IPs to include subnet route, got %v", ownedNode.AllowedIPs)
	}
	if !containsPrefix(ownedNode.PrimaryRoutes, subnetPrefix) {
		t.Fatalf("expected owned node primary routes to include subnet route, got %v", ownedNode.PrimaryRoutes)
	}
	if !containsPrefix(ownedNode.AllowedIPs, ExitRouteV4) || !containsPrefix(ownedNode.AllowedIPs, ExitRouteV6) {
		t.Fatalf("expected owned node allowed IPs to include exit routes, got %v", ownedNode.AllowedIPs)
	}

	if !containsPrefix(sharedNode.AllowedIPs, subnetPrefix) {
		t.Fatalf("expected shared node allowed IPs to include subnet route, got %v", sharedNode.AllowedIPs)
	}
	if !containsPrefix(sharedNode.AllowedIPs, ExitRouteV4) || !containsPrefix(sharedNode.AllowedIPs, ExitRouteV6) {
		t.Fatalf("expected shared node allowed IPs to include exit routes, got %v", sharedNode.AllowedIPs)
	}
	if !containsPrefix(sharedNode.PrimaryRoutes, subnetPrefix) {
		t.Fatalf("expected shared node primary routes to include subnet route, got %v", sharedNode.PrimaryRoutes)
	}
	selfPrefix := netip.PrefixFrom(machine.IPAddresses[0], machine.IPAddresses[0].BitLen())
	if !containsPrefix(sharedNode.AllowedIPs, selfPrefix) {
		t.Fatalf("expected shared node to keep its own address prefix, got %v", sharedNode.AllowedIPs)
	}
}

func TestListSharePeersForMachineIncludesConnectedOrgMachines(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")
	sourceOrgPeer := createTestMachine(t, app, sourceUser, "source-peer", "100.64.0.10")
	targetMachineA := createTestMachine(t, app, targetUser, "target-a", "100.64.0.2")
	targetMachineB := createTestMachine(t, app, targetUser, "target-b", "100.64.0.3")

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}
	if _, err := app.AcceptMachineShareByToken(share.ShareToken, targetUser); err != nil {
		t.Fatalf("AcceptMachineShareByToken(): %v", err)
	}

	sourcePeers, err := app.ListSharePeersForMachine(sourceMachine)
	if err != nil {
		t.Fatalf("ListSharePeersForMachine(source): %v", err)
	}
	if len(sourcePeers) != 2 {
		t.Fatalf("expected 2 connected target peers, got %d (%+v)", len(sourcePeers), sourcePeers)
	}
	for _, peer := range sourcePeers {
		if peer.Shared {
			t.Fatalf("expected connected target peers to stay owned, got shared peer %+v", peer)
		}
	}

	sourceOrgPeers, err := app.ListSharePeersForMachine(sourceOrgPeer)
	if err != nil {
		t.Fatalf("ListSharePeersForMachine(source org peer): %v", err)
	}
	if len(sourceOrgPeers) != 2 {
		t.Fatalf("expected 2 connected target peers for source org peer, got %d (%+v)", len(sourceOrgPeers), sourceOrgPeers)
	}
	for _, peer := range sourceOrgPeers {
		if peer.Shared {
			t.Fatalf("expected source org peer to see connected target peers as owned, got shared peer %+v", peer)
		}
	}

	targetPeers, err := app.ListSharePeersForMachine(targetMachineA)
	if err != nil {
		t.Fatalf("ListSharePeersForMachine(target): %v", err)
	}
	if len(targetPeers) != 1 || targetPeers[0].ID != sourceMachine.ID {
		t.Fatalf("expected one shared source peer for target machine, got %+v", targetPeers)
	}
	if !targetPeers[0].Shared {
		t.Fatal("expected shared source peer to be marked shared")
	}
	_ = targetMachineB
}

func TestGetPeersWithACLIncludesAcceptedSharedMachine(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")
	sourceOrgPeer := createTestMachine(t, app, sourceUser, "source-peer", "100.64.0.10")
	targetMachine := createTestMachine(t, app, targetUser, "target-node", "100.64.0.2")
	sourceMachine.HostInfo = HostInfo{
		Hostname:    sourceMachine.Hostname,
		RoutableIPs: []netip.Prefix{mustPrefix(t, "10.10.0.0/24"), ExitRouteV4, ExitRouteV6},
	}
	if err := app.db.Save(sourceMachine).Error; err != nil {
		t.Fatalf("Save(sourceMachine hostinfo): %v", err)
	}
	createTestRoute(t, app, sourceMachine, "10.10.0.0/24", true, true)
	createTestRoute(t, app, sourceMachine, "0.0.0.0/0", true, false)
	createTestRoute(t, app, sourceMachine, "::/0", true, false)

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}
	if _, err := app.AcceptMachineShareByToken(share.ShareToken, targetUser); err != nil {
		t.Fatalf("AcceptMachineShareByToken(): %v", err)
	}

	targetOrg, err := app.GetOrgnaizationByID(targetUser.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(): %v", err)
	}
	targetOrg.AclPolicy = &ACLPolicy{
		Groups:    make(Groups, 0),
		Hosts:     make(Hosts, 0),
		TagOwners: make(TagOwners, 0),
		ACLs: []ACL{{
			Action:       "accept",
			Sources:      []string{"*"},
			Destinations: []string{"*:*"},
		}},
		Tests: make([]ACLTest, 0),
		AutoApprovers: AutoApprovers{
			Routes:   make(map[string][]string, 0),
			ExitNode: make([]string, 0),
		},
		SSHs: make([]SSH, 0),
	}
	if err := app.db.Save(targetOrg).Error; err != nil {
		t.Fatalf("Save(targetOrg ACL policy): %v", err)
	}
	allowSelf, err := app.UpdateACLRulesOfOrg(targetOrg, targetUser, targetMachine)
	if err != nil {
		t.Fatalf("UpdateACLRulesOfOrg(): %v", err)
	}
	targetMachine.User.Organization = *targetOrg

	sourceOrg, err := app.GetOrgnaizationByID(sourceUser.OrganizationID)
	if err != nil {
		t.Fatalf("GetOrgnaizationByID(source): %v", err)
	}
	sourceAllowSelf, err := app.UpdateACLRulesOfOrg(sourceOrg, sourceUser, sourceMachine)
	if err != nil {
		t.Fatalf("UpdateACLRulesOfOrg(source): %v", err)
	}
	sourceMachine.User.Organization = *sourceOrg

	peers, invalidNodeIDs, err := app.getPeers(targetMachine, allowSelf)
	if err != nil {
		t.Fatalf("getPeers(): %v", err)
	}
	if len(invalidNodeIDs) != 0 {
		t.Fatalf("expected no invalid node IDs, got %v", invalidNodeIDs)
	}
	if len(peers) != 1 || peers[0].ID != sourceMachine.ID {
		t.Fatalf("expected one shared peer via ACL visibility, got %+v", peers)
	}
	if !peers[0].Shared {
		t.Fatal("expected ACL peer to be marked shared")
	}

	sourcePeers, sourceInvalidNodeIDs, err := app.getPeers(sourceMachine, sourceAllowSelf)
	if err != nil {
		t.Fatalf("getPeers(source): %v", err)
	}
	if len(sourceInvalidNodeIDs) != 0 {
		t.Fatalf("expected no invalid source node IDs, got %v", sourceInvalidNodeIDs)
	}
	sourcePeersByID := machinesByID(sourcePeers)
	if _, ok := sourcePeersByID[targetMachine.ID]; !ok {
		t.Fatalf("expected source machine to see connected target peer via ACL visibility, got %+v", sourcePeers)
	}
	if peer := sourcePeersByID[targetMachine.ID]; peer.Shared {
		t.Fatalf("expected connected target peer to stay owned, got %+v", peer)
	}
	if _, ok := sourcePeersByID[sourceOrgPeer.ID]; !ok {
		t.Fatalf("expected source machine to keep same-org peer visibility, got %+v", sourcePeers)
	}

	sourceOrgPeer.User.Organization = *sourceOrg
	sourceOrgPeers, sourceOrgInvalidNodeIDs, err := app.getPeers(sourceOrgPeer, sourceAllowSelf)
	if err != nil {
		t.Fatalf("getPeers(source org peer): %v", err)
	}
	if len(sourceOrgInvalidNodeIDs) != 0 {
		t.Fatalf("expected no invalid source org peer node IDs, got %v", sourceOrgInvalidNodeIDs)
	}
	sourceOrgPeersByID := machinesByID(sourceOrgPeers)
	if _, ok := sourceOrgPeersByID[targetMachine.ID]; !ok {
		t.Fatalf("expected source org peer to see connected target peer via ACL visibility, got %+v", sourceOrgPeers)
	}
	if peer := sourceOrgPeersByID[targetMachine.ID]; peer.Shared {
		t.Fatalf("expected source org peer connected target to stay owned, got %+v", peer)
	}
	if _, ok := sourceOrgPeersByID[sourceMachine.ID]; !ok {
		t.Fatalf("expected source org peer to keep same-org source machine visibility, got %+v", sourceOrgPeers)
	}

	sharedNode, err := app.toNode(peers[0], peers[0].Shared)
	if err != nil {
		t.Fatalf("toNode(shared peer): %v", err)
	}
	if !containsPrefix(sharedNode.AllowedIPs, mustPrefix(t, "10.10.0.0/24")) {
		t.Fatalf("expected shared peer allowed IPs to include subnet route, got %v", sharedNode.AllowedIPs)
	}
	if !containsPrefix(sharedNode.AllowedIPs, ExitRouteV4) || !containsPrefix(sharedNode.AllowedIPs, ExitRouteV6) {
		t.Fatalf("expected shared peer allowed IPs to include exit routes, got %v", sharedNode.AllowedIPs)
	}
	if !containsPrefix(sharedNode.PrimaryRoutes, mustPrefix(t, "10.10.0.0/24")) {
		t.Fatalf("expected shared peer primary routes to include subnet route, got %v", sharedNode.PrimaryRoutes)
	}
}

func containsUserID(users []User, want int64) bool {
	for _, user := range users {
		if user.ID == want {
			return true
		}
	}

	return false
}

func TestListShareConnectedOrgIDs(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	thirdUser := createTestUser(t, app, "third@example.com", "Third", "third-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")
	thirdMachine := createTestMachine(t, app, thirdUser, "third-node", "100.64.0.3")
	_ = thirdMachine

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}
	if _, err := app.AcceptMachineShareByToken(share.ShareToken, targetUser); err != nil {
		t.Fatalf("AcceptMachineShareByToken(): %v", err)
	}

	connectedOrgIDs, err := app.ListShareConnectedOrgIDs(sourceUser.OrganizationID)
	if err != nil {
		t.Fatalf("ListShareConnectedOrgIDs(): %v", err)
	}
	if len(connectedOrgIDs) != 2 {
		t.Fatalf("expected 2 connected org IDs, got %v", connectedOrgIDs)
	}
	if connectedOrgIDs[0] != sourceUser.OrganizationID || connectedOrgIDs[1] != targetUser.OrganizationID {
		t.Fatalf("unexpected connected org IDs: %v", connectedOrgIDs)
	}
}

func TestCreateUserFromInviteRejectsExistingUserInOtherOrg(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	owner := createTestUser(t, app, "owner@example.com", "Owner", "invited-org", "Mirage")
	otherUser := createTestUser(t, app, "existing@example.com", "Existing", "other-org", "Mirage")
	_ = otherUser

	invite, err := app.CreateOrgInvite(owner.OrganizationID, owner.ID, "existing@example.com")
	if err == nil {
		t.Fatal("expected invite creation for existing cross-org user to fail")
	}
	if err != ErrOrgInviteTargetBelongsToOtherOrg {
		t.Fatalf("expected ErrOrgInviteTargetBelongsToOtherOrg, got %v", err)
	}
	if invite != nil {
		t.Fatalf("expected nil invite on failure, got %+v", invite)
	}
}

func TestCreateMachineShareRejectsTargetAlreadyInSourceOrg(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	anotherUser := createTestUser(t, app, "teammate@example.com", "Teammate", "source-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")
	_ = anotherUser

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, "teammate@example.com")
	if err == nil {
		t.Fatal("expected share creation to fail for same-org target")
	}
	if err != ErrMachineShareTargetAlreadyInOrg {
		t.Fatalf("expected ErrMachineShareTargetAlreadyInOrg, got %v", err)
	}
	if share != nil {
		t.Fatalf("expected nil share on failure, got %+v", share)
	}
}

func TestListExternalSharedUsersByOrgIDDeduplicatesSourceUsers(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	sourceMachineA := createTestMachine(t, app, sourceUser, "source-a", "100.64.0.1")
	sourceMachineB := createTestMachine(t, app, sourceUser, "source-b", "100.64.0.2")

	shareA, err := app.CreateMachineShare(sourceMachineA, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(source A): %v", err)
	}
	shareB, err := app.CreateMachineShare(sourceMachineB, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(source B): %v", err)
	}
	if _, err := app.AcceptMachineShareByToken(shareA.ShareToken, targetUser); err != nil {
		t.Fatalf("AcceptMachineShareByToken(source A): %v", err)
	}
	if _, err := app.AcceptMachineShareByToken(shareB.ShareToken, targetUser); err != nil {
		t.Fatalf("AcceptMachineShareByToken(source B): %v", err)
	}

	externalUsers, err := app.ListExternalSharedUsersByOrgID(targetUser.OrganizationID)
	if err != nil {
		t.Fatalf("ListExternalSharedUsersByOrgID(): %v", err)
	}
	if len(externalUsers) != 1 || !containsUserID(externalUsers, sourceUser.ID) {
		t.Fatalf("expected one deduplicated external user, got %+v", externalUsers)
	}
}

func TestAcceptOrgInviteByTokenCreatesUserAndMarksAccepted(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	owner := createTestUser(t, app, "owner@example.com", "Owner", "invited-org", "Mirage")

	invite, err := app.CreateOrgInvite(owner.OrganizationID, owner.ID, "new-user@example.com")
	if err != nil {
		t.Fatalf("CreateOrgInvite(): %v", err)
	}

	acceptedInvite, invitedUser, err := app.AcceptOrgInviteByToken(invite.InviteToken, "new-user@example.com", "New User")
	if err != nil {
		t.Fatalf("AcceptOrgInviteByToken(): %v", err)
	}
	if invitedUser == nil {
		t.Fatal("expected accepted invite to create a user")
	}
	if invitedUser.OrganizationID != owner.OrganizationID {
		t.Fatalf("expected invited user org %d, got %d", owner.OrganizationID, invitedUser.OrganizationID)
	}
	if acceptedInvite.Status != OrgInviteStatusAccepted {
		t.Fatalf("expected accepted invite status, got %q", acceptedInvite.Status)
	}
	if acceptedInvite.AcceptedAt == nil {
		t.Fatal("expected accepted invite timestamp to be set")
	}
}

func TestRejectOrgInviteByTokenMarksRejectedAndBlocksAcceptance(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	owner := createTestUser(t, app, "owner@example.com", "Owner", "invited-org", "Mirage")

	invite, err := app.CreateOrgInvite(owner.OrganizationID, owner.ID, "new-user@example.com")
	if err != nil {
		t.Fatalf("CreateOrgInvite(): %v", err)
	}

	rejectedInvite, err := app.RejectOrgInviteByToken(invite.InviteToken, invite.TargetIdentity)
	if err != nil {
		t.Fatalf("RejectOrgInviteByToken(): %v", err)
	}
	if rejectedInvite.Status != OrgInviteStatusRejected {
		t.Fatalf("expected rejected invite status, got %q", rejectedInvite.Status)
	}
	if rejectedInvite.RejectedAt == nil {
		t.Fatal("expected rejected invite timestamp to be set")
	}

	if _, _, err := app.AcceptOrgInviteByToken(invite.InviteToken, invite.TargetIdentity, "New User"); err != ErrOrgInviteAlreadyRejected {
		t.Fatalf("expected ErrOrgInviteAlreadyRejected after reject, got %v", err)
	}
	if err := app.RevokeOrgInvite(invite.ID, owner.OrganizationID); err != ErrOrgInviteAlreadyRejected {
		t.Fatalf("expected revoke after reject to return ErrOrgInviteAlreadyRejected, got %v", err)
	}
}

func TestRejectMachineShareByTokenMarksRejectedAndBlocksFurtherChanges(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}

	rejectedShare, err := app.RejectMachineShareByToken(share.ShareToken, targetUser)
	if err != nil {
		t.Fatalf("RejectMachineShareByToken(): %v", err)
	}
	if rejectedShare.Status != MachineShareStatusRejected {
		t.Fatalf("expected rejected share status, got %q", rejectedShare.Status)
	}
	if rejectedShare.RejectedAt == nil {
		t.Fatal("expected rejected share timestamp to be set")
	}

	if _, err := app.AcceptMachineShareByToken(share.ShareToken, targetUser); err != ErrMachineShareAlreadyRejected {
		t.Fatalf("expected ErrMachineShareAlreadyRejected after reject, got %v", err)
	}
	if err := app.RevokeMachineShare(share.ID, sourceUser.OrganizationID); err != ErrMachineShareAlreadyRejected {
		t.Fatalf("expected revoke after reject to return ErrMachineShareAlreadyRejected, got %v", err)
	}
}

func TestBuildMachineShareResponseIncludesShareURL(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}
	if _, err := app.RejectMachineShareByToken(share.ShareToken, targetUser); err != nil {
		t.Fatalf("RejectMachineShareByToken(): %v", err)
	}

	response := app.buildMachineShareResponse(getMachineShareByID(t, app, share.ID))
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal(response): %v", err)
	}

	payload := map[string]interface{}{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(response): %v", err)
	}

	if payload["shareURL"] != "https://ctrl.example.test/invite/device/"+share.ShareToken {
		t.Fatalf("unexpected shareURL: %#v", payload["shareURL"])
	}
	if payload["status"] != MachineShareStatusRejected {
		t.Fatalf("unexpected share status: %#v", payload["status"])
	}
	if payload["rejectedAt"] == nil {
		t.Fatalf("expected rejectedAt to be serialized, got %#v", payload["rejectedAt"])
	}
}
