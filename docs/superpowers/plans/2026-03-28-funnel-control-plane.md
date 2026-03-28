# Mirage Funnel Control Plane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Mirage Funnel control-plane persistence, super-admin and tenant CRUD APIs, and cockpit/console management pages for managed-domain publication and deferred custom-domain onboarding, without yet binding public listeners.

**Architecture:** Keep Funnel source-of-truth in `MirageServer/controller` by persisting platform config, domains, services, cert metadata, edges, and audit rows through GORM. This slice stops before `server-edge` runtime, DNS verification workers, and client intent sync, so created services remain in projected pre-runtime states (`pending_dns`, `pending_cert`, `config_pending`, `pending_edge`) but use the final API shapes and stable IDs needed by later plans.

**Tech Stack:** Go, GORM/SQLite, Gorilla Mux, Vue 3, Vite, existing Mirage API envelope, existing cockpit/console build pipeline

---

## Scope Boundaries

This is plan `1/7` from the Funnel umbrella spec decomposition:

1. Control-plane schema, APIs, and Cockpit platform settings
2. Client and daemon intent-sync integration for managed-domain publication
3. `server-edge` runtime for managed-domain `HTTP/HTTPS/WS/WSS`
4. `TCP` and `TLS-terminated-TCP` runtime support
5. Custom-domain verification and platform-managed certificate activation
6. `remote-edge` runtime and sync model
7. BYOC certificate ingestion and lifecycle operations

This plan intentionally does **not** implement:

- public listener binding
- HTTP/WebSocket/TCP forwarding
- ACME or other certificate issuance workers
- real DNS verification
- remote-edge sync
- CLI/daemon `serve/funnel` reconciliation

Deferred APIs should still exist when they help lock the contract, but they must return stable deferred behavior instead of pretending the feature is active.

Cockpit domain/certificate operations in this slice are API-first and may be rendered as a read-only table plus explicit “deferred” action buttons on the same Funnel settings page. A separate dedicated cockpit domain/cert workflow page is not required until a later runtime/certificate plan.

Authorization rule for this plan:

- Cockpit Funnel APIs remain protected by existing cockpit super-admin auth only.
- Tenant read APIs are available to any logged-in tenant member with console access.
- Tenant mutation APIs (`POST`, `PATCH`, `DELETE`, enable, disable, verify) must require the current Mirage owner gate (`user.Role == RoleOwner`) until the product grows a distinct tenant-admin role model.

## File Structure

### Existing files to modify

- `controller/db.go`
  Registers Funnel tables in the existing GORM bootstrap path so both cockpit and controller startups create the schema.
- `controller/cockpit_syscfg.go`
  Persists platform-wide Funnel settings inside `SysConfig` and exposes normalized values for the cockpit API.
- `controller/cockpit.go`
  Wires super-admin Funnel routes into the existing cockpit router.
- `controller/app.go`
  Wires tenant Funnel routes into the existing `/admin/api` router.
- `cockpit_web/src/Settings.vue`
  Adds the Funnel settings entry beside the existing general/authority/client sections.
- `console_web/src/admin.js`
  Registers the tenant Funnel route.
- `console_web/src/Console.vue`
  Adds the tenant nav entry for public services.

### New files to create

- `controller/funnel_models.go`
  Funnel table models, status constants, JSON-backed config types, and stable-ID hooks.
- `controller/funnel_models_test.go`
  Schema and normalization tests for the new models.
- `controller/funnel_platform.go`
  Platform config normalization, managed-domain naming, status projection, and validation helpers shared by cockpit and tenant APIs.
- `controller/funnel_platform_test.go`
  Focused tests for config normalization, managed-domain allocation, and request validation helpers.
- `controller/cockpit_api_funnel.go`
  Super-admin Funnel config and edge inventory APIs.
- `controller/cockpit_api_funnel_test.go`
  HTTP-level tests for `/cockpit/api/funnel/*`.
- `controller/console_api_funnel.go`
  Tenant domain/service CRUD, projected status responses, and deferred verify/log endpoints.
- `controller/console_api_funnel_test.go`
  HTTP-level tests for `/admin/api/funnel/*`.
- `cockpit_web/src/setpart/Funnel.vue`
  Cockpit page for platform base-domain and ingress-default settings plus edge inventory display.
