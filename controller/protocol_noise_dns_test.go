package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
)

type testSetDNSProvider struct {
	name  string
	value string
}

func (p *testSetDNSProvider) EnsureManagedDomain(context.Context, string) error {
	return nil
}

func (p *testSetDNSProvider) DeleteManagedDomain(context.Context, string) error {
	return nil
}

func (p *testSetDNSProvider) LookupManagedDomain(context.Context, string) (managedFunnelDNSLookupResult, error) {
	return managedFunnelDNSLookupResult{}, nil
}

func (p *testSetDNSProvider) UpsertTXTRecord(_ context.Context, fqdn, value string) error {
	p.name = fqdn
	p.value = value
	return nil
}

func TestNoiseSetDNSHandlerUpsertsTXTRecord(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	provider := &testSetDNSProvider{}
	app.newManagedFunnelDNSProvider = func(FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
		return provider, nil
	}

	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	org := machine.User.Organization
	org.EnableMagic = true
	org.MagicDnsDomain = "tenant-org.mira.test"
	if err := app.db.Save(&org).Error; err != nil {
		t.Fatalf("Save(organization): %v", err)
	}
	refreshedMachine, err := app.GetMachineByID(machine.ID)
	if err != nil {
		t.Fatalf("GetMachineByID(machine): %v", err)
	}
	machine = refreshedMachine

	var machineKey key.MachinePublic
	if err := machineKey.UnmarshalText([]byte(MachinePublicKeyEnsurePrefix(machine.MachineKey))); err != nil {
		t.Fatalf("UnmarshalText(machine key): %v", err)
	}
	var nodeKey key.NodePublic
	if err := nodeKey.UnmarshalText([]byte(NodePublicKeyEnsurePrefix(machine.NodeKey))); err != nil {
		t.Fatalf("UnmarshalText(node key): %v", err)
	}

	expectedName := officialACMEChallengeNameForMachine(machine, app.cfg.IPPrefixes)
	reqBody, err := json.Marshal(tailcfg.SetDNSRequest{
		NodeKey: nodeKey,
		Name:    expectedName,
		Type:    "TXT",
		Value:   "challenge-token",
	})
	if err != nil {
		t.Fatalf("json.Marshal(request): %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/machine/set-dns", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	ns := &noiseServer{
		mirage:     app,
		machineKey: machineKey,
	}
	ns.NoiseSetDNSHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if provider.name != expectedName {
		t.Fatalf("provider.name = %q, want %q", provider.name, expectedName)
	}
	if provider.value != "challenge-token" {
		t.Fatalf("provider.value = %q", provider.value)
	}
}

func TestNoiseSetDNSHandlerRejectsUnexpectedDomain(t *testing.T) {
	t.Parallel()

	app := newFunnelTenantTestMirage(t)
	provider := &testSetDNSProvider{}
	app.newManagedFunnelDNSProvider = func(FunnelPlatformConfig) (managedFunnelDNSProvider, error) {
		return provider, nil
	}

	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	org := machine.User.Organization
	org.EnableMagic = true
	org.MagicDnsDomain = "tenant-org.mira.test"
	if err := app.db.Save(&org).Error; err != nil {
		t.Fatalf("Save(organization): %v", err)
	}
	refreshedMachine, err := app.GetMachineByID(machine.ID)
	if err != nil {
		t.Fatalf("GetMachineByID(machine): %v", err)
	}
	machine = refreshedMachine

	var machineKey key.MachinePublic
	if err := machineKey.UnmarshalText([]byte(MachinePublicKeyEnsurePrefix(machine.MachineKey))); err != nil {
		t.Fatalf("UnmarshalText(machine key): %v", err)
	}
	var nodeKey key.NodePublic
	if err := nodeKey.UnmarshalText([]byte(NodePublicKeyEnsurePrefix(machine.NodeKey))); err != nil {
		t.Fatalf("UnmarshalText(node key): %v", err)
	}

	reqBody, err := json.Marshal(tailcfg.SetDNSRequest{
		NodeKey: nodeKey,
		Name:    "_acme-challenge.other-node.tenant-org.mira.test",
		Type:    "TXT",
		Value:   "challenge-token",
	})
	if err != nil {
		t.Fatalf("json.Marshal(request): %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/machine/set-dns", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()

	ns := &noiseServer{
		mirage:     app,
		machineKey: machineKey,
	}
	ns.NoiseSetDNSHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if provider.name != "" || provider.value != "" {
		t.Fatalf("provider should not have been invoked, got name=%q value=%q", provider.name, provider.value)
	}
}
