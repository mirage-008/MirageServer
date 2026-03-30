package controller

import (
	"testing"
	"time"

	"tailscale.com/tailcfg"
)

func TestOfficialFunnelPortCapabilityIncludes443ForCLICompatibility(t *testing.T) {
	capability, ok := officialFunnelPortCapability(FunnelPlatformConfig{
		DirectBindPorts: FunnelPortList{8443},
	})
	if !ok {
		t.Fatal("expected funnel capability to be generated")
	}
	if got, want := string(capability), string(tailcfg.CapabilityFunnelPorts)+"?ports=443,8443"; got != want {
		t.Fatalf("official funnel capability = %q, want %q", got, want)
	}
}

func TestMachineCanUseOfficialFunnelRequiresPublicDomain(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	user := &User{}
	if err := app.db.First(user, machine.UserID).Error; err != nil {
		t.Fatalf("First(user): %v", err)
	}
	org := &Organization{}
	if err := app.db.First(org, user.OrganizationID).Error; err != nil {
		t.Fatalf("First(organization): %v", err)
	}
	org.EnableMagic = false
	org.MagicDnsDomain = ""
	if err := app.db.Save(org).Error; err != nil {
		t.Fatalf("Save(organization): %v", err)
	}
	app.cfg.FunnelCfg.ManagedBaseDomain = ""

	if machineCanUseOfficialServe(machine, app.cfg.IPPrefixes, app.cfg.FunnelCfg) {
		t.Fatal("did not expect official serve eligibility without public domain")
	}
	if machineCanUseOfficialFunnel(machine, app.cfg.IPPrefixes, app.cfg.FunnelCfg) {
		t.Fatal("did not expect official funnel eligibility without public domain")
	}
}

func TestOfficialFunnelDomainUsesManagedBaseDomain(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	user := &User{}
	if err := app.db.First(user, machine.UserID).Error; err != nil {
		t.Fatalf("First(user): %v", err)
	}
	org := &Organization{}
	if err := app.db.First(org, user.OrganizationID).Error; err != nil {
		t.Fatalf("First(organization): %v", err)
	}
	org.EnableMagic = true
	org.MagicDnsDomain = "tenant-org.mira.test"
	if err := app.db.Save(org).Error; err != nil {
		t.Fatalf("Save(organization): %v", err)
	}

	app.cfg.FunnelCfg.ManagedBaseDomain = "public.example.test"
	got := officialFunnelDomainForMachine(machine, app.cfg.IPPrefixes, app.cfg.FunnelCfg)
	want := "tenant-machine.public.example.test"
	if got != want {
		t.Fatalf("officialFunnelDomainForMachine() = %q, want %q", got, want)
	}
}

func TestRequestFunnelRuntimeReloadBumpsLastStateChange(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}

	before := app.getOrgLastStateChange(owner.OrganizationID)
	time.Sleep(10 * time.Millisecond)
	app.requestFunnelRuntimeReload()
	after := app.getOrgLastStateChange(owner.OrganizationID)
	if !after.After(before) {
		t.Fatalf("last state change did not advance: before=%v after=%v", before, after)
	}
}
