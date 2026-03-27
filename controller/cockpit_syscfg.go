package controller

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/dexidp/dex/server"
	"github.com/go-webauthn/webauthn/webauthn"
	"gorm.io/gorm"
)

type SysConfig struct {
	gorm.Model
	//	AdminCredential AdminCredential `gorm:"not null"`

	ServerURL                      string
	ServerKey                      string
	Addr                           string   `gorm:"default:':8080'"`               // default port
	Mip4                           IPPrefix `gorm:"default:'100.64.0.0/10'"`       // default prefix
	Mip6                           IPPrefix `gorm:"default:'fd7a:115c:a1e0::/48'"` // default prefix
	Basedomain                     string   `gorm:"default:'mira.net'"`            // default domain
	DerpUrl                        string   `gorm:"default:'https://controlplane.tailscale.com/derpmap/default'"`
	EphemeralNodeInactivityTimeout time.Duration
	RouteAccessDueMachine          bool `gorm:"default:false"`

	EsUrl string
	EsKey string

	WXScanURL    string
	AggregateCfg AggregateLoginConfig

	SMSConfig SMSConfig

	IdaasConfig ALIConfig

	//	OidcConfig OIDCConfig
	DexSecret string

	MicrosoftCfg MicrosoftCfg
	GithubCfg    GithubCfg
	GiteaCfg     GiteaCfg
	GoogleCfg    GoogleCfg
	AppleCfg     AppleCfg

	NaviDeployPub string
	NaviDeployKey string
	ClientVersion ClientVersionInfo

	CreatedAt time.Time
	UpdatedAt time.Time
}

type GeneralCfg struct {
	SrvAddr                        string        `json:"srvaddr"`
	ServerURL                      string        `json:"server_url"`
	MIPV4                          string        `json:"mipv4"`
	MIPV6                          string        `json:"mipv6"`
	BaseDomain                     string        `json:"basedomain"`
	DERPURL                        string        `json:"derp_url"`
	EphemeralNodeInactivityTimeout time.Duration `json:"ephemeral_node_inactivity_timeout"`
	RouteAccessDueMachine          bool          `json:"route_access_due_machine"`

	ESURL string `json:"es_url"`
	ESKey string `json:"es_key"`

	WXScanURL    string               `json:"wxscan_url"`
	AggregateCfg AggregateLoginConfig `json:"aggregate"`

	SMSConfig   SMSConfig `json:"sms"`
	IDaaSConfig ALIConfig `json:"idaas"`

	MicrosoftCfg MicrosoftCfg `json:"microsoft"`
	GithubCfg    GithubCfg    `json:"github"`
	GiteaCfg     GiteaCfg     `json:"gitea"`
	GoogleCfg    GoogleCfg    `json:"google"`
	AppleCfg     AppleCfg     `json:"apple"`

	NaviDeployPub string            `json:"navi_deploy_pub"`
	ClientVersion ClientVersionInfo `json:"client_version"`
}

func (s *SysConfig) toGeneralCfg() GeneralCfg {
	return GeneralCfg{
		SrvAddr:                        s.Addr,
		ServerURL:                      s.ServerURL,
		MIPV4:                          s.Mip4.String(),
		MIPV6:                          s.Mip6.String(),
		BaseDomain:                     s.Basedomain,
		DERPURL:                        normalizeDERPMapURL(s.DerpUrl),
		EphemeralNodeInactivityTimeout: normalizeEphemeralNodeInactivityTimeout(s.EphemeralNodeInactivityTimeout),
		RouteAccessDueMachine:          s.RouteAccessDueMachine,

		ESURL: s.EsUrl,
		ESKey: s.EsKey,

		WXScanURL:    s.WXScanURL,
		AggregateCfg: s.AggregateCfg,

		SMSConfig:    s.SMSConfig,
		IDaaSConfig:  s.IdaasConfig,
		MicrosoftCfg: s.MicrosoftCfg,
		GithubCfg:    s.GithubCfg,
		GiteaCfg:     s.GiteaCfg,
		GoogleCfg:    s.GoogleCfg,
		AppleCfg:     s.AppleCfg,

		NaviDeployPub: s.NaviDeployPub,
		ClientVersion: s.ClientVersion,
	}
}

