package controller

import (
	"net/netip"
	"testing"
)

func TestDecodeViaPrefix(t *testing.T) {
	preview, err := buildViaRoutePreview(netip.MustParsePrefix("192.168.1.0/24"), 7)
	if err != nil {
		t.Fatalf("buildViaRoutePreview: %v", err)
	}

	siteID, originalPrefix, ok := decodeViaPrefix(netip.MustParsePrefix(preview.ViaPrefix))
	if !ok {
		t.Fatalf("decodeViaPrefix should detect 4via6 prefix")
	}
	if siteID != 7 {
		t.Fatalf("decodeViaPrefix siteID = %d, want 7", siteID)
	}
	if originalPrefix != netip.MustParsePrefix("192.168.1.0/24") {
		t.Fatalf("decodeViaPrefix originalPrefix = %s, want 192.168.1.0/24", originalPrefix)
	}
}

func TestBuildMachineRouteDetailForViaPrefix(t *testing.T) {
	prefix := netip.MustParsePrefix("fd7a:115c:a1e0:b1a:0:7:c0a8:100/120")

	detail := buildMachineRouteDetail(prefix, true)
	if !detail.IsVia {
		t.Fatalf("buildMachineRouteDetail should mark via prefixes")
	}
	if detail.ViaSiteID != 7 {
		t.Fatalf("buildMachineRouteDetail ViaSiteID = %d, want 7", detail.ViaSiteID)
	}
	if detail.ViaOriginalPrefix != "192.168.1.0/24" {
		t.Fatalf("buildMachineRouteDetail ViaOriginalPrefix = %s, want 192.168.1.0/24", detail.ViaOriginalPrefix)
	}
	if detail.DisplayLabel == prefix.String() {
		t.Fatalf("buildMachineRouteDetail should enrich via display label, got %s", detail.DisplayLabel)
	}
}

func TestBuildViaRoutePreviewRejectsInvalidInput(t *testing.T) {
	if _, err := buildViaRoutePreview(netip.MustParsePrefix("fd7a:115c:a1e0::/64"), 1); err == nil {
		t.Fatalf("buildViaRoutePreview should reject IPv6 prefixes")
	}
	if _, err := buildViaRoutePreview(netip.MustParsePrefix("192.168.1.0/24"), 0); err == nil {
		t.Fatalf("buildViaRoutePreview should reject site ID 0")
	}
	if _, err := buildViaRoutePreview(netip.MustParsePrefix("0.0.0.0/0"), 1); err == nil {
		t.Fatalf("buildViaRoutePreview should reject exit routes")
	}
}
