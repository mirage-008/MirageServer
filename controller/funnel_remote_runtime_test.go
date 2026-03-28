package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestFunnelRemoteRuntimeAppliesHTTPPayload(t *testing.T) {
	t.Parallel()

	listenPort := freeFunnelTestPort(t)
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

	rt := newFunnelRemoteRuntime(context.Background(), []string{"127.0.0.1"})
	defer rt.close()

	if err := rt.applyPayload(&FunnelEdgeSyncPayload{
		Services: []FunnelEdgeSyncService{
			{
				OrgID:   1,
				Service: map[string]any{"id": "101"},
				Public: FunnelEdgeSyncPublic{
					Host:        "remote-http.example.test",
					ListenProto: FunnelListenProtoHTTP,
					ListenPort:  listenPort,
					MountPath:   "/app",
				},
				Backend: FunnelEdgeSyncBackend{
					Network: "tcp",
					Address: backendURL.Hostname(),
					Port:    backendPort,
					Scheme:  "http",
				},
			},
		},
	}); err != nil {
		t.Fatalf("applyPayload(http): %v", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	var (
		respBody string
		lastErr  error
	)
	for attempt := 0; attempt < 30; attempt++ {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/app/hello?x=1", listenPort), nil)
		if err != nil {
			t.Fatalf("http.NewRequest(): %v", err)
		}
		req.Host = "remote-http.example.test"
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
		t.Fatalf("request through remote runtime failed: %v", lastErr)
	}
	if want := "path=/hello query=x=1 host=remote-http.example.test"; respBody != want {
		t.Fatalf("runtime response = %q, want %q", respBody, want)
	}
}

func TestFunnelRemoteRuntimeAppliesHTTPSPayload(t *testing.T) {
	t.Parallel()

	listenPort := freeFunnelTestPort(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf("tls-path=%s host=%s", r.URL.Path, r.Host))
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

	rt := newFunnelRemoteRuntime(context.Background(), []string{"127.0.0.1"})
	rt.getCertFunc = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert := newFunnelTestCertificate(t, "remote-https.example.test")
		return &cert, nil
	}
	defer rt.close()

	if err := rt.applyPayload(&FunnelEdgeSyncPayload{
		Services: []FunnelEdgeSyncService{
			{
				OrgID:   1,
				Service: map[string]any{"id": "202"},
				Public: FunnelEdgeSyncPublic{
					Host:        "remote-https.example.test",
					ListenProto: FunnelListenProtoHTTPS,
					ListenPort:  listenPort,
					MountPath:   "/secure",
				},
				Backend: FunnelEdgeSyncBackend{
					Network: "tcp",
					Address: backendURL.Hostname(),
					Port:    backendPort,
					Scheme:  "http",
				},
			},
		},
	}); err != nil {
		t.Fatalf("applyPayload(https): %v", err)
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         "remote-https.example.test",
			InsecureSkipVerify: true, //nolint:gosec
			MinVersion:         tls.VersionTLS12,
		},
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}

	var (
		respBody string
		lastErr  error
	)
	for attempt := 0; attempt < 30; attempt++ {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/secure/hello", listenPort), nil)
		if err != nil {
			t.Fatalf("http.NewRequest(): %v", err)
		}
		req.Host = "remote-https.example.test"
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
		t.Fatalf("request through remote TLS runtime failed: %v", lastErr)
	}
	if want := "tls-path=/hello host=remote-https.example.test"; respBody != want {
		t.Fatalf("runtime response = %q, want %q", respBody, want)
	}
}

func TestFunnelRemoteRuntimeRejectsTCPPayload(t *testing.T) {
	t.Parallel()

	rt := newFunnelRemoteRuntime(context.Background(), []string{"127.0.0.1"})
	defer rt.close()

	err := rt.applyPayload(&FunnelEdgeSyncPayload{
		Services: []FunnelEdgeSyncService{
			{
				Public: FunnelEdgeSyncPublic{
					Host:        "remote-tcp.example.test",
					ListenProto: FunnelListenProtoTCP,
					ListenPort:  freeFunnelTestPort(t),
				},
				Backend: FunnelEdgeSyncBackend{
					Network: "tcp",
					Address: "100.64.0.10",
					Port:    5432,
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected tcp payload to be rejected")
	}
}
