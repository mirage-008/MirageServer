package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

type funnelTestDialer struct{}

func (d *funnelTestDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func (d *funnelTestDialer) Close() error { return nil }

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
