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
