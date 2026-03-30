package controller

import (
	"testing"
)

func TestNormalizeFunnelPlatformConfig(t *testing.T) {
	t.Parallel()

	cfg, err := normalizeFunnelPlatformConfig(FunnelPlatformConfig{
		ManagedBaseDomain:   "  PUBLIC.EXAMPLE.COM  ",
		DefaultEdgeMode:     "",
		DefaultListenerMode: "",
		DirectBindAddrs:     StringList{" 0.0.0.0 ", "::", "0.0.0.0"},
		DirectBindPorts:     FunnelPortList{443, 80, 443},
		TrustedProxyCIDRs:   StringList{"10.0.0.0/24", " 10.0.0.0/24 "},
	})
	if err != nil {
		t.Fatalf("normalizeFunnelPlatformConfig(): %v", err)
	}
	if cfg.ManagedBaseDomain != "public.example.com" {
		t.Fatalf("ManagedBaseDomain = %q", cfg.ManagedBaseDomain)
	}
	if cfg.DefaultEdgeMode != FunnelEdgeModeServer {
		t.Fatalf("DefaultEdgeMode = %q", cfg.DefaultEdgeMode)
	}
	if cfg.DefaultListenerMode != FunnelListenerModeDirect {
		t.Fatalf("DefaultListenerMode = %q", cfg.DefaultListenerMode)
	}
	if len(cfg.DirectBindPorts) != 2 || cfg.DirectBindPorts[0] != 80 || cfg.DirectBindPorts[1] != 443 {
		t.Fatalf("DirectBindPorts = %#v", cfg.DirectBindPorts)
	}
	if len(cfg.TrustedProxyCIDRs) != 1 || cfg.TrustedProxyCIDRs[0] != "10.0.0.0/24" {
		t.Fatalf("TrustedProxyCIDRs = %#v", cfg.TrustedProxyCIDRs)
	}
}

func TestNormalizeFunnelPlatformConfigRejectsInvalidCIDR(t *testing.T) {
	t.Parallel()

	if _, err := normalizeFunnelPlatformConfig(FunnelPlatformConfig{
		TrustedProxyCIDRs: StringList{"not-a-cidr"},
	}); err == nil {
		t.Fatal("expected invalid CIDR to fail")
	}
}

func TestNormalizeFunnelPlatformConfigRejectsInvalidPort(t *testing.T) {
	t.Parallel()

	if _, err := normalizeFunnelPlatformConfig(FunnelPlatformConfig{
		DirectBindPorts: FunnelPortList{0},
	}); err == nil {
		t.Fatal("expected invalid port to fail")
	}
}

func TestNormalizeFunnelPlatformConfigDNSMgrDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := normalizeFunnelPlatformConfig(FunnelPlatformConfig{
		ManagedDNSProvider: FunnelManagedDNSProviderDNSMgr,
		ManagedDNSUID:      1000,
		ManagedDNSAPIKey:   "secret",
	})
	if err != nil {
		t.Fatalf("normalizeFunnelPlatformConfig(): %v", err)
	}
	if cfg.ManagedBaseDomain != defaultFunnelDNSMgrBaseDomain {
		t.Fatalf("ManagedBaseDomain = %q", cfg.ManagedBaseDomain)
	}
	if cfg.ManagedDNSAPIBaseURL != defaultFunnelDNSMgrAPIBaseURL {
		t.Fatalf("ManagedDNSAPIBaseURL = %q", cfg.ManagedDNSAPIBaseURL)
	}
}

func TestNormalizeFunnelPlatformConfigRejectsDNSMgrSuffixOverride(t *testing.T) {
	t.Parallel()

	if _, err := normalizeFunnelPlatformConfig(FunnelPlatformConfig{
		ManagedBaseDomain:  "custom.example.test",
		ManagedDNSProvider: FunnelManagedDNSProviderDNSMgr,
		ManagedDNSUID:      1000,
		ManagedDNSAPIKey:   "secret",
	}); err == nil {
		t.Fatal("expected dnsmgr base domain override to fail")
	}
}

