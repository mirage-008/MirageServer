package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/patrickmn/go-cache"
)

func TestSysConfigToSrvConfigWithoutDexProviders(t *testing.T) {
	t.Parallel()

	sysCfg := SysConfig{
		ServerURL:  "ctrl.example.test",
		Addr:       "127.0.0.1:8080",
		Mip4:       mustIPPrefix(t, "100.64.0.0/10"),
		Mip6:       mustIPPrefix(t, "fd7a:115c:a1e0::/48"),
		Basedomain: "mira.net",
		WXScanURL:  "https://wx.example.test",
	}

	cfg, err := sysCfg.toSrvConfig()
	if err != nil {
		t.Fatalf("toSrvConfig returned error: %v", err)
	}
	if cfg.DexConfig != nil {
		t.Fatalf("expected DexConfig to be nil when no Dex providers are configured")
	}
	if cfg.OIDC.Issuer != "" {
		t.Fatalf("expected OIDC issuer to be empty, got %q", cfg.OIDC.Issuer)
	}
	if len(cfg.IdpList) != 1 || cfg.IdpList[0] != "WeChat" {
		t.Fatalf("unexpected IdpList: %#v", cfg.IdpList)
	}
}

func TestDoLoginRejectsUnconfiguredDexProvider(t *testing.T) {
	t.Parallel()

	m := &Mirage{
		cfg: &Config{
			ServerURL: "ctrl.example.test",
		},
		aCodeCache:     cache.New(0, 0),
		stateCodeCache: cache.New(0, 0),
	}

	form := url.Values{
		"provider": []string{"Github"},
		"next_url": []string{"/admin"},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.doLogin(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

func TestDoLoginRejectsUnknownProvider(t *testing.T) {
	t.Parallel()

	m := &Mirage{
		cfg: &Config{
			ServerURL: "ctrl.example.test",
		},
		aCodeCache:     cache.New(0, 0),
		stateCodeCache: cache.New(0, 0),
	}

	form := url.Values{
		"provider": []string{"Unknown"},
		"next_url": []string{"/admin"},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.doLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestDoLoginRejectsUnconfiguredGiteaProvider(t *testing.T) {
	t.Parallel()

	m := &Mirage{
		cfg: &Config{
			ServerURL: "ctrl.example.test",
		},
		aCodeCache:     cache.New(0, 0),
		stateCodeCache: cache.New(0, 0),
	}

	form := url.Values{
		"provider": []string{"Gitea"},
		"next_url": []string{"/admin"},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.doLogin(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

func TestSysConfigToSrvConfigIncludesGiteaProvider(t *testing.T) {
	t.Parallel()

	sysCfg := SysConfig{
		ServerURL:  "ctrl.example.test",
		Addr:       "127.0.0.1:8080",
		Mip4:       mustIPPrefix(t, "100.64.0.0/10"),
		Mip6:       mustIPPrefix(t, "fd7a:115c:a1e0::/48"),
		Basedomain: "mira.net",
		GiteaCfg: GiteaCfg{
			BaseURL:      "https://gitea.example.test",
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		},
	}

	cfg, err := sysCfg.toSrvConfig()
	if err != nil {
		t.Fatalf("toSrvConfig returned error: %v", err)
	}
	if cfg.DexConfig == nil {
		t.Fatal("expected DexConfig to be initialized when Gitea is configured")
	}
	if !containsIDP(cfg.IdpList, "Gitea") {
		t.Fatalf("expected IdpList to include Gitea, got %#v", cfg.IdpList)
	}
	if cfg.OIDC.Issuer != "https://ctrl.example.test/issuer" {
		t.Fatalf("unexpected OIDC issuer: %q", cfg.OIDC.Issuer)
	}
}

func TestSysConfigToSrvConfigIncludesAggregatorProvider(t *testing.T) {
	t.Parallel()

	sysCfg := SysConfig{
		ServerURL:  "ctrl.example.test",
		Addr:       "127.0.0.1:8080",
		Mip4:       mustIPPrefix(t, "100.64.0.0/10"),
		Mip6:       mustIPPrefix(t, "fd7a:115c:a1e0::/48"),
		Basedomain: "mira.net",
		AggregateCfg: AggregateLoginConfig{
			BaseURL:    "https://aggregate.example.test/entry",
			AppID:      "app-id",
			AppKey:     "app-key",
			LoginTypes: []string{"qq", "wx"},
		},
	}

	cfg, err := sysCfg.toSrvConfig()
	if err != nil {
		t.Fatalf("toSrvConfig returned error: %v", err)
	}
	if cfg.DexConfig != nil {
		t.Fatal("expected DexConfig to remain nil when only aggregator is configured")
	}
	if !containsIDP(cfg.IdpList, "Aggregator") {
		t.Fatalf("expected IdpList to include Aggregator, got %#v", cfg.IdpList)
	}
	if !cfg.AggregateLogin.Configured() {
		t.Fatal("expected AggregateLogin config to be populated")
	}
}

func TestDoLoginRejectsUnconfiguredAggregatorProvider(t *testing.T) {
	t.Parallel()

	m := &Mirage{
		cfg: &Config{
			ServerURL: "ctrl.example.test",
		},
		aCodeCache:     cache.New(0, 0),
		stateCodeCache: cache.New(0, 0),
	}

	form := url.Values{
		"provider":       []string{"Aggregator"},
		"next_url":       []string{"/admin"},
		"aggregate_type": []string{"qq"},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.doLogin(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

func TestDoLoginRejectsUnsupportedAggregatorType(t *testing.T) {
	t.Parallel()

	m := &Mirage{
		cfg: &Config{
			ServerURL: "ctrl.example.test",
			AggregateLogin: AggregateLoginConfig{
				BaseURL:    "https://aggregate.example.test/entry",
				AppID:      "app-id",
				AppKey:     "app-key",
				LoginTypes: []string{"qq", "wx"},
			},
		},
		aCodeCache:     cache.New(0, 0),
		stateCodeCache: cache.New(0, 0),
	}

	form := url.Values{
		"provider":       []string{"Aggregator"},
		"next_url":       []string{"/admin"},
		"aggregate_type": []string{"alipay"},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.doLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestDoLoginAggregatorStartRedirects(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connect.php" {
			t.Fatalf("expected request path /connect.php, got %q", r.URL.Path)
		}
		if r.URL.Query().Get("act") != "login" {
			t.Fatalf("expected act=login, got %q", r.URL.Query().Get("act"))
		}
		if r.URL.Query().Get("appid") != "app-id" {
			t.Fatalf("unexpected appid: %q", r.URL.Query().Get("appid"))
		}
		if r.URL.Query().Get("appkey") != "app-key" {
			t.Fatalf("unexpected appkey: %q", r.URL.Query().Get("appkey"))
		}
		if r.URL.Query().Get("type") != "qq" {
			t.Fatalf("unexpected type: %q", r.URL.Query().Get("type"))
		}

		redirectURI := r.URL.Query().Get("redirect_uri")
		parsedRedirectURI, err := url.Parse(redirectURI)
		if err != nil {
			t.Fatalf("Parse redirect_uri: %v", err)
		}
		if parsedRedirectURI.Scheme != "https" || parsedRedirectURI.Host != "ctrl.example.test" || parsedRedirectURI.Path != "/a/oauth_response" {
			t.Fatalf("unexpected redirect_uri: %q", redirectURI)
		}
		if parsedRedirectURI.Query().Get("state") == "" {
			t.Fatal("expected redirect_uri to include state")
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"url":  "https://aggregate.example.test/authorize?ticket=abc",
		})
	}))
	defer upstream.Close()

	m := &Mirage{
		cfg: &Config{
			ServerURL: "ctrl.example.test",
			AggregateLogin: AggregateLoginConfig{
				BaseURL:    upstream.URL,
				AppID:      "app-id",
				AppKey:     "app-key",
				LoginTypes: []string{"qq", "wx"},
			},
		},
		aCodeCache:     cache.New(0, 0),
		stateCodeCache: cache.New(0, 0),
	}

	form := url.Values{
		"provider":       []string{"Aggregator"},
		"next_url":       []string{"/admin"},
		"aggregate_type": []string{"qq"},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.doLogin(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected status %d, got %d", http.StatusFound, rec.Code)
	}
	if location := rec.Header().Get("Location"); location != "https://aggregate.example.test/authorize?ticket=abc" {
		t.Fatalf("unexpected redirect location: %q", location)
	}
}

func TestAggregateLoginConfigScanBackfillsLegacySingleType(t *testing.T) {
	t.Parallel()

	var cfg AggregateLoginConfig
	if err := cfg.Scan(`{"base_url":"https://aggregate.example.test","app_id":"app-id","app_key":"app-key","login_type":"qq"}`); err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	want := AggregateLoginConfig{
		BaseURL:    "https://aggregate.example.test",
		AppID:      "app-id",
		AppKey:     "app-key",
		LoginTypes: []string{"qq"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestAggregateIdentityFromResponse(t *testing.T) {
	t.Parallel()

	userName, userDisName, orgName, err := aggregateIdentityFromResponse("qq", map[string]interface{}{
		"type":       "qq",
		"social_uid": "10001",
		"nickname":   "测试用户",
	})
	if err != nil {
		t.Fatalf("aggregateIdentityFromResponse returned error: %v", err)
	}
	if userName != "qq:10001" {
		t.Fatalf("unexpected userName: %q", userName)
	}
	if userDisName != "测试用户" {
		t.Fatalf("unexpected userDisName: %q", userDisName)
	}
	if orgName != "qq:10001.Aggregator" {
		t.Fatalf("unexpected orgName: %q", orgName)
	}
}

func containsIDP(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func mustIPPrefix(t *testing.T, prefix string) IPPrefix {
	t.Helper()

	parsed, err := netip.ParsePrefix(prefix)
	if err != nil {
		t.Fatalf("ParsePrefix(%q): %v", prefix, err)
	}

	return IPPrefix(parsed)
}
