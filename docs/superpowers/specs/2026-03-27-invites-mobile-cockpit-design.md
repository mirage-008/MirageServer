# Mirage Invites And Mobile Cockpit Design

## Goal

Improve three user-facing workflows in `MirageServer` without changing the ACL editor in this round:

- turn organization invites and device shares into real invitation links with explicit `accept` / `reject` outcomes
- make cockpit tenant management usable on mobile instead of depending on desktop hover menus
- make the normal tenant login page show a clear, capability-driven user registration entry

## Current Problems

### Invite and share flow

Current organization invites and device shares are only partially productized:

- organization invites are created in the console, but they are effectively "login later and auto-join"
- device shares expose a raw token, not a user-facing invite link
- neither flow has a dedicated recipient page with explicit `accept` / `reject`
- current status is effectively only `pending`, `accepted`, and `revoked`
- the inviter cannot clearly see whether the recipient refused

### Cockpit mobile tenant management

`cockpit_web/src/Tenants.vue` relies on desktop-style menu behavior:

- row actions are anchored from a floating three-dot menu
- hover state and viewport-relative positioning are part of the interaction model
- desktop works, but mobile does not reliably expose edit/remove actions

### Tenant login registration entry

`console_web/src/login/Login.vue` currently decides whether to show the registration entry by checking for `Ali`:

- registration visibility is coupled to one provider name
- the login page can hide registration even when self-registration is operational
- the placement is weak and easy to miss

## Scope

### In Scope

- organization invite links
- device share links
- explicit invitation recipient pages
- explicit `rejected` state and timestamps
- inviter-side status visibility in tenant console
- mobile-friendly cockpit tenant action UI
- capability-driven tenant login registration entry
- backend and frontend tests for the above

### Out of Scope

- ACL visual editor redesign
- email delivery
- SMS delivery redesign
- OIDC login flow redesign
- role or tenant model changes
- full authentication system rewrite

## Product Behavior

## Invitation Flows

Two invitation types must behave consistently:

1. Organization invite
- inviter creates an invite for a target identity
- system returns a shareable invite URL
- recipient opens the URL and sees a dedicated invite page
- recipient can explicitly `accept` or `reject`
- inviter sees `pending`, `accepted`, `rejected`, or `revoked`

2. Device share
- device owner creates a share for a target identity
- system returns a shareable invite URL
- recipient opens the URL and sees the shared device context
- recipient can explicitly `accept` or `reject`
- owner sees `pending`, `accepted`, `rejected`, or `revoked`

These two flows should feel like the same product, not two unrelated token mechanisms.

## Recipient Experience

The recipient page must show:

- what is being invited or shared
- who initiated it
- current invite status
- a clear explanation of what `accept` will do
- a clear `accept` button
- a clear `reject` button

If the recipient is not logged in:

- the page should stay readable
- the page should prompt for `登录` and, if enabled, `注册`
- after authentication, the user should be able to return to the pending invite page

If the invite is already handled:

- the page should render a terminal state instead of a generic error
- examples: `已接受`, `已拒绝`, `已撤销`, `邀请无效`

## Inviter Experience

The inviter-side console UI must show:

- a copyable invitation link, not only a raw token
- the current status of each invite/share
- creation time
- decision time when accepted or rejected
- revoke action for pending items

Pending and terminal states should be easy to scan from the existing list views.

## Data Model Changes

### `OrgInvite`

Add:

- `RejectedAt *time.Time`

Extend status enum:

- `pending`
- `accepted`
- `rejected`
- `revoked`

### `MachineShare`

Add:

- `RejectedAt *time.Time`

Extend status enum:

- `pending`
- `accepted`
- `rejected`
- `revoked`

## State Machine

Shared rules for both `OrgInvite` and `MachineShare`:

- `pending -> accepted`
- `pending -> rejected`
- `pending -> revoked`

Terminal states:

- `accepted`
- `rejected`
- `revoked`

Invalid transitions:

- `accepted -> rejected`
- `rejected -> accepted`
- `revoked -> accepted`
- `revoked -> rejected`

The API must reject invalid transitions with explicit business errors instead of silently overwriting previous decisions.

## Backend Design

## New or Extended Backend Responsibilities

### Invite status and timestamps

Extend the existing invitation logic in `controller/share_invite.go` so that both models support:

- reject operations by recipient
- terminal-state validation
- terminal-state timestamps

### Invite URL construction

The backend should return full URLs for both invite types instead of making the frontend infer them from tokens.

Recommended URL forms:

- organization invite: `/invite/org/<token>`
- device share: `/invite/device/<token>`

The backend should derive absolute URLs from the current server base URL so the frontend can copy them directly.

### Recipient-facing APIs

Add recipient-side read and decision endpoints for both invite types.

Read endpoints must return:

- invite type
- status
- inviter identity
- recipient identity
- target object metadata
- whether login is required
- whether self-registration is enabled

Decision endpoints must support:

- `accept`
- `reject`

These endpoints should enforce:

- token validity
- state validity
- identity matching rules
- authorization for the current user

### Inviter-facing APIs

