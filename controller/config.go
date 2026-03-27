package controller

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/dexidp/dex/server"
)

const (
	JSONLogFormat = "json"
	TextLogFormat = "text"

	defaultOIDCExpiryTime               = 180 * 24 * time.Hour // 180 Days
	maxDuration           time.Duration = 1<<63 - 1
)

// Config contains the initial Mirage configuration.
type Config struct {
	ServerURL  string         //DONE
	Addr       string         //DONE
	IPPrefixes []netip.Prefix //DONE
	BaseDomain string         //DONE

	AllowRouteDueToMachine bool //DONE

	DERPURL                        string //DONE
	EphemeralNodeInactivityTimeout time.Duration

	ESURL string
	ESKey string

	OIDC OIDCConfig

	wxScanURL      string
	AggregateLogin AggregateLoginConfig

	IDaaS ALIConfig
	SMS   SMSConfig

	DexConfig *server.Config
	IdpList   []string

	ClientVersion ClientVersionInfo
}

func (c *Config) HasDexOIDCProvider() bool {
	return c != nil && c.DexConfig != nil && c.OIDC.Issuer != ""
}

func (c *Config) SelfRegistrationEnabled() bool {
	if c == nil {
		return false
	}

	return strings.TrimSpace(c.IDaaS.App) != "" &&
		strings.TrimSpace(c.IDaaS.ClientID) != "" &&
		strings.TrimSpace(c.IDaaS.ClientKey) != "" &&
		strings.TrimSpace(c.IDaaS.Instance) != "" &&
		strings.TrimSpace(c.IDaaS.OrgID) != "" &&
		strings.TrimSpace(c.SMS.ID) != "" &&
		strings.TrimSpace(c.SMS.Key) != "" &&
		strings.TrimSpace(c.SMS.Sign) != "" &&
		strings.TrimSpace(c.SMS.Template) != ""
}

type SMSConfig struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Sign     string `json:"sign"`
	Template string `json:"template"`
}

func (ac *SMSConfig) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, ac)
	case string:
		return json.Unmarshal([]byte(v), ac)
	default:
		return fmt.Errorf("cannot parse SMS Config: unexpected data type %T", value)
	}
}

func (ac SMSConfig) Value() (driver.Value, error) {
	bytes, err := json.Marshal(ac)
	return string(bytes), err
}

type ALIConfig struct {
	App       string `json:"app"`
	ClientID  string `json:"id"`
	ClientKey string `json:"key"`
	Instance  string `json:"instance"`
	OrgID     string `json:"org"`
}

func (ac *ALIConfig) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, ac)
	case string:
		return json.Unmarshal([]byte(v), ac)
	default:
		return fmt.Errorf("cannot parse Ali IDaaS Config: unexpected data type %T", value)
	}
}

func (ac ALIConfig) Value() (driver.Value, error) {
	bytes, err := json.Marshal(ac)
	return string(bytes), err
}

type AggregateLoginConfig struct {
	BaseURL    string   `json:"base_url"`
	AppID      string   `json:"app_id"`
	AppKey     string   `json:"app_key"`
	LoginTypes []string `json:"login_types"`
}

func normalizeAggregateLoginTypes(loginTypes []string) []string {
	if len(loginTypes) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(loginTypes))
	normalized := make([]string, 0, len(loginTypes))
	for _, loginType := range loginTypes {
		loginType = strings.TrimSpace(loginType)
		if loginType == "" {
			continue
		}
		if _, ok := seen[loginType]; ok {
			continue
		}
		seen[loginType] = struct{}{}
		normalized = append(normalized, loginType)
	}
	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func (ac AggregateLoginConfig) Configured() bool {
	return strings.TrimSpace(ac.BaseURL) != "" && ac.AppID != "" && ac.AppKey != "" && len(normalizeAggregateLoginTypes(ac.LoginTypes)) > 0
}

func (ac AggregateLoginConfig) SupportsLoginType(loginType string) bool {
	loginType = strings.TrimSpace(loginType)
	if loginType == "" {
		return false
	}

	for _, candidate := range normalizeAggregateLoginTypes(ac.LoginTypes) {
		if candidate == loginType {
			return true
		}
	}

	return false
}

func (ac AggregateLoginConfig) FirstLoginType() string {
	loginTypes := normalizeAggregateLoginTypes(ac.LoginTypes)
	if len(loginTypes) == 0 {
		return ""
	}

	return loginTypes[0]
}

