package controller

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"tailscale.com/tailcfg"
)

type funnelTestDialer struct{}

func (d *funnelTestDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func (d *funnelTestDialer) Close() error { return nil }

type funnelIngressTestDialer struct{}

func (d *funnelIngressTestDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
}

func (d *funnelIngressTestDialer) Close() error { return nil }

func TestMarkFunnelDomainVerifiedSetsDNSReady(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	domain, _, err := app.createTenantFunnelDomain(owner, FunnelDomainCreateRequest{
		Domain:       "custom.example.test",
		DomainType:   FunnelDomainTypeCustom,
		ListenerMode: FunnelListenerModeDirect,
		EdgeMode:     FunnelEdgeModeServer,
		TLSMode:      FunnelTLSModePlatformManaged,
	})
	if err != nil {
		t.Fatalf("createTenantFunnelDomain(): %v", err)
	}

	if err := markFunnelDomainVerified(app.db, domain); err != nil {
		t.Fatalf("markFunnelDomainVerified(): %v", err)
	}

	stored := &FunnelDomain{}
	if err := app.db.First(stored, domain.ID).Error; err != nil {
		t.Fatalf("First(domain): %v", err)
	}
	if stored.ValidationCheckedAt == nil {
		t.Fatal("expected ValidationCheckedAt to be set")
	}
	if stored.DNSStatus != FunnelDNSStatusReady {
		t.Fatalf("DNSStatus = %q, want %q", stored.DNSStatus, FunnelDNSStatusReady)
	}
	if stored.Status != FunnelDomainStatusPendingCert {
		t.Fatalf("Status = %q, want %q", stored.Status, FunnelDomainStatusPendingCert)
	}
}

func TestFunnelRuntimeProxiesHTTPMount(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	listenPort := freeFunnelTestPort(t)
	sysCfg.FunnelCfg.DirectBindAddrs = StringList{"127.0.0.1"}
	sysCfg.FunnelCfg.DirectBindPorts = FunnelPortList{listenPort}
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}
	app.cfg.FunnelCfg = sysCfg.FunnelCfg

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf("path=%s query=%s host=%s", r.URL.Path, r.URL.RawQuery, r.Host))
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("url.Parse(backend): %v", err)
	}
	backendPort, err := strconv.Atoi(backendURL.Port())
	if err != nil {
		t.Fatalf("Atoi(backend port): %v", err)
	}

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}

	service, domain, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:      machine.ID,
		DomainMode:     "managed",
		ListenProto:    FunnelListenProtoHTTP,
		ListenPort:     listenPort,
		MountPath:      "/app",
		BackendType:    FunnelBackendTypeHTTPProxy,
		BackendScheme:  "http",
		BackendPort:    backendPort,
		BackendTailnet: backendURL.Hostname(),
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}

	rt := newFunnelRuntime(app)
	rt.newOrgDialer = func(ctx context.Context, orgID int64) (funnelOrgDialer, error) {
		return &funnelTestDialer{}, nil
	}
	app.setFunnelRuntime(rt)
	defer app.setFunnelRuntime(nil)
	if err := rt.start(); err != nil {
		t.Fatalf("runtime.start(): %v", err)
	}
	defer rt.close()

	rt.reload("test")

	var (
		respBody string
		lastErr  error
	)
	client := &http.Client{Timeout: 2 * time.Second}
	for attempt := 0; attempt < 30; attempt++ {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/app/hello?x=1", listenPort), nil)
		if err != nil {
			t.Fatalf("http.NewRequest(): %v", err)
		}
		req.Host = domain.Domain
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("ReadAll(response): %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
			time.Sleep(100 * time.Millisecond)
			continue
		}
		respBody = string(body)
		lastErr = nil
		break
	}
	if lastErr != nil {
		t.Fatalf("request through funnel runtime failed: %v", lastErr)
	}
	if want := "path=/hello query=x=1 host=" + domain.Domain; respBody != want {
		t.Fatalf("runtime response = %q, want %q", respBody, want)
	}

	status, err := app.buildTenantFunnelServiceStatus(service)
	if err != nil {
		t.Fatalf("buildTenantFunnelServiceStatus(): %v", err)
	}
	serviceMap, ok := status["service"].(map[string]any)
	if !ok {
		t.Fatalf("status[service] type = %T", status["service"])
	}
	if got := serviceMap["configStatus"]; got != FunnelServiceConfigStatusActive {
		t.Fatalf("configStatus = %#v, want %q", got, FunnelServiceConfigStatusActive)
	}
	if got := serviceMap["edgeStatus"]; got != FunnelServiceEdgeStatusApplied {
		t.Fatalf("edgeStatus = %#v, want %q", got, FunnelServiceEdgeStatusApplied)
	}
}

