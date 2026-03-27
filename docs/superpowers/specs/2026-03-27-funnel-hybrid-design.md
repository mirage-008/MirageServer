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
- TLS termination at the edge for HTTP and `tls-terminated-tcp`
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

### Supported edge runtimes

1. `server-edge`
- The public listener is hosted by `MirageServer`

2. `remote-edge`
- The public listener is hosted by a dedicated `Navi/edge` node

Both modes share the same control-plane objects and routing rules.

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
- certificate issuance or BYOC ingestion
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
- `tls_mode`: `platform_managed` or `bring_your_own_cert`
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

### `FunnelService`

Represents a public service bound to one machine and one domain.

Fields:

- `id`
- `org_id`
- `machine_id`
- `domain_id`
- `enabled`
- `public`
- `listen_proto`: `http`, `https`, `tcp`, `tls_tcp`, `ws`, `wss`
- `listen_port`
- `mount_path`
- `backend_type`: `http_proxy`, `tcp_proxy`, `static`, `text`
- `backend_scheme`
- `backend_tailnet_ip`
- `backend_port`
- `target_machine_online`
- `config_status`
- `edge_status`
- `backend_status`
- `last_error`

### `FunnelEdge`

Represents an ingress execution target.

Fields:

- `id`
- `edge_type`: `server` or `navi`
- `hostname`
- `public_addrs`
- `listener_capabilities`
- `health_status`
- `last_seen`
- `allocatable`

### `FunnelCert`

Represents certificate material and lifecycle state.

Fields:

- `id`
- `domain_id`
- `issuer`
- `challenge_type`
- `status`
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
2. Control allocates a managed hostname using a deterministic naming scheme.
3. The hostname is pointed to the selected edge topology.
4. The certificate worker ensures TLS is available.
5. The service moves through `pending_cert` to `active`.
6. The active URL is returned to the tenant console and client status.

### Custom domain flow

1. Tenant adds a custom domain in the console.
2. Control returns validation instructions:
- `CNAME` or `A/AAAA` target for ingress
- `TXT` token when ownership proof is required
3. Control verifies DNS ownership and ingress routing readiness.
4. Certificate worker issues or imports the certificate.
5. Once the domain is `active`, a service can bind to it.
6. The service becomes publicly reachable only after both domain and runtime are healthy.

### Service publication flow

1. Client or console creates or updates a service intent.
2. Mirage validates tenancy, protocol, port conflicts, domain ownership, and edge availability.
3. Mirage stores approved service state in `FunnelService`.
4. Mirage renders a runtime config snapshot for the assigned edge.
5. Edge runtime applies the snapshot and reports readiness.
6. Control marks the service `active`, `degraded`, or `error`.

## Protocol Handling

### HTTP and HTTPS

- Route by `host + path`
- Proxy to normalized target machine address and port
- Preserve standard reverse proxy headers
- Support sticky WebSocket upgrades on the same service definition

### WS and WSS

- No separate routing plane
- Treated as HTTP Upgrade over the same host/path rule set
- Status and metrics remain attached to the parent Funnel service

### TCP

- Route by public listen port, optionally with SNI when available
- Create a raw stream tunnel to the normalized tailnet target

### TLS-terminated-TCP

- TLS terminates on the edge
- Decrypted TCP stream is forwarded to the target machine port
- Certificate ownership follows the selected domain, not the machine

## Target Addressing

Client-side local sources such as `http://localhost:8080` are not stored directly as the public runtime contract.

Mirage normalizes them into:

- `machine_id`
- `backend_tailnet_ip`
- `backend_port`
- `backend_scheme`

This keeps edge runtimes independent from client-specific local syntax.

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

### Client and daemon APIs

Local CLI remains:

- `mirage serve ...`
- `mirage funnel <port> on|off`

The daemon must gain control-plane sync for approved public service state. Client-side quick-start is supported for managed domains. Custom domain onboarding is handled in web UI, while CLI may control already-created services and query their state.

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

This is required so the UI can show the difference between:

- config saved
- DNS not ready
- certificate pending
- edge listener failed
- target node offline

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
2. `server-edge` runtime for managed-domain `HTTP/HTTPS/WS/WSS`
3. `TCP` and `TLS-terminated-TCP` runtime support
4. Custom-domain verification and certificate activation
5. `remote-edge` runtime and sync model
6. BYOC certificate ingestion and lifecycle operations

This design is the umbrella spec for Funnel v1. Each implementation plan must target one bounded slice from the list above.
