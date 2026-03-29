package controller

import (
	"testing"

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

	if machineCanUseOfficialServe(machine, app.cfg.IPPrefixes, app.cfg.FunnelCfg) {
		t.Fatal("did not expect official serve eligibility without public domain")
	}
	if machineCanUseOfficialFunnel(machine, app.cfg.IPPrefixes, app.cfg.FunnelCfg) {
		t.Fatal("did not expect official funnel eligibility without public domain")
	}
}