Existing inviter-side list or create endpoints should be extended to return:

- `inviteURL` or `shareURL`
- `rejectedAt`
- `acceptedAt`
- current status

Current create/revoke endpoints can stay in place if their response shape is upgraded.

## Frontend Design

## Tenant Console: Users

`console_web/src/components/Users.vue` should be upgraded so that pending invites show:

- target identity
- status badge
- created time
- accepted or rejected time when present
- copyable invite link
- revoke action for pending items

The section should remain owner-only for mutation actions.

## Tenant Console: Machines

`console_web/src/components/mmenu/ShareMachine.vue`
`console_web/src/components/Machines.vue`
`console_web/src/components/Machine.vue`

should be upgraded so that device shares show:

- copyable invite link
- status badge
- pending/accepted/rejected counts when applicable
- accepted or rejected timestamps in detail views

The UI should stop foregrounding the raw token as the primary artifact.

## New Invite Pages

Add new console-web routes and pages for:

- organization invite landing
- device share landing

These pages should:

- render a readable pending state without login
- offer `登录`
- offer `注册` when allowed
- render terminal states cleanly
- submit accept/reject after login

## Cockpit Mobile Tenant Management

`cockpit_web/src/Tenants.vue` should no longer depend on hover-first behavior on mobile.

Recommended behavior:

- desktop keeps the current row/action feel
- mobile uses explicit `操作` buttons on each card or row
- tapping opens a modal or action sheet
- the modal contains:
  - tenant summary
  - `编辑租户`
  - `移除租户`

The existing `EditTenant.vue` and `RemoveTenant.vue` dialogs should be reused.

The goal is to replace the trigger pattern, not rewrite tenant mutation logic.

## Tenant Login Registration Entry

`/api/idps` should return a registration capability object rather than forcing the frontend to infer registration support from provider names.

Recommended response addition:

- `registration.enabled`

Optional future-safe additions:

- `registration.label`
- `registration.mode`

`console_web/src/login/Login.vue` should then:

- show registration entry when `registration.enabled == true`
- hide it when false
- stop checking `hasIDP('Ali')` for registration visibility
- move the registration CTA into the main login card area so it is easy to discover

`console_web/src/login/Register.vue` remains the registration dialog for now, but its visibility is driven by backend capability instead of provider guessing.

## Error Handling

Recipient-facing pages must clearly distinguish:

- token not found
- revoked
- already rejected
- already accepted
- login required
- logged-in identity does not match the invite target

Do not collapse these into generic "操作失败".

Inviter-facing mutation errors should preserve business meaning:

- already handled
- already revoked
- invalid target identity
- target already in organization
- target belongs to another organization

## Files Likely To Change

### Backend

- `controller/share_invite.go`
- `controller/share_invite_test.go`
- `controller/console_api_users.go`
- `controller/console_api_machine.go`
- `controller/console_api_common.go`
- `controller/app.go`
- possibly new invite handlers under `controller/console_*` or a dedicated invite handler file

### Tenant Console Frontend

- `console_web/src/components/Users.vue`
- `console_web/src/components/mmenu/ShareMachine.vue`
- `console_web/src/components/Machines.vue`
- `console_web/src/components/Machine.vue`
- `console_web/src/login/Login.vue`
- `console_web/src/login/Register.vue`
- new invite page components and route wiring

### Cockpit Frontend

- `cockpit_web/src/Tenants.vue`
- `cockpit_web/src/components/TenantMenu.vue`
- possibly a new mobile action-sheet wrapper component

## Testing Strategy

### Backend Tests

Add or extend Go tests for:

- organization invite reject flow
- device share reject flow
- terminal-state re-submit protection
- revoke-after-terminal protection
- inviter list payload including invite URLs
- recipient read payload including terminal states
- registration capability returned by `/api/idps`

### Frontend Verification

Run:

- `cd /home/hao/A-1/MirageServer/console_web && npm run build`
- `cd /home/hao/A-1/MirageServer/cockpit_web && npm run build`

### Manual Validation

Verify at minimum:

1. Owner creates an organization invite and sees a copyable link.
2. Recipient opens the link while logged out and sees login/register options.
3. Recipient accepts and inviter sees `accepted`.
4. Recipient rejects and inviter sees `rejected`.
5. Owner creates a device share and sees a copyable link.
6. Recipient rejects the device share and owner sees `rejected`.
7. Cockpit tenant actions are reachable on a phone-width viewport.
8. Tenant login page shows registration only when backend says registration is enabled.

## Risks

- invite routing can easily become inconsistent if org-invite and device-share pages are implemented separately without shared UI conventions
- mixing login redirect logic with invite resolution can create broken return paths
- mobile cockpit fixes can regress desktop menus if both modes are not deliberately separated
- tying registration visibility to provider names again would recreate the current problem in a different form

## Recommendation

Implement this as one user-experience batch focused on invitations and discoverability:

- unify invite semantics first
- then expose the capability clearly in UI
- keep ACL editor work for a separate spec and plan

This preserves scope while resolving the concrete usability problems reported in this round.