- `console_web/src/components/Funnel.vue`
  Tenant page for public domains and public services.

### Data ownership for this plan

- `SysConfig.FunnelCfg`
  Platform defaults and operator-chosen ingress settings.
- `FunnelDomain`
  Domain inventory and projected DNS/cert readiness.
- `FunnelService`
  Tenant-owned public-service config and projected runtime status.
- `FunnelEdge`
  Edge inventory rows. In this slice, at least one synthetic or persisted `server_edge` row must be surfaced from the platform config so the API/UI is stable before remote-edge work lands.
- `FunnelCert`
  Certificate lifecycle metadata only. This plan creates pending rows; later plans make them become `ready`.
- `FunnelAudit`
  Operator-visible create/update/enable/disable/delete events.

### Status rules locked in by this plan

- Managed domain created by service create:
  - `status = pending_cert`
  - `dns_status = ready`
  - `cert_status = pending`
- Custom domain created by tenant:
  - `status = pending_dns`
  - `dns_status = pending`
  - `cert_status = pending`
- Service after successful create:
  - `config_status = config_pending`
  - `edge_status = pending_edge`
  - `backend_status = unknown`
  - `last_error = ""`

These are control-plane projections only. Later runtime plans are responsible for transitions to `active`, `degraded`, and `error`.

## Task 1: Add Funnel Persistence Models And Migrations

**Files:**
- Create: `controller/funnel_models.go`
- Create: `controller/funnel_models_test.go`
- Modify: `controller/db.go`
- Test: `controller/funnel_models_test.go`

- [ ] **Step 1: Write the failing schema test**

Add a new test file that migrates the new models into a temp SQLite DB and asserts the expected tables and key columns exist:

```go
func TestInitFunnelTables(t *testing.T) {
	db := newTestDB(t)
	if err := migrateFunnelTables(db); err != nil {
		t.Fatalf("migrateFunnelTables(): %v", err)
	}
	for _, model := range []interface{}{
		&FunnelDomain{},
		&FunnelService{},
		&FunnelEdge{},
		&FunnelCert{},
		&FunnelAudit{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing table for %T", model)
		}
	}
	if !db.Migrator().HasColumn(&FunnelService{}, "listen_proto") {
		t.Fatal("missing listen_proto column")
	}
}
```

- [ ] **Step 2: Run the targeted test to verify it fails**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestInitFunnelTables' -count=1 -v`
Expected: FAIL because the Funnel models and migration helper do not exist yet.

- [ ] **Step 3: Implement the new models**

In `controller/funnel_models.go`, add the persisted types and status constants. Use the existing Mirage patterns for `ID`, `StableID`, `BeforeCreate`, and JSON-backed value types.

Minimum model shape:

```go
type FunnelDomain struct {
	ID                int64  `gorm:"primary_key;unique;not null"`
	StableID          string `gorm:"uniqueIndex"`
	OrgID             int64  `gorm:"index;not null"`
	Domain            string `gorm:"uniqueIndex"`
	DomainType        string
	Status            string
	DNSStatus         string
	TLSMode           string
	ListenerMode      string
	EdgeMode          string
	EdgeTargetID      string
	HTTPPort          int
	HTTPSPort         int
	ValidationMethod  string
	ValidationTarget  string
	ValidationToken   string
	ValidationCheckedAt *time.Time
	CertID            *int64
	LastError         string
	LastDNSError      string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type FunnelService struct {
	ID              int64  `gorm:"primary_key;unique;not null"`
	StableID        string `gorm:"uniqueIndex"`
	OrgID           int64  `gorm:"index;not null"`
	MachineID       int64  `gorm:"index;not null"`
	DomainID        int64  `gorm:"index;not null"`
	Enabled         bool
	Public          bool
	ListenProto     string
	ListenPort      int
	MountPath       string
	BackendType     string
	BackendScheme   string
	BackendTailnetIP string
	BackendPort     int
	TargetMachineOnline bool
	ConfigStatus    string
	EdgeStatus      string
	BackendStatus   string
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
```

Also add:

- `FunnelPlatformConfig` with JSON `Scan`/`Value`
- `FunnelEdge`
- `FunnelCert`
- `FunnelAudit`
- status constants for domain/cert/service states
- `BeforeCreate` hooks that assign `StableID` using `GetShortId`

Lock these additional model fields now instead of leaving them implicit:

```go
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
```

Index expectations for later plans:

- `FunnelDomain.Domain` stays globally unique
- `FunnelService` needs query indexes on `OrgID`, `MachineID`, and `DomainID`
- `FunnelEdge.EdgeNodeID` must stay indexed for later remote-edge sync
- `FunnelCert.DomainID` stays unique because one active cert lifecycle belongs to one domain in this model
- `FunnelAudit` must be queryable by `OrgID`, `ResourceID`, and `Action`

- [ ] **Step 4: Register the tables in the DB bootstrap**

In `controller/db.go`, add a shared helper:

```go
func (dp *DataPool) initFunnelTables() error {
	return dp.db.AutoMigrate(
		&FunnelDomain{},
		&FunnelService{},
		&FunnelEdge{},
		&FunnelCert{},
		&FunnelAudit{},
	)
}
```

Call it from both `InitCockpitDB()` and `InitMirageDB()` so a cockpit-only boot or a controller-only boot produces the same schema.

- [ ] **Step 5: Re-run the targeted schema test**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestInitFunnelTables' -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Run the full controller package once after migration changes**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add controller/funnel_models.go controller/funnel_models_test.go controller/db.go
git commit -m "controller: add Funnel persistence models"
```

## Task 2: Persist And Normalize Funnel Platform Config In Cockpit

**Files:**
- Modify: `controller/cockpit_syscfg.go`
- Create: `controller/funnel_platform.go`
- Create: `controller/funnel_platform_test.go`
- Create: `controller/cockpit_api_funnel.go`
- Create: `controller/cockpit_api_funnel_test.go`
- Modify: `controller/cockpit.go`
- Test: `controller/funnel_platform_test.go`
- Test: `controller/cockpit_api_funnel_test.go`

- [ ] **Step 1: Write failing normalization tests**

Cover platform config normalization with focused unit tests:

```go
func TestNormalizeFunnelPlatformConfig(t *testing.T) {
	cfg, err := normalizeFunnelPlatformConfig(FunnelPlatformConfig{
		ManagedBaseDomain:  "  PUBLIC.EXAMPLE.COM  ",
		DefaultEdgeMode:    "",
		DefaultListenerMode:"",
		DirectBindAddrs:    []string{" 0.0.0.0 ", "::"},
		DirectBindPorts:    []int{443, 80, 443},
		TrustedProxyCIDRs:  []string{"10.0.0.0/24", " 10.0.0.0/24 "},
	})
	if err != nil { t.Fatalf("normalizeFunnelPlatformConfig(): %v", err) }
	if cfg.ManagedBaseDomain != "public.example.com" { t.Fatalf(...) }
	if cfg.DefaultEdgeMode != "server_edge" { t.Fatalf(...) }
	if cfg.DefaultListenerMode != "direct" { t.Fatalf(...) }
}
```

Add one invalid CIDR case and one invalid port case.

- [ ] **Step 2: Write the failing cockpit API tests**

Add HTTP-level tests for:

- `GET /cockpit/api/funnel/config`
- `POST /cockpit/api/funnel/config`
- `GET /cockpit/api/funnel/edges`
- `POST /cockpit/api/funnel/edges`
- `POST /cockpit/api/funnel/edges/{id}/sync`
- `GET /cockpit/api/funnel/domains`
- `POST /cockpit/api/funnel/domains/verify`
- `POST /cockpit/api/funnel/certs/renew`

Assert the response envelope and normalized payload:

```go
if got["managedBaseDomain"] != "public.example.com" {
	t.Fatalf("managedBaseDomain = %#v", got["managedBaseDomain"])
}
if len(data["edges"].([]interface{})) == 0 {
	t.Fatal("expected at least one edge row")
}
```

Also assert authz:

```go
req := httptest.NewRequest(http.MethodPost, "/cockpit/api/funnel/config", body)
rec := httptest.NewRecorder()
router.ServeHTTP(rec, req)
if !strings.Contains(rec.Body.String(), "error-unauthorized") {
	t.Fatalf("expected unauthorized response, got %s", rec.Body.String())
}
```

- [ ] **Step 3: Run the targeted tests to verify they fail**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestNormalizeFunnelPlatformConfig|TestCockpitFunnel' -count=1 -v`
Expected: FAIL because the config field, helpers, and routes do not exist yet.

