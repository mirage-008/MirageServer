# Mirage Invites And Mobile Cockpit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add explicit invite/share accept-reject flows, make cockpit tenant actions usable on mobile, and surface tenant self-registration through a backend capability flag instead of provider guessing.

**Architecture:** Keep the current Mirage console and cockpit structure intact, but add one thin invite portal slice and extend the existing invite/share models with an explicit `rejected` terminal state. The backend remains the source of truth for invite URLs and registration capability, while the frontend reuses existing dialogs and list views instead of introducing a parallel UI system.

**Tech Stack:** Go, Gorilla Mux, GORM, Vue 3, Vite multi-entry build, existing Mirage console/cockpit APIs, @superpowers:test-driven-development, @superpowers:verification-before-completion

---

## File Structure

### Existing files to modify

- `controller/share_invite.go`
  Adds `rejected` state, rejection timestamps, invite/share URL helpers, and terminal-state validation.
- `controller/share_invite_test.go`
  Covers reject transitions, repeat submission protection, and inviter-visible list state.
- `controller/console_api_users.go`
  Extends inviter-facing org invite payloads with URLs and rejection metadata.
- `controller/console_api_machine.go`
  Extends machine share payloads with URLs and rejection metadata; wires reject/accept behavior.
- `controller/console_api_common.go`
  Extends `/api/idps` response with `registration.enabled`.
- `controller/app.go`
  Registers public invite page routes and public invite decision/read APIs.
- `console_web/vite.config.js`
  Adds a new Vite entry for invite pages.
- `console_web/src/login.js`
  Keeps login entry focused on the login card while consuming the new registration capability.
- `console_web/src/login/Login.vue`
  Uses backend registration capability and promotes the registration CTA.
- `console_web/src/login/Register.vue`
  Keeps current dialog but is shown from capability-driven UI instead of `Ali` inference.
- `console_web/src/components/Users.vue`
  Shows inviter-facing org invite links, timestamps, and explicit statuses.
- `console_web/src/components/mmenu/ShareMachine.vue`
  Shows inviter-facing device share links, statuses, and timestamps.
- `console_web/src/components/Machines.vue`
  Replaces token-first badges with link/status-first badges.
- `console_web/src/components/Machine.vue`
  Mirrors the device detail share state and copy-link behavior.
- `cockpit_web/src/Tenants.vue`
  Replaces mobile-hover assumptions with explicit action triggers and modal/action-sheet behavior.
- `cockpit_web/src/components/TenantMenu.vue`
  Keeps desktop menu behavior while allowing mobile-specific rendering or action reuse.

### New files to create

- `controller/invite_portal.go`
  Public read/decision handlers and index-serving helpers for `/invite/*`.
- `controller/invite_portal_test.go`
  API coverage for invite/share read and accept/reject operations.
- `console_web/invite/index.html`
  New public invite SPA entry.
- `console_web/src/invite.js`
  Vue bootstrap for invite pages.
- `console_web/src/invite/InviteApp.vue`
  Shared invite page shell.
- `console_web/src/invite/InviteOrgView.vue`
  Org-invite-specific rendering.
- `console_web/src/invite/InviteDeviceView.vue`
  Device-share-specific rendering.

## Task 1: Registration Capability Flag

**Files:**
- Modify: `controller/console_api_common.go`
- Modify: `controller/oidc_config_test.go`
- Modify: `console_web/src/login/Login.vue`
- Test: `controller/oidc_config_test.go`

- [ ] **Step 1: Write the failing backend test**

Add a test next to the existing `/api/idps` coverage so it asserts the response includes:

```go
if got := data["registration"].(map[string]interface{})["enabled"]; got != true {
    t.Fatalf("registration.enabled = %v, want true", got)
}
```

- [ ] **Step 2: Run the targeted test to verify it fails**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestListIdps|Test.*Registration.*' -v`
Expected: FAIL because `/api/idps` does not yet emit `registration.enabled`.

- [ ] **Step 3: Implement the backend capability response**

Change `ListIdps` so the response shape includes:

```go
"registration": map[string]interface{}{
    "enabled": h.cfg.SMS.ID != "" && h.cfg.SMS.Key != "" && h.cfg.IDaaS.ClientID != "",
}
```

Keep the logic simple and explicit. Do not infer registration capability from `IdpList`.

- [ ] **Step 4: Update the login page to consume the new flag**

In `console_web/src/login/Login.vue`, replace:

```js
v-if="hasIDP('Ali')"
```

style registration gating with:

```js
const registrationEnabled = ref(false)
registrationEnabled.value = !!data["registration"]?.["enabled"]
```

Move the CTA into the main login card area and keep `Register.vue` as the existing dialog implementation.

- [ ] **Step 5: Re-run the targeted backend test**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestListIdps|Test.*Registration.*' -v`
Expected: PASS.

- [ ] **Step 6: Build the login frontend**

Run: `cd /home/hao/A-1/MirageServer/console_web && npm run build`
Expected: PASS and produce `controller/console_html/login`.

