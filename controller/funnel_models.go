package controller

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bwmarrin/snowflake"
	"gorm.io/gorm"
)

const (
	FunnelDomainTypeManaged = "managed"
	FunnelDomainTypeCustom  = "custom"

	FunnelManagedDNSProviderNone   = ""
	FunnelManagedDNSProviderDNSMgr = "dnsmgr"

	FunnelDomainStatusPendingDNS  = "pending_dns"
	FunnelDomainStatusPendingCert = "pending_cert"
	FunnelDomainStatusActive      = "active"
	FunnelDomainStatusError       = "error"
	FunnelDomainStatusDisabled    = "disabled"

	FunnelDNSStatusPending = "pending"
	FunnelDNSStatusReady   = "ready"
	FunnelDNSStatusError   = "error"

	FunnelTLSModePlatformManaged = "platform_managed"
	FunnelTLSModeBringYourOwn    = "bring_your_own_cert"

	FunnelListenerModeDirect      = "direct"
	FunnelListenerModeBehindProxy = "behind_proxy"

	FunnelEdgeModeServer = "server_edge"
	FunnelEdgeModeRemote = "remote_edge"

	FunnelEdgeTypeServer = "server"
	FunnelEdgeTypeNavi   = "navi"

	FunnelEdgeHealthUnknown   = "unknown"
	FunnelEdgeHealthHealthy   = "healthy"
	FunnelEdgeHealthUnhealthy = "unhealthy"

	FunnelCertStatusPending = "pending"
	FunnelCertStatusReady   = "ready"
	FunnelCertStatusError   = "error"
	FunnelCertStatusExpired = "expired"

	FunnelListenProtoHTTP              = "http"
	FunnelListenProtoHTTPS             = "https"
	FunnelListenProtoWS                = "ws"
	FunnelListenProtoWSS               = "wss"
	FunnelListenProtoTCP               = "tcp"
	FunnelListenProtoTLSTerminatedTCP  = "tls_terminated_tcp"
	FunnelBackendTypeHTTPProxy         = "http_proxy"
	FunnelBackendTypeTCPProxy          = "tcp_proxy"
	FunnelServiceConfigStatusPending   = "config_pending"
	FunnelServiceConfigStatusApplying  = "applying"
	FunnelServiceConfigStatusActive    = "active"
	FunnelServiceConfigStatusDegraded  = "degraded"
	FunnelServiceConfigStatusError     = "error"
	FunnelServiceConfigStatusDisabled  = "disabled"
	FunnelServiceEdgeStatusPending     = "pending_edge"
	FunnelServiceEdgeStatusApplied     = "applied"
	FunnelServiceEdgeStatusUnavailable = "edge_unavailable"
	FunnelServiceBackendStatusUnknown  = "unknown"
	FunnelServiceBackendStatusHealthy  = "healthy"
	FunnelServiceBackendStatusDegraded = "degraded"
	FunnelServiceBackendStatusError    = "error"
)

type FunnelPlatformConfig struct {
	ManagedBaseDomain       string         `json:"managedBaseDomain"`
	DefaultEdgeMode         string         `json:"defaultEdgeMode"`
	DefaultListenerMode     string         `json:"defaultListenerMode"`
	DirectBindAddrs         StringList     `json:"directBindAddrs"`
	DirectBindPorts         FunnelPortList `json:"directBindPorts"`
	TrustedProxyCIDRs       StringList     `json:"trustedProxyCIDRs"`
	ManagedDNSProvider      string         `json:"managedDnsProvider"`
	ManagedDNSAPIBaseURL    string         `json:"managedDnsApiBaseUrl"`
	ManagedDNSUID           int64          `json:"managedDnsUid"`
	ManagedDNSAPIKey        string         `json:"managedDnsApiKey"`
	ManagedDNSSkipTLSVerify bool           `json:"managedDnsSkipTlsVerify"`
}