func TestFunnelRuntimeProxiesRawTCP(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	listenPort := freeFunnelTestPort(t)
	sysCfg.FunnelCfg.DirectBindAddrs = StringList{"127.0.0.1"}
	sysCfg.FunnelCfg.DirectBindPorts = FunnelPortList{listenPort}
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}
	app.cfg.FunnelCfg = sysCfg.FunnelCfg

	backendHost, backendPort, stopBackend := startFunnelTCPBackend(t, "raw:")
	defer stopBackend()

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}

	service, _, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:      machine.ID,
		DomainMode:     "managed",
		ListenProto:    FunnelListenProtoTCP,
		ListenPort:     listenPort,
		BackendType:    FunnelBackendTypeTCPProxy,
		BackendPort:    backendPort,
		BackendTailnet: backendHost,
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}

	rt := newFunnelRuntime(app)
	rt.newOrgDialer = func(ctx context.Context, orgID int64) (funnelOrgDialer, error) {
		return &funnelTestDialer{}, nil
	}
	app.setFunnelRuntime(rt)
	defer app.setFunnelRuntime(nil)
	if err := rt.start(); err != nil {
		t.Fatalf("runtime.start(): %v", err)
	}
	defer rt.close()

	rt.reload("test")

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("DialTimeout(runtime): %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetDeadline(runtime): %v", err)
	}

	payload := "hello-raw"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("Write(runtime): %v", err)
	}
	reply := make([]byte, len("raw:")+len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("ReadFull(runtime): %v", err)
	}
	if got, want := string(reply), "raw:"+payload; got != want {
		t.Fatalf("runtime tcp reply = %q, want %q", got, want)
	}

	assertFunnelServiceActive(t, app, service)
}

func TestFunnelRuntimeProxiesTLSTerminatedTCP(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	listenPort := freeFunnelTestPort(t)
	sysCfg.FunnelCfg.DirectBindAddrs = StringList{"127.0.0.1"}
	sysCfg.FunnelCfg.DirectBindPorts = FunnelPortList{listenPort}
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}
	app.cfg.FunnelCfg = sysCfg.FunnelCfg

	backendHost, backendPort, stopBackend := startFunnelTCPBackend(t, "tls:")
	defer stopBackend()

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}

	service, domain, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:      machine.ID,
		DomainMode:     "managed",
		ListenProto:    FunnelListenProtoTLSTerminatedTCP,
		ListenPort:     listenPort,
		BackendType:    FunnelBackendTypeTCPProxy,
		BackendPort:    backendPort,
		BackendTailnet: backendHost,
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}

	cert := newFunnelTestCertificate(t, domain.Domain)
	rt := newFunnelRuntime(app)
	rt.newOrgDialer = func(ctx context.Context, orgID int64) (funnelOrgDialer, error) {
		return &funnelTestDialer{}, nil
	}
	rt.getCertFunc = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		return &cert, nil
	}
	app.setFunnelRuntime(rt)
	defer app.setFunnelRuntime(nil)
	if err := rt.start(); err != nil {
		t.Fatalf("runtime.start(): %v", err)
	}
	defer rt.close()

	rt.reload("test")

	conn, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(listenPort)), &tls.Config{
		ServerName:         domain.Domain,
		InsecureSkipVerify: true, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("tls.Dial(runtime): %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetDeadline(runtime): %v", err)
	}

	payload := "hello-tls"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("Write(runtime): %v", err)
	}
	reply := make([]byte, len("tls:")+len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("ReadFull(runtime): %v", err)
	}
	if got, want := string(reply), "tls:"+payload; got != want {
		t.Fatalf("runtime tls-tcp reply = %q, want %q", got, want)
	}

	assertFunnelServiceActive(t, app, service)
}