func (s *SysConfig) hasDexOIDCProvider() bool {
	return (s.MicrosoftCfg.ClientID != "" && s.MicrosoftCfg.ClientSecret != "") ||
		(s.GithubCfg.ClientID != "" && s.GithubCfg.ClientSecret != "") ||
		(s.GiteaCfg.ClientID != "" && s.GiteaCfg.ClientSecret != "") ||
		(s.GoogleCfg.ClientID != "" && s.GoogleCfg.ClientSecret != "") ||
		(s.AppleCfg.ClientID != "" && s.AppleCfg.KeyID != "" && s.AppleCfg.TeamID != "" && s.AppleCfg.PrivateKey != "")
}

func (s *SysConfig) toSrvConfig() (*Config, error) {
	idps := []string{}
	if s.MicrosoftCfg.ClientID != "" && s.MicrosoftCfg.ClientSecret != "" {
		idps = append(idps, "Microsoft")
	}
	if s.GithubCfg.ClientID != "" && s.GithubCfg.ClientSecret != "" {
		idps = append(idps, "Github")
	}
	if s.GiteaCfg.ClientID != "" && s.GiteaCfg.ClientSecret != "" {
		idps = append(idps, "Gitea")
	}
	if s.GoogleCfg.ClientID != "" && s.GoogleCfg.ClientSecret != "" {
		idps = append(idps, "Google")
	}
	if s.AppleCfg.ClientID != "" && s.AppleCfg.KeyID != "" && s.AppleCfg.TeamID != "" && s.AppleCfg.PrivateKey != "" {
		idps = append(idps, "Apple")
	}
	if s.WXScanURL != "" {
		idps = append(idps, "WeChat")
	}
	if s.AggregateCfg.Configured() {
		idps = append(idps, "Aggregator")
	}

	var (
		dexCfg     *server.Config
		err        error
		oidcConfig OIDCConfig
	)
	if s.hasDexOIDCProvider() {
		dexCfg, err = s.toDexConfig()
		if err != nil {
			return nil, err
		}
		oidcConfig = OIDCConfig{
			Issuer:       "https://" + s.ServerURL + "/issuer",
			ClientID:     "MirageServer",
			ClientSecret: s.DexSecret,
			Scope:        []string{"offline_access", "openid", "profile", "email", "groups", "name"},
			ExtraParams:  map[string]string{"prompt": "login"},
		}
	}

	return &Config{
		ServerURL:                      s.ServerURL,
		Addr:                           s.Addr,
		IPPrefixes:                     []netip.Prefix{netip.Prefix(s.Mip4), netip.Prefix(s.Mip6)},
		BaseDomain:                     s.Basedomain,
		DERPURL:                        normalizeDERPMapURL(s.DerpUrl),
		EphemeralNodeInactivityTimeout: normalizeEphemeralNodeInactivityTimeout(s.EphemeralNodeInactivityTimeout),
		AllowRouteDueToMachine:         s.RouteAccessDueMachine,

		ESURL: s.EsUrl,
		ESKey: s.EsKey,

		wxScanURL:      s.WXScanURL,
		AggregateLogin: s.AggregateCfg,

		SMS:       s.SMSConfig,
		IDaaS:     s.IdaasConfig,
		OIDC:      oidcConfig,
		DexConfig: dexCfg,
		IdpList:   idps,

		ClientVersion: s.ClientVersion,
	}, nil
}

type AdminCredential webauthn.Credential

func (ac *AdminCredential) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, ac)
	case string:
		return json.Unmarshal([]byte(v), ac)
	default:
		return fmt.Errorf("cannot parse admin credential: unexpected data type %T", value)
	}
}

