# Mirage Funnel Hybrid Edge Design

## Goal

Build Mirage Funnel as a first-class public ingress system that can:

- publish services on both platform-managed domains and tenant custom domains
- run behind existing `Nginx/Caddy/cloud LB` or by directly listening on `80/443` or configured ports
- support `HTTP`, `HTTPS`, `WS`, `WSS`, `TCP`, and `TLS-terminated-TCP`
- route traffic through either `MirageServer` itself or dedicated remote `Navi/edge` nodes

This design treats Funnel as a control-plane-managed product, not only a client CLI flag.

## Product Scope

### Required in v1

- Managed domain onboarding and service publication
- Custom domain onboarding, DNS verification, and active public service
- Direct edge mode on `MirageServer`
- Remote edge mode on `Navi/edge` nodes
- HTTP reverse proxy with WebSocket upgrade pass-through
- Raw TCP forwarding
- TLS termination at the edge for HTTP and `tls_terminated_tcp`
- Cockpit platform management pages
- Tenant console domain/service management pages
- Client and daemon integration with existing `serve/funnel` intent model

### Non-Goals for the first implementation plan

The whole product is in scope for Funnel v1, but implementation planning must be split into multiple execution plans. A single implementation plan must not try to deliver the full system in one batch.

## Deployment Model

Mirage Funnel runs as a hybrid ingress platform.

### Supported ingress topologies

1. Reverse-proxied ingress
- External `Nginx`, `Caddy`, or cloud load balancer receives public traffic
- TLS may terminate there or be passed through
- Request metadata is forwarded using `X-Forwarded-*` headers or PROXY protocol

2. Direct-listener ingress
- `MirageServer` or a remote edge node listens directly on `80/443` or operator-defined ports
- TLS, routing, and protocol handling are performed by the Funnel edge runtime

### Reverse-proxy trust boundary

When `listener_mode` is `behind_proxy`:

- forwarded metadata is trusted only from explicitly configured proxy CIDRs or dedicated PROXY-protocol listener ports
- untrusted peers must not influence client IP, scheme, host, or port interpretation through `X-Forwarded-*` headers
- routing continues to use the validated ingress host and listener mapping decided by the trusted edge entrypoint
- if trust configuration is absent, forwarded headers are ignored and the deployment is treated as misconfigured rather than partially trusted

### Supported edge runtimes

1. `server-edge`
- The public listener is hosted by `MirageServer`

2. `remote-edge`
- The public listener is hosted by a dedicated `Navi/edge` node

Both modes share the same control-plane objects and routing rules.

### Mode compatibility matrix

| Protocol | `listener_mode=direct` | `listener_mode=behind_proxy` | TLS termination in v1 |
|----------|-------------------------|------------------------------|-----------------------|
| `http` | valid | valid | none |
| `ws` | valid | valid | none |
| `https` | valid | valid, but only with L4 passthrough or PROXY protocol to the edge | edge |
| `wss` | valid | valid, but only with L4 passthrough or PROXY protocol to the edge | edge |
| `tcp` | valid | valid, but only with L4 passthrough or PROXY protocol | none |
| `tls_terminated_tcp` | valid | valid, but only with L4 passthrough or PROXY protocol to the edge | edge |

Rules:

- `tls_mode` is only meaningful for `https`, `wss`, and `tls_terminated_tcp`
- `http` and `ws` ignore `tls_mode`
- `behind_proxy` for `https`, `wss`, and `tls_terminated_tcp` does not permit proxy-side TLS termination in v1; external proxies must preserve the TLS stream to the Funnel edge
- `behind_proxy` for `http` and `ws` permits trusted L7 forwarding with `X-Forwarded-*` headers

## Architecture

Mirage Funnel is split into four layers.

### 1. Client intent layer

`MirageClient` keeps the local `serve` and `funnel` UX. The client expresses local service intent using local `ServeConfig`, then syncs normalized service intent to Mirage control.

### 2. Control plane layer

`MirageServer` owns:

- domain inventory
- verification state
- certificate state
- service definitions
- edge allocation
- policy validation
- runtime config snapshots

Mirage control is the source of truth for public ingress status.

### 3. Edge runtime layer

An edge runtime receives public traffic, loads the latest approved config snapshot, and performs:

- listener binding
- TLS configuration
- HTTP routing
- WebSocket upgrade pass-through
- TCP stream proxying
- target reachability checks
- health and metrics reporting

### 4. DNS/TLS integration layer

This layer manages:

- managed domain allocation
- custom domain ownership validation
- platform-managed certificate issuance
- later BYOC certificate ingestion
- renewal tracking

## Data Model

### `FunnelDomain`

Represents a public domain managed by the platform.

Fields:

- `id`
- `org_id`
- `domain`
- `domain_type`: `managed` or `custom`
- `status`: `pending_dns`, `pending_cert`, `active`, `error`, `disabled`
- `dns_status`: `pending`, `ready`, `error`
- `tls_mode`: `platform_managed` or deferred `bring_your_own_cert`
- `listener_mode`: `direct` or `behind_proxy`
- `edge_mode`: `server_edge` or `remote_edge`
- `edge_target_id`
- `http_port`
- `https_port`
- `validation_method`
- `validation_target`
- `validation_token`
- `validation_checked_at`
- `cert_id`
- `last_error`
- `last_dns_error`

Domain-level edge assignment is authoritative in v1. All services bound to the same domain inherit `edge_mode` and `edge_target_id` from `FunnelDomain`. Service-level edge override is deferred and must not be part of the initial schema or API slice.

### `FunnelService`

Represents a public service bound to one machine and one domain.

Fields:

- `id`
- `org_id`
- `machine_id`
- `domain_id`
- `enabled`
- `public`
- `listen_proto`: `http`, `https`, `tcp`, `tls_terminated_tcp`, `ws`, `wss`
- `listen_port`
- `mount_path`
- `backend_type`: `http_proxy`, `tcp_proxy`
- `backend_scheme`
- `backend_tailnet_ip`
- `backend_port`
- `target_machine_online`
- `config_status`
- `edge_status`
- `backend_status`
- `last_error`

Owned service state is limited to config, edge application, backend reachability, and last runtime error. Domain readiness and certificate readiness are projected from the linked `FunnelDomain` and `FunnelCert` records and must not be duplicated as separate source-of-truth service columns.

### `FunnelEdge`

Represents an ingress execution target.

Fields:

- `id`
- `stable_id`
- `edge_type`: `server` or `navi`
- `edge_node_id`: stable control-plane identifier of the actual edge executor
- `hostname`
- `public_addrs`: array of `{network, address, port}`
- `sync_endpoint`
- `listener_capabilities`: object with booleans for `supports_http`, `supports_ws`, `supports_tcp`, `supports_tls_terminated_tcp`, `supports_proxy_protocol`, `supports_forwarded_headers`, and `supports_direct_bind`
- `health_status`
- `last_seen`
- `allocatable`
- `trust_proxy_cidrs`

### `FunnelCert`

Represents certificate material and lifecycle state.

Fields:

- `id`
- `domain_id`
- `issuer`
- `challenge_type`
- `cert_status`: `pending`, `ready`, `error`, `expired`
- `certificate_ref`
- `private_key_ref`
- `not_before`
- `not_after`
- `renew_after`
- `last_error`

### `FunnelAudit`

Tracks operator and system events.

Fields:

- `id`
- `org_id`
- `actor_type`
- `actor_id`
- `resource_type`
- `resource_id`
- `action`
- `payload`
- `created_at`

## Domain and Service Flows

### Managed domain flow

1. Tenant creates a Funnel service and selects platform-managed domain mode.
2. Control allocates and persists the backing `FunnelDomain` automatically using a deterministic naming scheme.
3. The managed hostname, edge assignment, and TLS mode are server-generated rather than caller-supplied.
4. The hostname is pointed to the selected edge topology.
5. The certificate worker ensures TLS is available.
6. The service moves through `pending_cert` to `active`.
7. The active URL is returned to the tenant console and client status.

### Custom domain flow

1. Tenant adds a custom domain in the console.
2. Control returns validation instructions:
- `TXT _mirage-funnel.<domain> = <token>` for ownership proof
- either `CNAME <domain> -> <platform ingress target>` for subdomains
- or `A/AAAA <domain> -> <assigned ingress addresses>` for apex domains
3. Control verifies DNS ownership and ingress routing readiness.
4. `dns_status` becomes `ready` only when both ownership proof and ingress target mapping are correct.
4. Certificate worker issues a platform-managed certificate.
5. Once the domain is `active`, a service can bind to it.
6. The service becomes publicly reachable only after both domain and runtime are healthy.