func TestFunnelRuntimeProxiesOfficialIngressHTTP(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	listenPort := freeFunnelTestPort(t)
	sysCfg.FunnelCfg.DirectBindAddrs = StringList{"127.0.0.1"}
	sysCfg.FunnelCfg.DirectBindPorts = FunnelPortList{80, listenPort}
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}
	app.cfg.FunnelCfg = sysCfg.FunnelCfg

	machine := &Machine{}
	if err := app.db.Preload("User").Preload("User.Organization").Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}
	domain := officialFunnelDomainForMachine(machine, app.cfg.IPPrefixes, app.cfg.FunnelCfg)
	peerAPIPort, stopPeerAPI := startFunnelPeerAPIServer(t, net.JoinHostPort(domain, strconv.Itoa(listenPort)), true)
	defer stopPeerAPI()

	now := time.Now().UTC()
	hostInfo := machine.GetHostInfo()
	hostInfo.IngressEnabled = true
	hostInfo.Services = []tailcfg.Service{{
		Proto: tailcfg.PeerAPI4,
		Port:  uint16(peerAPIPort),
	}}
	machine.HostInfo = HostInfo(hostInfo)
	machine.LastSeen = &now
	if err := app.db.Save(machine).Error; err != nil {
		t.Fatalf("Save(machine): %v", err)
	}

	cert := newFunnelTestCertificate(t, domain)
	rt := newFunnelRuntime(app)
	rt.newOrgDialer = func(ctx context.Context, orgID int64) (funnelOrgDialer, error) {
		return &funnelIngressTestDialer{}, nil
	}
	rt.getCertFunc = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		return &cert, nil
	}
	app.setFunnelRuntime(rt)
	defer app.setFunnelRuntime(nil)
	if err := rt.start(); err != nil {
		t.Fatalf("runtime.start(): %v", err)
	}
	defer rt.close()

	rt.reload("test")

	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName:         domain,
				InsecureSkipVerify: true, //nolint:gosec
				MinVersion:         tls.VersionTLS12,
			},
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
			},
			ForceAttemptHTTP2: false,
		},
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://%s:%d/hello?x=1", domain, listenPort), nil)
	if err != nil {
		t.Fatalf("http.NewRequest(): %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do(): %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(response): %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, body = %s", resp.StatusCode, string(body))
	}
	if got, want := string(body), "path=/hello query=x=1 host="+domain+":"+strconv.Itoa(listenPort); got != want {
		t.Fatalf("official ingress response = %q, want %q", got, want)
	}
}

func TestFunnelRuntimePrewiresOfficialIngressDNSForWireIntent(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	machine := &Machine{}
	if err := app.db.Preload("User").Preload("User.Organization").Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}

	hostInfo := machine.GetHostInfo()
	hostInfo.WireIngress = true
	hostInfo.IngressEnabled = false
	machine.HostInfo = HostInfo(hostInfo)
	if err := app.db.Save(machine).Error; err != nil {
		t.Fatalf("Save(machine): %v", err)
	}

	fakeDNS := &fakeManagedFunnelDNSProvider{}
	rt := newFunnelRuntime(app)
	rt.resolveManagedDNSProvider = func() (managedFunnelDNSProvider, error) {
		return fakeDNS, nil
	}

	snapshot, _, err := rt.buildSnapshot()
	if err != nil {
		t.Fatalf("buildSnapshot(): %v", err)
	}
	if len(snapshot.TLSPorts) != 0 {
		t.Fatalf("expected no active official ingress routes for wire-only funnel intent")
	}

	wantDomain := strings.TrimSuffix(officialFunnelDomainForMachine(machine, app.cfg.IPPrefixes, app.cfg.FunnelCfg), ".")
	if len(fakeDNS.ensured) != 1 || fakeDNS.ensured[0] != wantDomain {
		t.Fatalf("ensured domains = %#v, want [%q]", fakeDNS.ensured, wantDomain)
	}
}