func (ac AdminCredential) Value() (driver.Value, error) {
	bytes, err := json.Marshal(ac)
	return string(bytes), err
}

type MicrosoftCfg struct {
	ApplicationID string `json:"app_id"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
}

func (mscfg *MicrosoftCfg) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, mscfg)
	case string:
		return json.Unmarshal([]byte(v), mscfg)
	default:
		return fmt.Errorf("cannot parse microsoft config: unexpected data type %T", value)
	}
}

func (mscfg MicrosoftCfg) Value() (driver.Value, error) {
	bytes, err := json.Marshal(mscfg)
	return string(bytes), err
}

type GithubCfg struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func (ghCfg *GithubCfg) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, ghCfg)
	case string:
		return json.Unmarshal([]byte(v), ghCfg)
	default:
		return fmt.Errorf("cannot parse github config: unexpected data type %T", value)
	}
}

func (ghCfg GithubCfg) Value() (driver.Value, error) {
	bytes, err := json.Marshal(ghCfg)
	return string(bytes), err
}

type GiteaCfg struct {
	BaseURL      string `json:"base_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func (giteaCfg *GiteaCfg) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, giteaCfg)
	case string:
		return json.Unmarshal([]byte(v), giteaCfg)
	default:
		return fmt.Errorf("cannot parse gitea config: unexpected data type %T", value)
	}
}

func (giteaCfg GiteaCfg) Value() (driver.Value, error) {
	bytes, err := json.Marshal(giteaCfg)
	return string(bytes), err
}

type GoogleCfg struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func (ghCfg *GoogleCfg) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, ghCfg)
	case string:
		return json.Unmarshal([]byte(v), ghCfg)
	default:
		return fmt.Errorf("cannot parse github config: unexpected data type %T", value)
	}
}

func (ghCfg GoogleCfg) Value() (driver.Value, error) {
	bytes, err := json.Marshal(ghCfg)
	return string(bytes), err
}

type AppleCfg struct {
	ClientID   string `json:"client_id"`
	TeamID     string `json:"team_id"`
	KeyID      string `json:"key_id"`
	PrivateKey string `json:"private_key"`
}

func (ghCfg *AppleCfg) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, ghCfg)
	case string:
		return json.Unmarshal([]byte(v), ghCfg)
	default:
		return fmt.Errorf("cannot parse github config: unexpected data type %T", value)
	}
}

func (ghCfg AppleCfg) Value() (driver.Value, error) {
	bytes, err := json.Marshal(ghCfg)
	return string(bytes), err
}

type ClientVersionInfo struct {
	NaviAMD64     string         `json:"naviAmd64"`
	NaviAARCH64   string         `json:"naviAarch64"`
	Win           ClientVer      `json:"win"`
	MacStore      ClientVer      `json:"mac_store"`
	MacTestFlight ClientVer      `json:"mac_test"`
	Linux         LinuxBuildInfo `json:"linux"`
	Android       ClientVer      `json:"android"`
	IOSStore      ClientVer      `json:"ios_store"`
	IOSTestFlight ClientVer      `json:"ios_test"`
}

func (c *ClientVersionInfo) Scan(value interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, c)
	case string:
		return json.Unmarshal([]byte(v), c)
	default:
		return fmt.Errorf("cannot parse client version info: unexpected data type %T", value)
	}
}

func (c ClientVersionInfo) Value() (driver.Value, error) {
	bytes, err := json.Marshal(c)
	return string(bytes), err
}

type ClientVer struct {
	Version string `json:"version"`
	Url     string `json:"url"`
}

type LinuxBuildInfo struct {
	Version    string `json:"version"`   // 当前版本号
	Url        string `json:"url"`       // 当前仓库地址
	RepoCred   string `json:"repo_cred"` // 当前仓库凭据
	Hash       string `json:"hash"`      // 当前发布hash
	BuildState string `json:"buildst"`   // 最近一次构建状态 - 尚未进行、正在进行、成功、失败
}