Rules:

- wildcard domains are not part of the initial implementation slice
- a custom domain cannot become `active` on target reachability alone; explicit ownership proof is always required
- the same custom domain cannot be simultaneously verified by multiple orgs
- custom-domain transfer and release workflows are not part of the initial implementation slice

### Custom domain BYOC flow

1. Tenant creates or verifies a custom domain using the normal custom domain flow.
2. After ownership is established, the tenant uploads certificate material.
3. Mirage validates certificate chain, key match, and validity window.
4. `FunnelCert.cert_status` becomes `ready` only after successful validation.
5. BYOC support is planned as a later implementation slice and is not part of the initial custom-domain activation plan.
6. Before the BYOC slice lands, `bring_your_own_cert` is a deferred/read-only mode and must not be exposed as an active creation path in the first schema or UI plan.

`FunnelCert` is still part of the initial schema because platform-managed certificate lifecycle requires it. BYOC-specific upload, revoke, and operator workflows are fully deferred to plan 7.

### Service publication flow

1. Client or console creates or updates a service intent.
2. Mirage validates tenancy, protocol, port conflicts, domain ownership, and edge availability.
3. Mirage stores approved service state in `FunnelService`.
4. Mirage renders a runtime config snapshot for the assigned edge.
5. Edge runtime applies the snapshot and reports readiness.
6. Control marks the service `active`, `degraded`, or `error`.

HTTP-family listen-port rule:

- for `http` and `ws`, `listen_port` is the shared public listener port and defaults to `80`
- for `https` and `wss`, `listen_port` is the shared public listener port and defaults to `443`
- for HTTP-family traffic, route uniqueness is determined by `listen_port + host + mount_path`
- for `tcp` and `tls_terminated_tcp`, `listen_port` is a dedicated public port and must be explicitly provided

### Client and daemon reconcile flow

1. The local daemon computes a normalized desired-state set from local `ServeConfig`.
2. The daemon syncs the full desired set on startup, local config change, reconnect, and periodic refresh.
3. Mirage validates each desired service and returns approved public-service state with stable IDs and domain assignments.
4. Managed-domain services created from daemon intent are control-managed but machine-scoped.
5. Custom-domain bindings remain server-authoritative and are never deleted solely because they are absent from a daemon sync.
6. A managed-domain service is disabled when the daemon explicitly turns it off or when the same desired service is missing from consecutive reconciliations according to the server-side stale-intent policy.

## Protocol Handling

### HTTP and HTTPS

- Route by `host + path`
- Proxy to normalized target machine address and port
- Preserve standard reverse proxy headers
- Support sticky WebSocket upgrades on the same service definition
- `host + path` routing only applies to HTTP-family traffic handled at L7

### WS and WSS

- No separate routing plane
- Treated as HTTP Upgrade over the same host/path rule set
- Status and metrics remain attached to the parent Funnel service

### TCP

- Route only by a dedicated public listen port
- Create a raw stream tunnel to the normalized tailnet target
- Raw TCP does not use SNI multiplexing in v1

### TLS-terminated-TCP

- TLS terminates on the edge
- Certificate selection is based on the bound domain and the incoming TLS SNI
- Decrypted TCP stream is forwarded to the target machine port
- Certificate ownership follows the selected domain, not the machine

Canonical protocol field name in config and APIs is `tls_terminated_tcp`. The prose label `TLS-terminated-TCP` refers to the same value.

## Target Addressing

Client-side local sources such as `http://localhost:8080` are not stored directly as the public runtime contract.

Mirage normalizes them into:

- `machine_id`
- `backend_tailnet_ip`
- `backend_port`
- `backend_scheme`

This keeps edge runtimes independent from client-specific local syntax.

## Edge Allocation and Failover Policy

Compatible edge selection uses all of these filters:

- `edge_mode` must match the domain-authoritative edge mode
- `listener_mode` must be supported by the candidate edge
- protocol capability must exist on the candidate edge
- direct-bind requirements must match the candidate edge bind capability
- trusted-proxy requirements must match the candidate edge trust configuration

Selection and failover rules:

- v1 failover only reassigns within the same `edge_mode`
- `server_edge` services may move only among configured `server_edge` candidates
- `remote_edge` services may move only among allocatable `remote_edge` candidates
- automatic failover between `server_edge` and `remote_edge` is not performed in v1
- the preferred compatible edge is the healthiest candidate with the most recent successful heartbeat
- if no compatible edge exists, the service enters `edge_unavailable` and remains configured but not active

## API Design

### Cockpit platform APIs

- `GET /cockpit/api/funnel/config`
- `POST /cockpit/api/funnel/config`
- `GET /cockpit/api/funnel/edges`
- `POST /cockpit/api/funnel/edges`
- `POST /cockpit/api/funnel/edges/{id}/sync`
- `GET /cockpit/api/funnel/domains`
- `POST /cockpit/api/funnel/domains/verify`
- `POST /cockpit/api/funnel/certs/renew`

These APIs manage:

- platform base domain
- managed domain naming policy
- direct-listener settings
- reverse-proxy settings
- edge inventory and health
- global certificate policy

Permission rule:

- Cockpit Funnel APIs are super-admin only

Minimal request and response contracts for the first implementation slice:

- `POST /cockpit/api/funnel/config`
  - Request: `managedBaseDomain`, `defaultEdgeMode`, `defaultListenerMode`, `directBindAddrs`, `directBindPorts`, `trustedProxyCIDRs`
  - Response: normalized platform config plus effective ingress targets

### Tenant console APIs

- `GET /admin/api/funnel/domains`
- `POST /admin/api/funnel/domains`
- `POST /admin/api/funnel/domains/{id}/verify`
- `DELETE /admin/api/funnel/domains/{id}`
- `GET /admin/api/funnel/services`
- `POST /admin/api/funnel/services`
- `PATCH /admin/api/funnel/services/{id}`
- `POST /admin/api/funnel/services/{id}/enable`
- `POST /admin/api/funnel/services/{id}/disable`
- `DELETE /admin/api/funnel/services/{id}`
- `GET /admin/api/funnel/services/{id}/status`
- `GET /admin/api/funnel/services/{id}/logs`

Permission rule:

- Domain creation, service creation, enable, disable, and delete require tenant owner or tenant admin
- Read-only status APIs are available to tenant members with console access

Response contract:

- Follow the existing Mirage API envelope: `{"status":"success","data":...}` on success
- Failures use the same envelope with `status` prefixed by `error-`
- Domain and service responses must always include stable IDs and the latest projected readiness state

Minimal request and response contracts for the first implementation slice:

- `POST /admin/api/funnel/domains`
  - First-slice purpose: custom domains only
  - Request: `domain`, `domainType=custom`, `listenerMode`, `edgeMode`, `tlsMode=platform_managed`
  - Response: created `FunnelDomain` plus validation instructions when applicable
  - Validation: `tlsMode=bring_your_own_cert` is deferred and must be rejected or treated as read-only in the first implementation slice

- `POST /admin/api/funnel/services`
  - Request: `machineId`, `domainId` or `domainMode=managed`, `listenProto`, `listenPort`, `mountPath`, `backendType`, `backendPort`
  - Response: created `FunnelService` plus projected `dns_status`, `cert_status`, `edge_status`, and `backend_status`

- `POST /admin/api/funnel/services` detailed request fields
  - Required: `machineId`, `listenProto`, `backendType`, `backendPort`
  - Required domain selector: either `domainId` for an existing custom domain, or `domainMode=managed` for automatic managed-domain allocation
  - Optional: `mountPath`, `backendScheme`, `enabled`, `listenPort`
  - Validation: `backendScheme` is required for HTTP-family backends and must be absent for raw TCP backends
  - Validation: edge assignment is inherited from the selected domain and must not be overridden at service create time in the first implementation slice
  - Validation: `listenPort` is optional for `http`, `https`, `ws`, and `wss` and defaults to `80` or `443`; it is required for `tcp` and `tls_terminated_tcp`

Managed vs custom domain field ownership:

- Managed domain: `domain`, `edgeMode`, `edgeTargetId`, and `tlsMode` are control-plane generated
- Custom domain: caller supplies `domain`; control validates and persists readiness state before service binding

- `GET /admin/api/funnel/services/{id}/status`
  - Response: service state, linked domain state, linked certificate state, current edge assignment, and `last_error`

### Client and daemon APIs

Local CLI remains:

- `mirage serve ...`
- `mirage funnel <port> on|off`

The daemon must gain control-plane sync for approved public service state. Client-side quick-start is supported for managed domains. Custom domain onboarding is handled in web UI, while CLI may control already-created services and query their state.

## Term Glossary

- `public`: the service is intended for internet exposure rather than tailnet-only local serving
- `listener_mode`: whether the edge listener is direct (`direct`) or receives traffic from a trusted reverse proxy (`behind_proxy`)
- `edge_mode`: whether execution is hosted on `MirageServer` (`server_edge`) or a remote `Navi/edge` node (`remote_edge`)
- `edge_target_id`: foreign key from a domain assignment to a `FunnelEdge.id`
- `edge_node_id`: stable identity of the actual runtime executor used for sync, health, and failover

## UI Design

### Cockpit

Add:

- Funnel platform settings
- Funnel edge management
- Funnel domain and certificate operations

### Tenant console

Add:

- Public domains page
- Public services page

The domain page owns validation and certificate readiness. The services page owns binding a machine service to a managed or already-active custom domain.

## Runtime Status Model

Every service exposes these operator-visible states:

- `config_status`
- `edge_status`
- `dns_status`
- `cert_status`
- `backend_status`
- `last_error`

Ownership of these states is explicit:

- `config_status`, `edge_status`, `backend_status`, and `last_error` are stored on `FunnelService`
- `dns_status` is stored on `FunnelDomain` and projected into service status views
- `cert_status` is stored on `FunnelCert` and projected into service status views

State progression summary:

- Domain: `pending_dns -> pending_cert -> active -> disabled|error`
- Certificate: `pending -> ready -> expired|error`
- Service: `config_pending -> applying -> active|degraded|error|disabled`

This is required so the UI can show the difference between:

- config saved
- DNS not ready
- certificate pending
- edge listener failed
- target node offline

Lifecycle ownership summary:

- `FunnelDomain` owns public domain onboarding, DNS validation, and ingress attachment intent
- `FunnelCert` owns certificate readiness and renewal lifecycle
- `FunnelService` owns runtime enablement, edge apply status, and backend reachability
- `FunnelEdge` owns executor identity, sync endpoint, and health state

## Security and Isolation

- Domains are scoped to tenant organizations
- A custom domain cannot be attached to another org once verified unless explicitly released
- Edge snapshots include only resources assigned to that edge
- Direct and remote edges both use least-privilege config pull credentials
- Certificates and private keys are stored via references, not embedded in service rows
- Public services are disabled if the target machine is no longer visible to the org

## Observability

The system must emit:

- domain verification events
- certificate issuance and renewal events
- service create/update/enable/disable events
- edge config apply events
- listener bind failures
- request counters
- active connection gauges
- error rates
- ingress and egress bytes

## Failure Handling

- Domain verification failure keeps the domain in `pending_dns` or `error`
- Certificate failure keeps the service out of `active`
- Target machine offline moves the service to `degraded`
- Edge failure triggers reallocation when another compatible edge is available
- Reverse-proxy misconfiguration is surfaced separately from backend reachability

## Testing Strategy

### Unit tests

- domain ownership validation
- managed domain allocation
- service normalization from client intent
- protocol validation and conflict detection
- edge assignment logic
- snapshot rendering

### Integration tests

- managed domain HTTP publication on `server-edge`
- custom domain verification and activation
- WebSocket upgrade pass-through
- raw TCP forwarding
- TLS-terminated-TCP publication
- reverse-proxy mode request forwarding
- direct-listener mode on configured ports
- remote-edge config sync and activation

### End-to-end tests

- client `serve/funnel` intent sync to active public service
- console-created service becomes reachable from outside
- certificate renewal does not interrupt active service

## Planning Decomposition

Implementation planning must be split into these plans, in order:

1. Control-plane schema, APIs, and Cockpit platform settings
2. Client and daemon intent-sync integration for managed-domain publication
3. `server-edge` runtime for managed-domain `HTTP/HTTPS/WS/WSS`
4. `TCP` and `TLS-terminated-TCP` runtime support
5. Custom-domain verification and platform-managed certificate activation
6. `remote-edge` runtime and sync model
7. BYOC certificate ingestion and lifecycle operations

This design is the umbrella spec for Funnel v1. Each implementation plan must target one bounded slice from the list above.