func TestRequestManagedCertificateAllowsDNS01BeforeRouteActivation(t *testing.T) {
	app := newFunnelTenantTestMirage(t)

	sysCfg := &SysConfig{}
	if err := app.db.First(sysCfg).Error; err != nil {
		t.Fatalf("First(sysCfg): %v", err)
	}
	_, dnsServer := newDNSMgrTestServer(t)
	defer dnsServer.Close()

	sysCfg.FunnelCfg.ManagedDNSProvider = FunnelManagedDNSProviderDNSMgr
	sysCfg.FunnelCfg.ManagedBaseDomain = defaultFunnelDNSMgrBaseDomain
	sysCfg.FunnelCfg.ManagedDNSAPIBaseURL = dnsServer.URL
	sysCfg.FunnelCfg.ManagedDNSUID = 1000
	sysCfg.FunnelCfg.ManagedDNSAPIKey = "secret"
	sysCfg.FunnelCfg.DirectBindPorts = FunnelPortList{880, 4443}
	if err := app.db.Save(sysCfg).Error; err != nil {
		t.Fatalf("Save(sysCfg): %v", err)
	}
	app.cfg.FunnelCfg = sysCfg.FunnelCfg

	owner, err := app.GetUser("owner@example.com", "tenant-org", "Mirage")
	if err != nil {
		t.Fatalf("GetUser(owner): %v", err)
	}
	machine := &Machine{}
	if err := app.db.Where("hostname = ?", "tenant-machine").First(machine).Error; err != nil {
		t.Fatalf("First(machine): %v", err)
	}

	service, domain, _, err := app.createTenantFunnelService(owner, FunnelServiceCreateRequest{
		MachineID:     machine.ID,
		DomainMode:    "managed",
		ListenProto:   FunnelListenProtoHTTPS,
		BackendType:   FunnelBackendTypeHTTPProxy,
		BackendScheme: "http",
		BackendPort:   8080,
	})
	if err != nil {
		t.Fatalf("createTenantFunnelService(): %v", err)
	}
	if got, want := service.ListenPort, 4443; got != want {
		t.Fatalf("service.ListenPort = %d, want %d", got, want)
	}
	if got, want := domain.HTTPSPort, 4443; got != want {
		t.Fatalf("domain.HTTPSPort = %d, want %d", got, want)
	}

	service.ListenPort = 443
	if err := app.db.Save(service).Error; err != nil {
		t.Fatalf("Save(service): %v", err)
	}

	rt := newFunnelRuntime(app)
	snapshot, _, err := rt.buildSnapshot()
	if err != nil {
		t.Fatalf("buildSnapshot(): %v", err)
	}
	rt.snapshot = snapshot
	if !rt.dns01EligibleForHost(domain.Domain) {
		t.Fatalf("dns01EligibleForHost(%q) = false", domain.Domain)
	}
	if ok, reason := rt.dns01ManagedCertificateFallbackAllowed(domain.Domain); !ok {
		t.Fatalf("dns01ManagedCertificateFallbackAllowed(%q) = false: %s", domain.Domain, reason)
	}
	triggered := make(chan string, 1)
	rt.managedCertRequestFunc = func(ctx context.Context, host string) (funnelManagedCertRequestResult, error) {
		triggered <- host
		return funnelManagedCertRequestResult{ChallengeType: FunnelCertChallengeDNS01}, nil
	}

	if err := rt.requestManagedCertificate(domain.Domain); err != nil {
		t.Fatalf("requestManagedCertificate(): %v", err)
	}

	select {
	case got := <-triggered:
		if got != domain.Domain {
			t.Fatalf("certificate host = %q, want %q", got, domain.Domain)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for certificate request")
	}
}

func freeFunnelTestPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(127.0.0.1:0): %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startFunnelTCPBackend(t *testing.T, prefix string) (string, int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(tcp backend): %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
				default:
					t.Logf("funnel test backend accept stopped: %v", err)
				}
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 256)
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				_, _ = conn.Write([]byte(prefix + string(buf[:n])))
			}()
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port, func() {
		close(done)
		_ = ln.Close()
	}
}