- [ ] **Step 4: Store Funnel config inside `SysConfig`**

In `controller/cockpit_syscfg.go`, add:

```go
type SysConfig struct {
	...
	FunnelCfg FunnelPlatformConfig
}
```

Do **not** overload `GeneralCfg`. Funnel settings get their own cockpit API, not the existing general settings payload.

In `controller/funnel_platform.go`, implement:

- `normalizeFunnelPlatformConfig(...)`
- `defaultFunnelPlatformConfig(...)`
- `effectiveFunnelIngressTargets(...)`
- `buildDefaultServerEdge(...)`

Normalization rules for this plan:

- `managedBaseDomain` must be lowercase FQDN without scheme
- `defaultEdgeMode` defaults to `server_edge`
- `defaultListenerMode` defaults to `direct`
- `directBindPorts` is deduped and sorted
- `trustedProxyCIDRs` is deduped and normalized

- [ ] **Step 5: Implement the cockpit Funnel APIs**

In `controller/cockpit_api_funnel.go`, add:

- `GetFunnelConfig`
- `SetFunnelConfig`
- `GetFunnelEdges`
- `PostFunnelEdge`
- `PostFunnelEdgeSync`
- `GetFunnelDomains`
- `PostFunnelDomainVerify`
- `PostFunnelCertRenew`

Use this response shape:

```go
map[string]interface{}{
	"config": normalizedConfig,
	"effectiveIngressTargets": effectiveFunnelIngressTargets(normalizedConfig),
}
```

`GET /cockpit/api/funnel/edges` must return at least the configured `server_edge` row, even before remote-edge work exists.

Deferred behavior to lock now:

- `POST /cockpit/api/funnel/edges/{id}/sync`
  - returns a stable deferred payload such as `{ "syncDeferred": true }`
  - records a `FunnelAudit` event
- `POST /cockpit/api/funnel/domains/verify`
  - returns the requested domain plus `verificationDeferred=true`
  - does not perform DNS probing yet
- `POST /cockpit/api/funnel/certs/renew`
  - returns the requested domain/cert pair plus `renewDeferred=true`
  - does not trigger issuance yet
- `POST /cockpit/api/funnel/edges`
  - persists or updates inventory rows so the later remote-edge plan reuses the same API contract

Every cockpit mutating handler in this step must append a `FunnelAudit` row with:

- `actor_type = "super_admin"`
- `action = "edge_upsert" | "edge_sync" | "domain_verify_requested" | "cert_renew_requested"`
- `resource_type = "edge" | "domain" | "cert"`
- normalized request payload in `payload`

- [ ] **Step 6: Wire the cockpit routes**

In `controller/cockpit.go`, register:

```go
cockpit_router.HandleFunc("/api/funnel/config", c.GetFunnelConfig).Methods(http.MethodGet)
cockpit_router.HandleFunc("/api/funnel/config", c.SetFunnelConfig).Methods(http.MethodPost)
cockpit_router.HandleFunc("/api/funnel/edges", c.GetFunnelEdges).Methods(http.MethodGet)
cockpit_router.HandleFunc("/api/funnel/edges", c.PostFunnelEdge).Methods(http.MethodPost)
cockpit_router.HandleFunc("/api/funnel/edges/{id}/sync", c.PostFunnelEdgeSync).Methods(http.MethodPost)
cockpit_router.HandleFunc("/api/funnel/domains", c.GetFunnelDomains).Methods(http.MethodGet)
cockpit_router.HandleFunc("/api/funnel/domains/verify", c.PostFunnelDomainVerify).Methods(http.MethodPost)
cockpit_router.HandleFunc("/api/funnel/certs/renew", c.PostFunnelCertRenew).Methods(http.MethodPost)
```

