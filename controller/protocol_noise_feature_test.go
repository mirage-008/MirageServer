package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

func TestNoiseQueryFeatureHandlerReturnsCompleteForConfiguredFunnel(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}

	machineKey, nodeKey := mustNoiseTestKeys(t, machine)
	for _, feature := range []string{"serve", "funnel"} {
		reqBody, err := json.Marshal(tailcfg.QueryFeatureRequest{
			NodeKey: nodeKey,
			Feature: feature,
		})
		if err != nil {
			t.Fatalf("json.Marshal(%q): %v", feature, err)
		}
		req := httptest.NewRequest(http.MethodPost, "/machine/feature/query", bytes.NewReader(reqBody))
		rec := httptest.NewRecorder()

		ns := &noiseServer{
			mirage:     app,
			machineKey: machineKey,
		}
		ns.NoiseQueryFeatureHandler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status code = %d, body = %s", feature, rec.Code, rec.Body.String())
		}

		var resp tailcfg.QueryFeatureResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("json.Decode(%q): %v", feature, err)
		}
		if !resp.Complete {
			t.Fatalf("%s Complete = false, want true; resp=%+v", feature, resp)
		}
	}
}

func TestNoiseQueryFeatureHandlerReturnsInstructionsWhenFunnelUnavailable(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	app.cfg.FunnelCfg.DirectBindPorts = nil

	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}

	machineKey, nodeKey := mustNoiseTestKeys(t, machine)
	reqBody, err := json.Marshal(tailcfg.QueryFeatureRequest{
		NodeKey: nodeKey,
		Feature: "funnel",
	})
	if err != nil {
		t.Fatalf("json.Marshal(request): %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/machine/feature/query", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	ns := &noiseServer{
		mirage:     app,
		machineKey: machineKey,
	}
	ns.NoiseQueryFeatureHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp tailcfg.QueryFeatureResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("json.Decode(response): %v", err)
	}
	if resp.Complete {
		t.Fatalf("Complete = true, want false; resp=%+v", resp)
	}
	if resp.Text == "" {
		t.Fatalf("expected explanatory text, got %+v", resp)
	}
	if resp.ShouldWait {
		t.Fatalf("ShouldWait = true, want false; resp=%+v", resp)
	}
}

func mustNoiseTestKeys(t *testing.T, machine *Machine) (key.MachinePublic, key.NodePublic) {
	t.Helper()

	var machineKey key.MachinePublic
	if err := machineKey.UnmarshalText([]byte(MachinePublicKeyEnsurePrefix(machine.MachineKey))); err != nil {
		t.Fatalf("UnmarshalText(machine key): %v", err)
	}
	var nodeKey key.NodePublic
	if err := nodeKey.UnmarshalText([]byte(NodePublicKeyEnsurePrefix(machine.NodeKey))); err != nil {
		t.Fatalf("UnmarshalText(node key): %v", err)
	}
	return machineKey, nodeKey
}