type FunnelPublicAddr struct {
	Network string `json:"network"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type FunnelPublicAddrList []FunnelPublicAddr

type FunnelListenerCapabilities struct {
	SupportsHTTP             bool `json:"supports_http"`
	SupportsWS               bool `json:"supports_ws"`
	SupportsTCP              bool `json:"supports_tcp"`
	SupportsTLSTerminatedTCP bool `json:"supports_tls_terminated_tcp"`
	SupportsProxyProtocol    bool `json:"supports_proxy_protocol"`
	SupportsForwardedHeaders bool `json:"supports_forwarded_headers"`
	SupportsDirectBind       bool `json:"supports_direct_bind"`
}

type FunnelPortList []int

type FunnelDomain struct {
	ID                  int64  `gorm:"primary_key;unique;not null"`
	StableID            string `gorm:"uniqueIndex"`
	OrgID               int64  `gorm:"index;not null"`
	Domain              string `gorm:"uniqueIndex"`
	DomainType          string
	Status              string
	DNSStatus           string
	TLSMode             string
	ListenerMode        string
	EdgeMode            string
	EdgeTargetID        string
	HTTPPort            int
	HTTPSPort           int
	ValidationMethod    string
	ValidationTarget    string
	ValidationToken     string
	ValidationCheckedAt *time.Time
	CertID              *int64
	LastError           string
	LastDNSError        string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type FunnelService struct {
	ID                  int64  `gorm:"primary_key;unique;not null"`
	StableID            string `gorm:"uniqueIndex"`
	OrgID               int64  `gorm:"index;not null"`
	MachineID           int64  `gorm:"index;not null"`
	DomainID            int64  `gorm:"index;not null"`
	Enabled             bool
	Public              bool
	ListenProto         string
	ListenPort          int
	MountPath           string
	BackendType         string
	BackendScheme       string
	BackendTailnetIP    string
	BackendPort         int
	TargetMachineOnline bool
	ConfigStatus        string
	EdgeStatus          string
	BackendStatus       string
	LastError           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type FunnelEdge struct {
	ID                   int64  `gorm:"primary_key;unique;not null"`
	StableID             string `gorm:"uniqueIndex"`
	EdgeType             string
	EdgeNodeID           string `gorm:"index"`
	Hostname             string
	PublicAddrs          FunnelPublicAddrList
	SyncEndpoint         string
	ListenerCapabilities FunnelListenerCapabilities
	HealthStatus         string
	LastSeen             *time.Time
	Allocatable          bool
	TrustProxyCIDRs      StringList
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type FunnelCert struct {
	ID             int64  `gorm:"primary_key;unique;not null"`
	StableID       string `gorm:"uniqueIndex"`
	DomainID       int64  `gorm:"uniqueIndex"`
	Issuer         string
	ChallengeType  string
	CertStatus     string
	CertificateRef string
	PrivateKeyRef  string
	NotBefore      *time.Time
	NotAfter       *time.Time
	RenewAfter     *time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type FunnelAudit struct {
	ID           int64  `gorm:"primary_key;unique;not null"`
	StableID     string `gorm:"uniqueIndex"`
	OrgID        int64  `gorm:"index"`
	ActorType    string
	ActorID      string
	ResourceType string
	ResourceID   string `gorm:"index"`
	Action       string `gorm:"index"`
	Payload      string
	CreatedAt    time.Time
}

func (cfg *FunnelPlatformConfig) Scan(value interface{}) error {
	return scanFunnelJSON(value, cfg)
}

func (cfg FunnelPlatformConfig) Value() (driver.Value, error) {
	return valueFunnelJSON(cfg)
}

func (addrs *FunnelPublicAddrList) Scan(value interface{}) error {
	return scanFunnelJSON(value, addrs)
}

func (addrs FunnelPublicAddrList) Value() (driver.Value, error) {
	return valueFunnelJSON(addrs)
}

func (caps *FunnelListenerCapabilities) Scan(value interface{}) error {
	return scanFunnelJSON(value, caps)
}

func (caps FunnelListenerCapabilities) Value() (driver.Value, error) {
	return valueFunnelJSON(caps)
}

func (ports *FunnelPortList) Scan(value interface{}) error {
	return scanFunnelJSON(value, ports)
}

func (ports FunnelPortList) Value() (driver.Value, error) {
	return valueFunnelJSON(ports)
}

func (domain *FunnelDomain) BeforeCreate(tx *gorm.DB) error {
	return assignFunnelStableID(&domain.ID, &domain.StableID)
}

func (service *FunnelService) BeforeCreate(tx *gorm.DB) error {
	return assignFunnelStableID(&service.ID, &service.StableID)
}

func (edge *FunnelEdge) BeforeCreate(tx *gorm.DB) error {
	return assignFunnelStableID(&edge.ID, &edge.StableID)
}

func (cert *FunnelCert) BeforeCreate(tx *gorm.DB) error {
	return assignFunnelStableID(&cert.ID, &cert.StableID)
}

func (audit *FunnelAudit) BeforeCreate(tx *gorm.DB) error {
	return assignFunnelStableID(&audit.ID, &audit.StableID)
}

func assignFunnelStableID(id *int64, stableID *string) error {
	if *id == 0 {
		flakeID, err := snowflake.NewNode(1)
		if err != nil {
			return err
		}
		*id = flakeID.Generate().Int64()
	}
	*stableID = GetShortId(*id)
	return nil
}

func scanFunnelJSON(value interface{}, target interface{}) error {
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, target)
	case string:
		return json.Unmarshal([]byte(v), target)
	default:
		return fmt.Errorf("%w: unexpected data type %T", ErrMachineAddressesInvalid, value)
	}
}

func valueFunnelJSON(value interface{}) (driver.Value, error) {
	bytes, err := json.Marshal(value)
	return string(bytes), err
}