- [ ] **Step 7: Re-run the targeted tests**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestNormalizeFunnelPlatformConfig|TestCockpitFunnel' -count=1 -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add controller/cockpit_syscfg.go controller/funnel_platform.go controller/funnel_platform_test.go controller/cockpit_api_funnel.go controller/cockpit_api_funnel_test.go controller/cockpit.go
git commit -m "cockpit: add Funnel platform config APIs"
```

## Task 3: Add Tenant Domain And Service CRUD APIs With Projected Status

**Files:**
- Create: `controller/console_api_funnel.go`
- Create: `controller/console_api_funnel_test.go`
- Modify: `controller/funnel_platform.go`
- Modify: `controller/app.go`
- Test: `controller/console_api_funnel_test.go`
- Test: `controller/funnel_platform_test.go`

- [ ] **Step 1: Write failing tenant API tests**

Cover these cases:

1. `POST /admin/api/funnel/domains` creates a custom-domain record in `pending_dns`
2. `POST /admin/api/funnel/domains/{id}/verify` returns the deferred-not-active response for this plan
3. `POST /admin/api/funnel/services` with `domainMode=managed` auto-creates a managed domain
4. `GET /admin/api/funnel/services` returns projected `dnsStatus`, `certStatus`, `edgeStatus`, and `backendStatus`
5. `POST /admin/api/funnel/services/{id}/enable` and `/disable` flip `enabled`
6. `GET /admin/api/funnel/services/{id}/logs` returns a stable empty payload for this plan
7. tenant member reads succeed but tenant mutation attempts from non-owner users fail

Example assertions:

```go
if domain["status"] != "pending_dns" {
	t.Fatalf("domain status = %#v", domain["status"])
}
if service["edgeStatus"] != "pending_edge" {
	t.Fatalf("edgeStatus = %#v", service["edgeStatus"])
}
if data["logs"] == nil {
	t.Fatal("expected logs field, even if empty")
}
```

Add one authz assertion for a non-owner tenant user:

```go
req := newTenantRequest(t, http.MethodPost, "/admin/api/funnel/services", body, memberUser)
rec := httptest.NewRecorder()
router.ServeHTTP(rec, req)
if !strings.Contains(rec.Body.String(), "权限") && !strings.Contains(rec.Body.String(), "error-") {
	t.Fatalf("expected owner-only mutation failure, got %s", rec.Body.String())
}
```

- [ ] **Step 2: Run the tenant Funnel tests to verify they fail**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestConsoleFunnel' -count=1 -v`
Expected: FAIL because the handlers and helper methods do not exist yet.

- [ ] **Step 3: Add request normalization and managed-domain allocation helpers**

In `controller/funnel_platform.go`, implement:

- `normalizeFunnelListenProto`
- `normalizeFunnelMountPath`
- `defaultFunnelListenPort`
- `validateFunnelDomainCreateRequest`
- `validateFunnelServiceCreateRequest`
- `allocateManagedFunnelDomain`
- `projectFunnelServiceStatus`
- `recordFunnelAudit`

Use this deterministic managed-domain naming rule:

```go
func allocateManagedFunnelDomain(org *Organization, machine *Machine, cfg FunnelPlatformConfig) string {
	return fmt.Sprintf("%s-%s.%s", machine.StableID, org.StableID, cfg.ManagedBaseDomain)
}
```

Use this service projection rule in this plan:

```go
service.ConfigStatus = "config_pending"
service.EdgeStatus = "pending_edge"
service.BackendStatus = "unknown"
```

And use this status-response contract:

```go
type FunnelServiceStatusResponse struct {
	Service     interface{} `json:"service"`
	Domain      interface{} `json:"domain"`
	Cert        interface{} `json:"cert"`
	CurrentEdge interface{} `json:"currentEdge"`
	LastError   string      `json:"lastError"`
	Logs        []any       `json:"logs,omitempty"`
}
```

- [ ] **Step 4: Implement the tenant Funnel handlers**

In `controller/console_api_funnel.go`, add:

- `CAPIGetFunnelDomains`
- `CAPIPostFunnelDomains`
- `CAPIVerifyFunnelDomain`
- `CAPIDeleteFunnelDomain`
- `CAPIGetFunnelServices`
- `CAPIPostFunnelServices`
- `CAPIPatchFunnelService`
- `CAPIEnableFunnelService`
- `CAPIDisableFunnelService`
- `CAPIDeleteFunnelService`
- `CAPIGetFunnelServiceStatus`
- `CAPIGetFunnelServiceLogs`

Important behavior for this plan:

- `POST /admin/api/funnel/domains`
  - allows `domainType=custom`
  - persists `pending_dns` + validation token/instructions
  - does **not** try real DNS verification yet
- `POST /admin/api/funnel/domains/{id}/verify`
  - must exist in this slice
  - returns the current domain record and validation instructions plus a stable deferred flag or deferred error message
  - must not perform DNS lookups yet
