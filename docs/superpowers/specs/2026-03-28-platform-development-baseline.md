# Mirage Platform Development Baseline

## Core Thesis

Mirage should not be developed as "a self-hosted `headscale` plus a few `tailscale` patches".

The platform baseline is:

- use official upstream `tailscale` data-plane contracts and client semantics as the protocol baseline
- use `headscale` as the control-plane behavior and test-depth reference
- layer Mirage-specific `edge`, `logging`, and product features on top of that shared contract

This is the only direction that keeps Mirage compatible with official upstream clients while also keeping the local Mirage client fork rebase-friendly.

## Product Boundary

### `MirageServer`

`MirageServer` is the control-plane-centered product repository. It owns:

- controller behavior and protocol responses
- tenant/admin product surfaces
- organization, user, device, policy, and identity management
- Funnel platform control plane
- flow-log collection, query, retention, export, and audit-plane features
- share/invite and other Mirage product workflows

### `tailscale`

`/home/hao/A-1/tailscale` is the active Mirage client repository. It owns:

- CLI and daemon behavior
- Windows GUI and Linux GUI
- managed `Navi` / DERP runtime
- client-side `serve` / `funnel` intent handling
- data-plane-facing compatibility work

This repository is the long-term Mirage client base and should stay as close as practical to upstream structure and semantics.

### `tailscale-android`

`/home/hao/A-1/tailscale-android` is the future target for Mirage Android adaptation. Mobile Mirage work should land there, not in deprecated client forks.

### Legacy / Reference Repositories

`MirageClient` is deprecated and should be treated as reference-only unless a task explicitly needs archaeology or migration comparison.

`headscale` is a control-plane reference, not a client implementation baseline.

## Upstream Alignment Rules

### Official Client Meaning

When Mirage documentation says "official `tailscale` client", it means the upstream `tailscale` mainline.

### Protocol and Data-Plane Baseline

Mirage should follow upstream `tailscale` for:

- `tailcfg` and control-plane contract shape
- client-side `serve` / `funnel` semantics
- flow-log / logtarget / netlog behavior
- DERP, netcheck, and managed edge runtime expectations

### Control-Plane Reference Baseline

Mirage should use `headscale` for:

- control-plane behavior comparisons
- policy/admin semantics
- server-side test strategy and convergence expectations

## Repository Ownership Rules

- Put control-plane, cockpit/console, tenant/admin workflow, policy, Funnel platform orchestration, flow-log platform, and product backend work in `MirageServer`.
- Put client, GUI, daemon, managed DERP/Navi runtime, serve/funnel intent sync, and data-plane-facing protocol work in `tailscale`.
- Put Mirage Android work in `tailscale-android`.
- Do not route new primary product work through deprecated `MirageClient`.

## Non-Goals

The baseline is explicitly not:

- keeping `MirageServer` tied to legacy local client forks
- treating `MirageClient` as the long-term active client implementation
- inventing Mirage-only contracts where upstream-compatible structures already exist
- using `headscale` as a substitute for Mirage client or data-plane implementation

## Long-Term Outcome

If this baseline is followed consistently:

- `MirageServer` can keep official-client compatibility without repeated structural regressions
- `/home/hao/A-1/tailscale` can keep rebasing against upstream with lower merge pressure
- Mirage-specific features such as managed edge, self-hosted flow logs, and product UX stay additive instead of forking the protocol model

## Immediate Execution Consequence

For new development:

- first decide whether the change is control-plane/product work or client/data-plane work
- place the change in the owning repository from the start
- prefer upstream-compatible schemas and semantics over Mirage-only rewrites
- use `headscale` to validate behavior depth, not to define client architecture
