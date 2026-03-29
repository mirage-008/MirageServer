package controller

import "testing"

func TestGenMachineNameNormalizesUnsupportedHostnameCharacters(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}

	got := app.GenMachineName("Xiaomi 17 Pro Max / Dev", owner.ID, owner.OrganizationID, "machine-key-1")
	want := "xiaomi-17-pro-max-dev"
	if got != want {
		t.Fatalf("GenMachineName() = %q, want %q", got, want)
	}
}

func TestGenMachineNameNormalizesAndDeduplicates(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}

	first := app.GenMachineName("My Phone 17", owner.ID, owner.OrganizationID, "machine-key-1")
	if first != "my-phone-17" {
		t.Fatalf("first GenMachineName() = %q, want %q", first, "my-phone-17")
	}

	machine := &Machine{
		Hostname:   "My Phone 17",
		GivenName:  first,
		UserID:     owner.ID,
		MachineKey: "existing-machine-key",
	}
	if err := app.db.Create(machine).Error; err != nil {
		t.Fatalf("Create(machine): %v", err)
	}

	second := app.GenMachineName("My Phone 17", owner.ID, owner.OrganizationID, "machine-key-2")
	if second != "my-phone-17-1" {
		t.Fatalf("second GenMachineName() = %q, want %q", second, "my-phone-17-1")
	}
}

func TestRenameMachineNormalizesName(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}

	if err := app.db.Preload("User").First(machine, machine.ID).Error; err != nil {
		t.Fatalf("Preload(User): %v", err)
	}

	if err := app.RenameMachine(machine, "Living Room TV / 4K"); err != nil {
		t.Fatalf("RenameMachine(): %v", err)
	}
	if machine.GivenName != "living-room-tv-4k" {
		t.Fatalf("machine.GivenName = %q, want %q", machine.GivenName, "living-room-tv-4k")
	}
}