- `POST /admin/api/funnel/services`
  - accepts either existing `domainId` or `domainMode=managed`
  - auto-creates the managed domain when needed
  - rejects any service-level edge override
- `PATCH /admin/api/funnel/services/{id}`
  - may update only `mountPath`, `backendScheme`, `backendPort`, and `enabled`
  - must not change `machineId`, `domainId`, `domainMode`, `listenProto`, `listenPort`, `edgeMode`, or `edgeTargetId`
  - after a successful patch, the handler must re-run the same validation and projection helpers used by create
- `GET /admin/api/funnel/services/{id}/logs`
  - returns `{"logs":[]}` in the normal Mirage success envelope until runtime exists

Authorization and audit rules for this step:

- `GET` endpoints allow any logged-in tenant member in the same org.
- `POST`, `PATCH`, `DELETE`, `/enable`, `/disable`, and `/verify` must require `user.Role == RoleOwner`.
- every successful mutation writes a `FunnelAudit` row:
  - `domain_created`
  - `domain_verify_requested`
  - `domain_deleted`
  - `service_created`
  - `service_updated`
  - `service_enabled`
  - `service_disabled`
  - `service_deleted`

- [ ] **Step 5: Wire the tenant routes**

In `controller/app.go`, register:

```go
console_router.HandleFunc("/api/funnel/domains", h.CAPIGetFunnelDomains).Methods(http.MethodGet)
console_router.HandleFunc("/api/funnel/domains", h.CAPIPostFunnelDomains).Methods(http.MethodPost)
console_router.HandleFunc("/api/funnel/domains/{id}/verify", h.CAPIVerifyFunnelDomain).Methods(http.MethodPost)
console_router.HandleFunc("/api/funnel/domains/{id}", h.CAPIDeleteFunnelDomain).Methods(http.MethodDelete)
console_router.HandleFunc("/api/funnel/services", h.CAPIGetFunnelServices).Methods(http.MethodGet)
console_router.HandleFunc("/api/funnel/services", h.CAPIPostFunnelServices).Methods(http.MethodPost)
console_router.HandleFunc("/api/funnel/services/{id}", h.CAPIPatchFunnelService).Methods(http.MethodPatch)
console_router.HandleFunc("/api/funnel/services/{id}/enable", h.CAPIEnableFunnelService).Methods(http.MethodPost)
console_router.HandleFunc("/api/funnel/services/{id}/disable", h.CAPIDisableFunnelService).Methods(http.MethodPost)
console_router.HandleFunc("/api/funnel/services/{id}", h.CAPIDeleteFunnelService).Methods(http.MethodDelete)
console_router.HandleFunc("/api/funnel/services/{id}/status", h.CAPIGetFunnelServiceStatus).Methods(http.MethodGet)
console_router.HandleFunc("/api/funnel/services/{id}/logs", h.CAPIGetFunnelServiceLogs).Methods(http.MethodGet)
```

`GET /admin/api/funnel/services/{id}/status` must return:

- the service row
- linked domain projection
- linked cert projection
- current edge assignment
- `last_error`

- [ ] **Step 6: Re-run the targeted tenant tests**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestConsoleFunnel' -count=1 -v`
Expected: PASS.

- [ ] **Step 7: Run the full controller package once after route changes**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add controller/console_api_funnel.go controller/console_api_funnel_test.go controller/funnel_platform.go controller/app.go
git commit -m "console: add Funnel domain and service APIs"
```

## Task 4: Add Cockpit Funnel Settings UI

**Files:**
- Create: `cockpit_web/src/setpart/Funnel.vue`
- Modify: `cockpit_web/src/Settings.vue`
- Test: `controller/cockpit_api_funnel_test.go`

- [ ] **Step 1: Add the cockpit settings entry**

In `cockpit_web/src/Settings.vue`, register the new section:

```js
import Funnel from "./setpart/Funnel.vue";