- [ ] **Step 7: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add controller/console_api_common.go controller/oidc_config_test.go console_web/src/login/Login.vue
git commit -m "console: drive registration CTA from backend capability"
```

## Task 2: Reject State in Invite Models

**Files:**
- Modify: `controller/share_invite.go`
- Modify: `controller/share_invite_test.go`
- Test: `controller/share_invite_test.go`

- [ ] **Step 1: Write failing reject-state tests**

Add tests that assert:

```go
accepted, err := app.RejectOrgInviteByToken(invite.InviteToken, user)
if err != nil { t.Fatalf(...) }
if accepted.Status != OrgInviteStatusRejected { t.Fatalf(...) }
if accepted.RejectedAt == nil { t.Fatal("expected rejected timestamp") }
```

and the equivalent for `MachineShare`.

Also add a repeat-submission assertion:

```go
if _, err := app.RejectMachineShareByToken(share.ShareToken, user); err == nil {
    t.Fatal("expected second reject to fail")
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'Test.*(Reject|Rejected).*' -v`
Expected: FAIL because `rejected` is not modeled yet.

- [ ] **Step 3: Implement the `rejected` terminal state**

In `controller/share_invite.go`:

- add constants:

```go
MachineShareStatusRejected = "rejected"
OrgInviteStatusRejected = "rejected"
```

- extend `MachineShare` and `OrgInvite` with:

```go
RejectedAt *time.Time
```

- add reject helper methods with terminal-state validation.

Prefer explicit helpers such as:

```go
func (h *Mirage) RejectOrgInviteByToken(token string, user *User) (*OrgInvite, error)
func (h *Mirage) RejectMachineShareByToken(token string, user *User) (*MachineShare, error)
```

- [ ] **Step 4: Re-run the reject-state tests**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'Test.*(Reject|Rejected).*' -v`
Expected: PASS.

- [ ] **Step 5: Run the full share/invite test file**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'Test(Create|Accept|Reject|Revoke).*' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add controller/share_invite.go controller/share_invite_test.go
git commit -m "controller: add rejected invite and share states"
```

## Task 3: Public Invite Portal APIs

**Files:**
- Create: `controller/invite_portal.go`
- Create: `controller/invite_portal_test.go`
- Modify: `controller/app.go`
- Modify: `controller/console_api_users.go`
- Modify: `controller/console_api_machine.go`
- Test: `controller/invite_portal_test.go`
- Test: `controller/share_invite_test.go`

- [ ] **Step 1: Write the failing portal API tests**

Cover at least:

```go
GET /api/invite/org/{token}
POST /api/invite/org/{token}/decision {"action":"reject"}
GET /api/invite/device/{token}
POST /api/invite/device/{token}/decision {"action":"accept"}
```

Assert that read responses include:

```go
status
targetIdentity
inviter
inviteURL or shareURL
registration.enabled
```

- [ ] **Step 2: Run the portal tests to verify they fail**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestInvitePortal' -v`
Expected: FAIL because the routes and handlers do not exist yet.

- [ ] **Step 3: Implement the invite/share portal handlers**

In `controller/invite_portal.go`:

- add read handlers for org invites and device shares
- add decision handlers for `accept` and `reject`
- reuse existing accept logic where possible
- return stable terminal-state payloads instead of generic errors

Use response fields like:

```go
type InvitePortalData struct {
    Kind         string `json:"kind"`
    Status       string `json:"status"`
    TargetIdentity string `json:"targetIdentity"`
    Registration map[string]bool `json:"registration"`
}
```

- [ ] **Step 4: Register routes in `controller/app.go`**

Add public API routes:

```go
router.HandleFunc("/api/invite/org/{token}", h.GetOrgInvitePortal).Methods(http.MethodGet)
router.HandleFunc("/api/invite/org/{token}/decision", h.PostOrgInviteDecision).Methods(http.MethodPost)
router.HandleFunc("/api/invite/device/{token}", h.GetDeviceSharePortal).Methods(http.MethodGet)
router.HandleFunc("/api/invite/device/{token}/decision", h.PostDeviceShareDecision).Methods(http.MethodPost)
```

Also add a public page route that serves the invite SPA for `/invite/*`.

- [ ] **Step 5: Extend inviter-facing payloads**

Update `buildPendingInviteData` and `buildMachineShareResponse` to include:

```go
InviteURL  string
ShareURL   string
RejectedAt *time.Time
```

and return those from existing create/list endpoints.

- [ ] **Step 6: Re-run the portal tests**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller -run 'TestInvitePortal' -v`
Expected: PASS.

- [ ] **Step 7: Run the controller package**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add controller/invite_portal.go controller/invite_portal_test.go controller/app.go controller/console_api_users.go controller/console_api_machine.go
git commit -m "controller: add public invite portal APIs"
```

## Task 4: Invite Portal Frontend

**Files:**
- Create: `console_web/invite/index.html`
- Create: `console_web/src/invite.js`
- Create: `console_web/src/invite/InviteApp.vue`
- Create: `console_web/src/invite/InviteOrgView.vue`
- Create: `console_web/src/invite/InviteDeviceView.vue`
- Modify: `console_web/vite.config.js`
- Test: `console_web` build output

- [ ] **Step 1: Add the failing frontend integration point**

Add the Vite input entry:

```js
invite: resolve(__dirname, 'invite/index.html'),
```

without the files yet, then run the build.

- [ ] **Step 2: Run the frontend build to verify it fails**

Run: `cd /home/hao/A-1/MirageServer/console_web && npm run build`
Expected: FAIL because the invite entry files do not exist yet.

- [ ] **Step 3: Implement the invite SPA**

Create the invite entry files and keep the page logic shallow:

- `InviteApp.vue` reads the current path and fetches the portal payload
- `InviteOrgView.vue` renders org invite details
- `InviteDeviceView.vue` renders device share details
- logged-out state shows `登录` and conditional `注册`
- terminal states render directly from API payload

Keep routing simple; do not introduce a full router if path parsing inside the invite app is sufficient.

- [ ] **Step 4: Re-run the frontend build**

Run: `cd /home/hao/A-1/MirageServer/console_web && npm run build`
Expected: PASS and produce `controller/console_html/invite`.

- [ ] **Step 5: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add console_web/vite.config.js console_web/invite/index.html console_web/src/invite.js console_web/src/invite
git commit -m "console: add invite landing pages"
```

## Task 5: Console Invite and Share Views

**Files:**
- Modify: `console_web/src/components/Users.vue`
- Modify: `console_web/src/components/mmenu/ShareMachine.vue`
- Modify: `console_web/src/components/Machines.vue`
- Modify: `console_web/src/components/Machine.vue`
- Test: `console_web` build output

- [ ] **Step 1: Write the UI changes against the new backend fields**

Update org invite cards to show:

```js
invite.inviteURL
invite.rejectedAt
invite.acceptedAt
```

and update machine share cards/badges to show:

```js
share.shareURL
share.rejectedAt
```

Replace "复制令牌" with copy-link behavior where the backend returns a URL.

- [ ] **Step 2: Run the frontend build to catch template or field mismatches**

Run: `cd /home/hao/A-1/MirageServer/console_web && npm run build`
Expected: PASS.

- [ ] **Step 3: Perform a quick manual smoke against the existing views**

Run the dev server only if needed; otherwise verify from code that:

- pending and rejected statuses have distinct text
- accepted/rejected timestamps do not render when empty
- copy buttons use the URL fields first

- [ ] **Step 4: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add console_web/src/components/Users.vue console_web/src/components/mmenu/ShareMachine.vue console_web/src/components/Machines.vue console_web/src/components/Machine.vue
git commit -m "console: surface invite links and decision states"
```

## Task 6: Cockpit Mobile Tenant Actions

**Files:**
- Modify: `cockpit_web/src/Tenants.vue`
- Modify: `cockpit_web/src/components/TenantMenu.vue`
- Test: `cockpit_web` build output

- [ ] **Step 1: Make the mobile action trigger explicit**

Refactor `Tenants.vue` so the mobile card view uses a button like:

```vue
<button class="btn btn-sm ..." type="button">操作</button>
```

and opens a modal or action-sheet style wrapper instead of relying on hover positioning.

- [ ] **Step 2: Keep desktop behavior intact**

Preserve the existing floating menu for `md` and up, but isolate the mobile interaction path so mobile state does not depend on `mouseenter`.

- [ ] **Step 3: Build cockpit frontend**

Run: `cd /home/hao/A-1/MirageServer/cockpit_web && npm run build`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
cd /home/hao/A-1/MirageServer
git add cockpit_web/src/Tenants.vue cockpit_web/src/components/TenantMenu.vue
git commit -m "cockpit: make tenant actions usable on mobile"
```

## Task 7: Final Verification

**Files:**
- Verify all files touched in Tasks 1-6

- [ ] **Step 1: Run backend test suite**

Run: `cd /home/hao/A-1/MirageServer && go test ./controller`
Expected: PASS.

- [ ] **Step 2: Run full Go build**

Run: `cd /home/hao/A-1/MirageServer && go build ./...`
Expected: PASS.

- [ ] **Step 3: Run console frontend build**

Run: `cd /home/hao/A-1/MirageServer/console_web && npm run build`
Expected: PASS.

- [ ] **Step 4: Run cockpit frontend build**

Run: `cd /home/hao/A-1/MirageServer/cockpit_web && npm run build`
Expected: PASS.

- [ ] **Step 5: Manual checklist**

Verify:

- org invite create -> open link -> reject -> inviter sees `rejected`
- device share create -> open link -> accept/reject -> owner sees terminal state
- invite page while logged out shows `登录` and conditional `注册`
- cockpit tenant action buttons work at phone width
- login page registration CTA only appears when `/api/idps` says it is enabled

- [ ] **Step 6: Final commit or squash strategy**

If the per-task commits are clean, keep them.
If the branch should be simplified before merge, coordinate explicitly before rewriting history.