func (ac AggregateLoginConfig) ConnectURL() (string, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(ac.BaseURL))
	if err != nil {
		return "", err
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return "", fmt.Errorf("invalid aggregate base url")
	}

	trimmedPath := strings.TrimSuffix(parsedURL.Path, "/")
	if trimmedPath == "" {
		parsedURL.Path = "/connect.php"
	} else if strings.HasSuffix(trimmedPath, "/connect.php") || trimmedPath == "connect.php" {
		parsedURL.Path = trimmedPath
	} else {
		parsedURL.Path = trimmedPath + "/connect.php"
	}
	parsedURL.RawPath = ""
	parsedURL.RawQuery = ""
	parsedURL.Fragment = ""

	return parsedURL.String(), nil
}

func (ac *AggregateLoginConfig) Scan(value interface{}) error {
	var rawBytes []byte
	switch v := value.(type) {
	case nil:
		*ac = AggregateLoginConfig{}
		return nil
	case []byte:
		rawBytes = v
	case string:
		rawBytes = []byte(v)
	default:
		return fmt.Errorf("cannot parse aggregate login config: unexpected data type %T", value)
	}

	type aggregateLoginConfigLegacy struct {
		BaseURL    string   `json:"base_url"`
		AppID      string   `json:"app_id"`
		AppKey     string   `json:"app_key"`
		LoginType  string   `json:"login_type"`
		LoginTypes []string `json:"login_types"`
	}

	legacy := aggregateLoginConfigLegacy{}
	if err := json.Unmarshal(rawBytes, &legacy); err != nil {
		return err
	}

	loginTypes := legacy.LoginTypes
	if len(loginTypes) == 0 && legacy.LoginType != "" {
		loginTypes = []string{legacy.LoginType}
	}

	*ac = AggregateLoginConfig{
		BaseURL:    legacy.BaseURL,
		AppID:      legacy.AppID,
		AppKey:     legacy.AppKey,
		LoginTypes: normalizeAggregateLoginTypes(loginTypes),
	}

	return nil
}

func (ac AggregateLoginConfig) Value() (driver.Value, error) {
	ac.LoginTypes = normalizeAggregateLoginTypes(ac.LoginTypes)
	bytes, err := json.Marshal(ac)
	return string(bytes), err
}

func aggregateLoginConfigFromMap(raw map[string]interface{}) (AggregateLoginConfig, error) {
	cfg := AggregateLoginConfig{}

	baseURL, ok := raw["base_url"].(string)
	if !ok {
		return cfg, fmt.Errorf("用户请求Aggregate.base_url解析失败")
	}
	appID, ok := raw["app_id"].(string)
	if !ok {
		return cfg, fmt.Errorf("用户请求Aggregate.app_id解析失败")
	}
	appKey, ok := raw["app_key"].(string)
	if !ok {
		return cfg, fmt.Errorf("用户请求Aggregate.app_key解析失败")
	}

	loginTypes := []string{}
	if loginTypesRaw, exists := raw["login_types"]; exists {
		switch typed := loginTypesRaw.(type) {
		case []interface{}:
			for _, item := range typed {
				loginType, ok := item.(string)
				if !ok {
					return cfg, fmt.Errorf("用户请求Aggregate.login_types解析失败")
				}
				loginTypes = append(loginTypes, loginType)
			}
		case []string:
			loginTypes = append(loginTypes, typed...)
		default:
			return cfg, fmt.Errorf("用户请求Aggregate.login_types解析失败")
		}
	} else if legacyLoginType, ok := raw["login_type"].(string); ok && legacyLoginType != "" {
		loginTypes = []string{legacyLoginType}
	}

	cfg = AggregateLoginConfig{
		BaseURL:    baseURL,
		AppID:      appID,
		AppKey:     appKey,
		LoginTypes: normalizeAggregateLoginTypes(loginTypes),
	}

	return cfg, nil
}

type OIDCConfig struct {
	Issuer           string            `json:"issuer"`
	ClientID         string            `json:"id"`
	ClientSecret     string            `json:"key"`
	Scope            []string          `json:"scope"`
	ExtraParams      map[string]string `json:"extra"`
	StripEmaildomain bool              `json:"strip_flag"`
}

func (ac *OIDCConfig) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, ac)
	case string:
		return json.Unmarshal([]byte(v), ac)
	default:
		return fmt.Errorf("cannot parse OIDC Config: unexpected data type %T", value)
	}
}

func (ac OIDCConfig) Value() (driver.Value, error) {
	bytes, err := json.Marshal(ac)
	return string(bytes), err
}