const setPartContent = {
  general: General,
  authority: Authority,
  client: Client,
  funnel: Funnel,
  rebindSA: RebindSA,
};
```

Add `公网入口` to both the desktop nav and the mobile `<select>`.

- [ ] **Step 2: Build the Funnel settings component**

In `cockpit_web/src/setpart/Funnel.vue`, implement:

- on mount: `GET /cockpit/api/funnel/config`
- save action: `POST /cockpit/api/funnel/config`
- edge inventory load: `GET /cockpit/api/funnel/edges`

Minimum fields on the page:

- `managedBaseDomain`
- `defaultEdgeMode`
- `defaultListenerMode`
- `directBindAddrs`
- `directBindPorts`
- `trustedProxyCIDRs`
- read-only `effectiveIngressTargets`
- table of edge rows with `stableId`, `edgeType`, `healthStatus`, `allocatable`
- read-only or deferred action table for domains/certs:
  - domain
  - org
  - status
  - `验证` button calling `/cockpit/api/funnel/domains/verify`
  - `续期` button calling `/cockpit/api/funnel/certs/renew`

The page should reuse the existing `Toast.vue` pattern from other cockpit settings pages instead of introducing another notification system.

- [ ] **Step 3: Verify the cockpit frontend builds**

Run: `cd /home/hao/A-1/MirageServer/cockpit_web && npm run build`
Expected: PASS and emit updated files under `controller/cockpit_html`.

- [ ] **Step 4: Re-run the cockpit Funnel backend tests**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestCockpitFunnel' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add cockpit_web/src/Settings.vue cockpit_web/src/setpart/Funnel.vue controller/cockpit_html
git commit -m "cockpit: add Funnel settings UI"
```

## Task 5: Add Tenant Console Funnel Page

**Files:**
- Create: `console_web/src/components/Funnel.vue`
- Modify: `console_web/src/admin.js`
- Modify: `console_web/src/Console.vue`
- Test: `controller/console_api_funnel_test.go`

- [ ] **Step 1: Register the new tenant route**

In `console_web/src/admin.js`, add:

```js
import Funnel from "./components/Funnel.vue";

const routes = [
  ...,
  { path: "/funnel", component: Funnel },
];
```

In `console_web/src/Console.vue`, add a nav item labeled `公网服务` and include `/funnel` in `currentRoute`.

- [ ] **Step 2: Build the tenant Funnel page**

In `console_web/src/components/Funnel.vue`, implement two panels on one page:

1. `公共域名`
   - list existing domains
   - create custom domain
   - show validation token/target/instructions
   - delete a pending domain
2. `公共服务`
   - list existing services
   - create a managed-domain service from a machine
   - enable/disable/delete a service
   - show projected `dnsStatus`, `certStatus`, `edgeStatus`, `backendStatus`

Use the existing Mirage console style:

- `axios` requests
- `Toast.vue`
- existing table/card layout conventions from `Machines.vue` and `Users.vue`

- [ ] **Step 3: Build the tenant frontend**

Run: `cd /home/hao/A-1/MirageServer/console_web && npm run build`
Expected: PASS and emit updated files under `controller/console_html`.

- [ ] **Step 4: Re-run the tenant Funnel backend tests**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestConsoleFunnel' -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add console_web/src/admin.js console_web/src/Console.vue console_web/src/components/Funnel.vue controller/console_html
git commit -m "console: add Funnel management page"
```

## Task 6: Final Verification For This Slice

**Files:**
- Modify: `docs/superpowers/plans/2026-03-28-funnel-control-plane.md`

- [ ] **Step 1: Run backend verification**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller`
Expected: PASS.

- [ ] **Step 2: Run the cockpit frontend build**

Run: `cd /home/hao/A-1/MirageServer/cockpit_web && npm run build`
Expected: PASS.

- [ ] **Step 3: Run the tenant frontend build**

Run: `cd /home/hao/A-1/MirageServer/console_web && npm run build`
Expected: PASS.

- [ ] **Step 4: Mark the completed steps in this plan**

Update this plan file so every completed checkbox reflects reality before handoff to plan 2.

- [ ] **Step 5: Commit the plan-tracking updates if code changed during verification**

```bash
cd /home/hao/A-1/MirageServer
git add docs/superpowers/plans/2026-03-28-funnel-control-plane.md
git commit -m "docs: mark Funnel control-plane plan progress"
```

## Handoff Notes For Plan 2

After this plan lands:

- the schema and APIs are stable for client-side intent sync
- managed-domain service creation already has stable IDs and projected status fields
- cockpit already owns the base domain and ingress defaults
- tenant UI already exposes domain/service records without pretending the runtime is active

Plan 2 should build on these contracts instead of inventing parallel ones.