func startFunnelPeerAPIServer(t *testing.T, expectedTarget string, backendTLS bool) (int, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(peerapi backend): %v", err)
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v0/ingress" {
				http.NotFound(w, r)
				return
			}
			if got := r.Header.Get("Tailscale-Ingress-Target"); got != expectedTarget {
				http.Error(w, "unexpected ingress target: "+got, http.StatusBadRequest)
				return
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			conn, brw, err := hj.Hijack()
			if err != nil {
				t.Logf("peerapi hijack failed: %v", err)
				return
			}
			defer conn.Close()
			if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n\r\n"); err != nil {
				t.Logf("peerapi write 101 failed: %v", err)
				return
			}

			streamConn := net.Conn(conn)
			streamReader := brw.Reader
			if backendTLS {
				host, _, err := net.SplitHostPort(expectedTarget)
				if err != nil {
					t.Logf("peerapi split target failed: %v", err)
					return
				}
				cert := newFunnelTestCertificate(t, host)
				tlsConn := tls.Server(&funnelBufferedConn{Conn: conn, reader: brw.Reader}, &tls.Config{
					MinVersion:   tls.VersionTLS12,
					Certificates: []tls.Certificate{cert},
				})
				if err := tlsConn.Handshake(); err != nil {
					t.Logf("peerapi tunneled tls handshake failed: %v", err)
					return
				}
				streamConn = tlsConn
				streamReader = bufio.NewReader(tlsConn)
			}

			req, err := http.ReadRequest(streamReader)
			if err != nil {
				t.Logf("peerapi read tunneled request failed: %v", err)
				return
			}
			defer req.Body.Close()

			respBody := fmt.Sprintf("path=%s query=%s host=%s", req.URL.Path, req.URL.RawQuery, req.Host)
			if _, err := io.WriteString(streamConn, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: "+strconv.Itoa(len(respBody))+"\r\n\r\n"+respBody); err != nil {
				t.Logf("peerapi write tunneled response failed: %v", err)
			}
		}),
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			t.Logf("peerapi backend stopped: %v", err)
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port, func() {
		_ = srv.Close()
		_ = ln.Close()
	}
}

func assertFunnelServiceActive(t *testing.T, app *Mirage, service *FunnelService) {
	t.Helper()
	status, err := app.buildTenantFunnelServiceStatus(service)
	if err != nil {
		t.Fatalf("buildTenantFunnelServiceStatus(): %v", err)
	}
	serviceMap, ok := status["service"].(map[string]any)
	if !ok {
		t.Fatalf("status[service] type = %T", status["service"])
	}
	if got := serviceMap["configStatus"]; got != FunnelServiceConfigStatusActive {
		t.Fatalf("configStatus = %#v, want %q", got, FunnelServiceConfigStatusActive)
	}
	if got := serviceMap["edgeStatus"]; got != FunnelServiceEdgeStatusApplied {
		t.Fatalf("edgeStatus = %#v, want %q", got, FunnelServiceEdgeStatusApplied)
	}
}

func newFunnelTestCertificate(t *testing.T, host string) tls.Certificate {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey(): %v", err)
	}
	serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("rand.Int(serial): %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		DNSNames:              []string{host},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("x509.CreateCertificate(): %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("x509.MarshalECPrivateKey(): %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("tls.X509KeyPair(): %v", err)
	}
	return cert
}