func TestEffectiveFunnelIngressTargets(t *testing.T) {
	t.Parallel()

	targets := effectiveFunnelIngressTargets(FunnelPlatformConfig{
		DirectBindAddrs: StringList{"127.0.0.1", "::1"},
		DirectBindPorts: FunnelPortList{80, 443},
	})
	if len(targets) != 4 {
		t.Fatalf("expected 4 targets, got %d", len(targets))
	}
}

func TestFunnelDomainSummaryExplainsUnsupportedManagedCertPorts(t *testing.T) {
	t.Parallel()

	cfg := FunnelPlatformConfig{
		DefaultListenerMode: FunnelListenerModeDirect,
		DirectBindPorts:     FunnelPortList{880, 4443},
	}
	domain := &FunnelDomain{
		Domain:       "managed.example.test",
		DomainType:   FunnelDomainTypeManaged,
		Status:       FunnelDomainStatusPendingCert,
		DNSStatus:    FunnelDNSStatusReady,
		TLSMode:      FunnelTLSModePlatformManaged,
		ListenerMode: FunnelListenerModeDirect,
		EdgeMode:     FunnelEdgeModeServer,
	}

	summary := funnelDomainSummaryWithConfig(domain, &cfg, nil)
	if summary["status"] != "error" {
		t.Fatalf("status = %#v", summary["status"])
	}
	if summary["label"] != "证书受阻" {
		t.Fatalf("label = %#v", summary["label"])
	}
	if got := summary["reason"].(string); got == "" || got == "DNS 已就绪，等待证书签发" {
		t.Fatalf("reason = %#v", got)
	}
}

func TestFunnelDomainSummaryAllowsDNS01ManagedCertsWithout443(t *testing.T) {
	t.Parallel()

	cfg := FunnelPlatformConfig{
		ManagedBaseDomain:    defaultFunnelDNSMgrBaseDomain,
		ManagedDNSProvider:   FunnelManagedDNSProviderDNSMgr,
		DefaultListenerMode:  FunnelListenerModeDirect,
		DirectBindPorts:      FunnelPortList{880, 4443},
	}
	domain := &FunnelDomain{
		Domain:       "managed.example." + defaultFunnelDNSMgrBaseDomain,
		DomainType:   FunnelDomainTypeManaged,
		Status:       FunnelDomainStatusPendingCert,
		DNSStatus:    FunnelDNSStatusReady,
		TLSMode:      FunnelTLSModePlatformManaged,
		ListenerMode: FunnelListenerModeDirect,
		EdgeMode:     FunnelEdgeModeServer,
	}

	summary := funnelDomainSummaryWithConfig(domain, &cfg, nil)
	if summary["status"] != "pending" {
		t.Fatalf("status = %#v", summary["status"])
	}
	if summary["label"] != "待证书" {
		t.Fatalf("label = %#v", summary["label"])
	}
	if got, want := summary["reason"], "DNS 已就绪，将通过 DNS-01 签发证书"; got != want {
		t.Fatalf("reason = %#v, want %#v", got, want)
	}
}

func TestFunnelPlatformSummaryAllowsDNS01Without443(t *testing.T) {
	t.Parallel()

	summary := funnelPlatformSummary(FunnelPlatformConfig{
		ManagedBaseDomain:    defaultFunnelDNSMgrBaseDomain,
		ManagedDNSProvider:   FunnelManagedDNSProviderDNSMgr,
		DefaultListenerMode:  FunnelListenerModeDirect,
		DefaultEdgeMode:      FunnelEdgeModeServer,
		DirectBindPorts:      FunnelPortList{880, 4443},
		DirectBindAddrs:      StringList{"0.0.0.0"},
	})
	if summary["status"] != "ready" {
		t.Fatalf("status = %#v", summary["status"])
	}
	if got, want := summary["reason"], "平台入口和 DNS-01 已就绪，可以给托管域名自动签证"; got != want {
		t.Fatalf("reason = %#v, want %#v", got, want)
	}
}
