package controller

import (
	"errors"
	"net/netip"
	"testing"
)

func TestHardDeleteMachineReleasesIPsAndRemovesMachineShares(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	sourceUser := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	targetUser := createTestUser(t, app, "target@example.com", "Target", "target-org", "Mirage")
	sourceMachine := createTestMachine(t, app, sourceUser, "source-node", "100.64.0.1")

	share, err := app.CreateMachineShare(sourceMachine, sourceUser, targetUser.Name)
	if err != nil {
		t.Fatalf("CreateMachineShare(): %v", err)
	}
	if _, err := app.AcceptMachineShareByToken(share.ShareToken, targetUser); err != nil {
		t.Fatalf("AcceptMachineShareByToken(): %v", err)
	}

	if err := app.HardDeleteMachine(sourceMachine); err != nil {
		t.Fatalf("HardDeleteMachine(): %v", err)
	}

	var machineCount int64
	if err := app.db.Model(&Machine{}).Where("id = ?", sourceMachine.ID).Count(&machineCount).Error; err != nil {
		t.Fatalf("Count(machine): %v", err)
	}
	if machineCount != 0 {
		t.Fatalf("expected deleted machine row to be removed, got %d rows", machineCount)
	}

	var shareCount int64
	if err := app.db.Model(&MachineShare{}).Where("source_machine_id = ?", sourceMachine.ID).Count(&shareCount).Error; err != nil {
		t.Fatalf("Count(machine_shares): %v", err)
	}
	if shareCount != 0 {
		t.Fatalf("expected machine shares for deleted machine to be removed, got %d rows", shareCount)
	}

	ip, err := app.getAvailableIP(app.cfg.IPPrefixes[0])
	if err != nil {
		t.Fatalf("getAvailableIP(): %v", err)
	}
	if *ip != netip.MustParseAddr("100.64.0.1") {
		t.Fatalf("expected deleted machine IPv4 to be reusable, got %s", ip.String())
	}
}

func TestSetMachineAddressesPreservesUnspecifiedFamilies(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	user := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	machine := createTestMachine(
		t,
		app,
		user,
		"source-node",
		"100.64.0.1",
		"fd7a:115c:a1e0::1",
	)

	if err := app.SetMachineAddresses(machine, []string{"100.64.0.50"}); err != nil {
		t.Fatalf("SetMachineAddresses(): %v", err)
	}

	got := machine.IPAddresses.ToStringSlice()
	wantV4 := "100.64.0.50"
	wantV6 := "fd7a:115c:a1e0::1"
	if len(got) != 2 {
		t.Fatalf("expected dual-stack addresses after update, got %v", got)
	}
	if !containsStr(got, wantV4) || !containsStr(got, wantV6) {
		t.Fatalf("expected addresses to include %s and %s, got %v", wantV4, wantV6, got)
	}
}

func TestSetMachineAddressesRejectsOccupiedAddress(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	user := createTestUser(t, app, "source@example.com", "Source", "source-org", "Mirage")
	machine := createTestMachine(
		t,
		app,
		user,
		"source-node",
		"100.64.0.1",
		"fd7a:115c:a1e0::1",
	)
	_ = createTestMachine(
		t,
		app,
		user,
		"occupied-node",
		"100.64.0.2",
		"fd7a:115c:a1e0::2",
	)

	err := app.SetMachineAddresses(machine, []string{"100.64.0.2"})
	if !errors.Is(err, ErrMachineIPAddressUnavailable) {
		t.Fatalf("SetMachineAddresses() error = %v, want %v", err, ErrMachineIPAddressUnavailable)
	}
}

func TestGetAvailableIPSkipsSharedPeerMasqRange(t *testing.T) {
	t.Parallel()

	app := newShareInviteTestMirage(t)
	app.cfg.IPPrefixes = []netip.Prefix{sharePeerMasqIPv4Prefix, sharePeerMasqIPv6Prefix}

	if _, err := app.getAvailableIP(app.cfg.IPPrefixes[0]); !errors.Is(err, ErrCouldNotAllocateIP) {
		t.Fatalf("getAvailableIP(v4 masq range) error = %v, want %v", err, ErrCouldNotAllocateIP)
	}
	if _, err := app.getAvailableIP(app.cfg.IPPrefixes[1]); !errors.Is(err, ErrCouldNotAllocateIP) {
		t.Fatalf("getAvailableIP(v6 masq range) error = %v, want %v", err, ErrCouldNotAllocateIP)
	}
}
