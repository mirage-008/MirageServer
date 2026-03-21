package controller

import (
	"reflect"
	"testing"

	"tailscale.com/tailcfg"
)

func TestValidateDERPMapURLDefaultsBlankInput(t *testing.T) {
	t.Parallel()

	got, err := validateDERPMapURL("")
	if err != nil {
		t.Fatalf("validateDERPMapURL returned error: %v", err)
	}
	if got != defaultRemoteDERPMapURL {
		t.Fatalf("unexpected default DERP map URL: got %q want %q", got, defaultRemoteDERPMapURL)
	}
}

func TestValidateDERPMapURLRejectsNonHTTPURL(t *testing.T) {
	t.Parallel()

	if _, err := validateDERPMapURL("ftp://example.com/derpmap/default"); err == nil {
		t.Fatal("expected non-http DERP map URL to be rejected")
	}
}

func TestCloneDERPMapMakesDeepCopy(t *testing.T) {
	t.Parallel()

	src := &tailcfg.DERPMap{
		OmitDefaultRegions: true,
		Regions: map[int]*tailcfg.DERPRegion{
			1: {
				RegionID:   1,
				RegionCode: "nyc",
				RegionName: "New York City",
				Nodes: []*tailcfg.DERPNode{{
					Name:     "1a",
					RegionID: 1,
					HostName: "derp1.example.com",
					IPv4:     "1.2.3.4",
				}},
			},
		},
	}

	cloned := cloneDERPMap(src)
	if !reflect.DeepEqual(cloned, src) {
		t.Fatalf("unexpected cloned DERP map: got %+v want %+v", cloned, src)
	}

	cloned.OmitDefaultRegions = false
	cloned.Regions[1].RegionName = "Changed"
	cloned.Regions[1].Nodes[0].HostName = "changed.example.com"

	if src.OmitDefaultRegions != true {
		t.Fatalf("source OmitDefaultRegions was modified: got %v", src.OmitDefaultRegions)
	}
	if src.Regions[1].RegionName != "New York City" {
		t.Fatalf("source region was modified: got %q", src.Regions[1].RegionName)
	}
	if src.Regions[1].Nodes[0].HostName != "derp1.example.com" {
		t.Fatalf("source node was modified: got %q", src.Regions[1].Nodes[0].HostName)
	}
}

func TestMergeDERPRegionOverridesExistingRegion(t *testing.T) {
	t.Parallel()

	derpMap := &tailcfg.DERPMap{
		Regions: map[int]*tailcfg.DERPRegion{
			900: {
				RegionID:   900,
				RegionCode: "old",
				RegionName: "Old",
				Nodes: []*tailcfg.DERPNode{{
					Name:     "900a",
					RegionID: 900,
					HostName: "old.example.com",
				}},
			},
		},
	}
	region := tailcfg.DERPRegion{
		RegionID:   900,
		RegionCode: "cn",
		RegionName: "China",
		Nodes: []*tailcfg.DERPNode{{
			Name:     "900b",
			RegionID: 900,
			HostName: "new.example.com",
		}},
	}

	mergeDERPRegion(derpMap, region)

	got := derpMap.Regions[900]
	if got == nil {
		t.Fatal("merged region missing")
	}
	if got.RegionCode != "cn" || got.RegionName != "China" {
		t.Fatalf("unexpected merged region metadata: %+v", got)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].HostName != "new.example.com" {
		t.Fatalf("unexpected merged region nodes: %+v", got.Nodes)
	}

	region.Nodes[0].HostName = "mutated.example.com"
	if got.Nodes[0].HostName != "new.example.com" {
		t.Fatalf("mergeDERPRegion kept source alias instead of cloning: got %q", got.Nodes[0].HostName)
	}
}
