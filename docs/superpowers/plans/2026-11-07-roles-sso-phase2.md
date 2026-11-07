# Roles & SSO, phase 2: people and SSO. Implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** People other than root sign in to a site through its Google or Auth0 provider, if and only if they hold a membership there. Site admins add and invite people. Contributors work under the role table, and platform admins get root's platform views.

**Architecture:**
- **Models** gain stores for memberships, identities and invites. Invite acceptance is one transaction inside `models`. Root guards live in the stores, so no handler can bypass them.
- **`people`** holds the sign-in policy: invite, then known identity, then email match, then the membership check.
- **`sso`** wraps go-oidc and oauth2 behind two calls, `AuthCodeURL` and `Exchange`, plus the signed `ntx_oauth` flow cookie. `sso/ssotest` is an in-process fake provider.
- **`authz`** is the only place roles are compared. Handlers call it; templates receive booleans computed from it.
- **`auth.Manager`** loads the current membership on every request, so revocations take effect on the next request.

**Tech Stack:** Go 1.26, chi v5, GORM, `github.com/coreos/go-oidc/v3` (v3.21.0), `golang.org/x/oauth2` (v0.37.0), `github.com/go-jose/go-jose/v4` (tests only, already a go-oidc dependency), `crypto/aes` + `crypto/cipher` (GCM).

**Spec:** `docs/superpowers/specs/2026-11-07-roles-and-sso-design.md`. Phase 2 is "Build order" item 2. Pending revisions (`post_revisions`, Publish changes, Discard) are phase 3 and out of scope. In this phase a contributor without `can_publish` gets a read-only editor for live-once posts.

## Global Constraints

- **No schema change.** `100_initial.sql` already has every table and column this phase uses. Don't edit the migrations.
- **Providers:**
  - `provider_type` is `""`, `google` or `auth0`.
  - Configured issuers: Google `https://accounts.google.com`; Auth0 `https://<domain>/` (trailing slash).
  - Identities store the configured issuer, never the token's `iss`.
- **OIDC flow:**
  - Authorization code flow with `state`, `nonce` and PKCE (S256). Scopes: `openid email profile`.
  - Callback URL: `https://<site hostname>/auth/callback`. In dev mode it's `clientip.Scheme(r) + "://" + r.Host + "/auth/callback"`.
- **The `ntx_oauth` cookie:**
  - Holds state, nonce, PKCE verifier, site ID, the optional invite token and an expiry.
  - HMAC-SHA256-signed with `session_secret`.
  - 10 minutes, `SameSite=Lax`, HttpOnly, `Secure` unless dev, `Path=/auth`.
  - The callback clears it whatever the outcome.
- **Callback requirements:**
  - The flow's site ID equals the resolved site's.
  - `state` matches.
  - The ID token verifies (issuer, audience = client ID, expiry, nonce).
  - `email_verified` is true.
- **Sign-in finish:**
  - On success, a session for (user, this site), then an interstitial page with `<meta http-equiv="refresh" content="0;url=/admin/posts">` and a plain link.
  - No access: HTTP 403 with exactly "You don't have access to this site." and nothing that reveals whether the email is known.
- **Emails** are trimmed and lowercased before every store, compare or lookup. `users.email` is never updated from a provider; `users.name` is refreshed from the provider's `name` claim at each SSO sign-in, if non-empty.
- **Reassigned-account guard.** Linking (issuer, subject) to a user who already has an identity with that issuer and a different subject is refused.
- **Invites:**
  - Token: 32 random bytes, base64url. The DB stores the hex sha256.
  - Expire after 7 days.
  - Link: `https://<site hostname>/invite/<token>` (dev: request scheme and Host). Shown once.
  - One pending invite per (site, email); creating another replaces it.
  - Inviting or adding an existing member is a form error.
  - Acceptance marks the invite used with `UPDATE … WHERE id = ? AND used_at IS NULL`, checking the affected row count.
- **Secrets:**
  - AES-256-GCM with `encryption_key`, a random 12-byte nonce per value, stored as nonce‖ciphertext.
  - Never rendered to the browser: forms show "set" / "not set".
  - Saving with an empty secret keeps the stored one only if `provider_type`, `provider_domain` and `provider_client_id` are all unchanged. Otherwise it's a form error.
- **Root:**
  - No web or store action may give root an email, identity, membership or name change.
  - Nothing may delete root, or change its `is_root` or `is_platform_admin`.
  - These are enforced in the stores.
- **Sessions:**
  - A non-root session is valid only with a current membership on its own site, and never with a nil site.
  - User, membership and platform flag are re-read on every request.
- **Role table** (spec § Roles) is implemented only in `internal/authz`. Root and platform admins have site-admin powers on the site they're signed in to. Site admins can't change, or remove, a platform admin's membership.
- **Request logging:** the request log prints `/invite/[redacted]` for any `/invite/…` path, and drops the query string of `/auth/callback`.
- **Admin pages:**
  - Every new embedded page is added to `adminui.pageNames`.
  - The CSP is unchanged; no inline script.
  - The new `RenderStatus` lets admin pages carry a non-200 status.
- **Unchanged files:** don't change `internal/clientip`, `internal/validate` (except `Email`), `internal/themes` or the shipped themes.
- Commit after each task. Run `go vet ./... && go test ./...` before each commit.

## Review Focus

1. **Email case and whitespace.** An invite for `" Alice@Example.COM "` is accepted by a provider login reporting `alice@example.com`. Adding `Bob@x.io` when `bob@x.io` is already a member is a duplicate. Tested in Task 3 (`TestInviteEmailNormalised`) and Task 2 (`TestFindOrCreateByEmailNormalises`).
2. **Provider unreachable or misconfigured.** A typo'd Auth0 domain, or a provider that's down, at `/auth/start`: a 502 page saying the sign-in provider couldn't be reached, nothing cached, and the next try re-attempts discovery. Tested in Task 7 (`TestStartProviderUnreachable`, `TestDiscoveryFailureNotCached`).
3. **Stale or missing flow cookie.** Callback opened twice, after 10 minutes, or in another browser: a 400 page "Your sign-in expired. Try again." with a link to `/admin/login`. Never a 500 or a session. Tested in Task 7 (`TestCallbackWithoutFlowCookie`, `TestCallbackExpiredFlow`).
4. **User cancels at the provider.** A callback with `?error=access_denied` gets a 400 page "Sign-in was cancelled." and no session. Tested in Task 7 (`TestCallbackProviderError`).
5. **A stored secret that no longer decrypts** (`encryption_key` changed): `/auth/start` shows a 502 page saying the provider's client secret must be re-entered, and logs it. The platform form still shows "set" so the secret can be overwritten. Tested in Task 7 (`TestStartUndecryptableSecret`).

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/validate/validate.go` | + `Email` |
| `internal/secretbox/secretbox.go` (new) | AES-256-GCM seal/open |
| `internal/models/site.go` | Provider settings, the secret-clearing rule, `ProviderSecretSet` |
| `internal/models/user.go` | Lookup by email, list, edit and delete, with root guards |
| `internal/models/membership.go` (new) | `MembershipStore` |
| `internal/models/identity.go` (new) | `IdentityStore`, reassigned-account guard |
| `internal/models/invite.go` (new) | `InviteStore`, transactional `Accept` |
| `internal/models/session.go` | + `DeleteForUser` |
| `internal/models/post.go` | `AuthorID`, `ListByAuthor` |
| `internal/auth/auth.go` | `Actor.Membership`; membership-checked sessions |
| `internal/authz/authz.go` (new) | The role table |
| `internal/sso/sso.go`, `internal/sso/flow.go` (new) | OIDC client, flow cookie |
| `internal/sso/ssotest/ssotest.go` (new) | Fake OIDC provider for tests |
| `internal/people/people.go` (new) | Sign-in policy, add-member |
| `internal/handlers/signin.go` (new) | `/auth/start`, `/auth/callback`, `/invite/{token}` |
| `internal/handlers/members.go` (new) | `/admin/members…` |
| `internal/handlers/users.go` (new) | `/admin/platform/users…` |
| `internal/handlers/admin.go`, `platform.go`, `routes.go`, `handler.go` | authz-gated posts; provider form; route table; Deps |
| `internal/adminui/` | New pages `message`, `signed_in`, `invite`, `members`, `platform_users`, `platform_user`; edits to `layout`, `login`, `posts`, `editor`, `platform_site_form`, `platform_sites` |
| `internal/server/server.go` | Log redaction |
| `main.go` | Wiring |
| Docs | `docs/usage.md`, `docs/ai-context.md`, `README.md` |

---

### Task 1: Email rule, secretbox, provider settings in the site store

**Files:**
- Modify: `internal/validate/validate.go`, `internal/validate/validate_test.go`, `internal/models/site.go`, `internal/models/site_test.go`
- Create: `internal/secretbox/secretbox.go`, `internal/secretbox/secretbox_test.go`

**Interfaces:**
- Produces:
  - `validate.Email(s string) error`. Takes an already trimmed and lowercased value. Needs exactly one `@`, non-empty local part and domain, a `.` in the domain, no whitespace or control characters, and at most 254 bytes.
  - `secretbox.Seal(key []byte, plaintext string) ([]byte, error)`, `secretbox.Open(key, sealed []byte) (string, error)`, `secretbox.ErrOpen`.
  - `models.Site.ProviderSecretSet bool`, filled by `List`.
  - `models.ProviderSettings{Type, Domain, ClientID string}`.
  - `(*SiteStore).SetProvider(siteID int64, p ProviderSettings, sealedSecret []byte) error`.
  - `(*SiteStore).ProviderSecret(siteID int64) ([]byte, error)`, which returns nil when there is no secret.
  - Constants `models.ProviderGoogle = "google"` and `models.ProviderAuth0 = "auth0"`.

- [ ] **Step 1: Write the failing tests**
  - `TestEmail`:
    - valid: `a@b.co`, `first.last+tag@sub.example.com`;
    - invalid: `""`, `a`, `a@`, `@b.co`, `a@b`, `a@@b.co`, `a b@c.co`, `a@b.co\n`, and a 255-byte address.
  - `TestSealOpenRoundTrip`: sealing the same plaintext twice gives different bytes. Both open to the plaintext.
  - `TestOpenWrongKey`: a value sealed with key A doesn't open with key B: `errors.Is(err, secretbox.ErrOpen)`. A truncated value gives `ErrOpen` and no panic.
  - `TestSetProviderClearsSecretOnChange`:
    1. Set auth0 / `acme.us.auth0.com` / `cid` with sealed secret `s1`.
    2. Change the domain to `evil.example.com` with a nil secret. That's a `*ValidationError` containing "client secret", and `ProviderSecret` still returns `s1`.
    3. The same change with a new secret succeeds.
    4. Changing only the client ID with a nil secret is a `ValidationError`.
    5. Changing the type with a nil secret is a `ValidationError`.
  - `TestSetProviderKeepsSecretWhenUnchanged`: re-saving the same type, domain and client ID with a nil secret succeeds, and the secret is unchanged.
  - `TestSetProviderValidation`, all `ValidationError`:
    - type `github`;
    - auth0 with domain `https://x.com`;
    - auth0 with an empty domain;
    - any type with an empty client ID;
    - first save with a nil secret.

    Google with a domain stores the domain as `""`.
  - `TestSetProviderNone`: type `""` clears type, domain, client ID and secret. `List()` shows `ProviderSecretSet == false`.

- [ ] **Step 2: Run them; expect a compile failure (undefined `validate.Email`, `secretbox`, `SetProvider`)**

  Run: `go test ./internal/validate ./internal/secretbox ./internal/models`

- [ ] **Step 3: Implement**
  - `secretbox`: `aes.NewCipher` on the 32-byte key, then `cipher.NewGCM`. The nonce comes from `crypto/rand`.
  - `SetProvider`:
    1. Normalise: trim, and lowercase the domain.
    2. Validate.
    3. Load the current row.
    4. With nil `sealedSecret`, keep the stored secret only if type, domain and client ID are all equal and a secret is stored. Otherwise return one of these `ValidationError`s:
       - "enter the client secret again: the provider, domain or client ID changed";
       - "a client secret is required".
    5. Write the four columns and `updated_at` in one `Updates`.
  - Add `ProviderClientSecretEnc []byte` to `dbSite`. `List` sets `ProviderSecretSet = len(enc) > 0`.

- [ ] **Step 4: Run tests: PASS.** `go test ./internal/validate ./internal/secretbox ./internal/models`
- [ ] **Step 5: Commit** "Store per-site provider settings with an encrypted secret"

---

### Task 2: Users, memberships and identities

**Files:**
- Modify: `internal/models/user.go`, `internal/models/session.go`
- Create: `internal/models/membership.go`, `internal/models/identity.go`, `internal/models/people_test.go`

**Interfaces:**
- Consumes: `validate.Email`.
- Produces:
  - `models.ErrRootProtected`, a `*ValidationError` with "root can't be changed from the web".
  - User lookups, all lowercasing and trimming the email first; root is never returned by an email lookup:
    - `(*UserStore).FindByEmail(email string) (*User, error)`, which returns nil, nil when there's no match;
    - `(*UserStore).FindOrCreateByEmail(email string) (*User, error)`, which validates with `validate.Email`;
    - `(*UserStore).List() ([]User, error)`, with root first, then by email;
    - `(*UserStore).GetByIDs(ids []int64) (map[int64]*User, error)`.
  - User writes, each of which returns `ErrRootProtected` for root:
    - `SetEmail(id int64, email string) error` (a duplicate is a `ValidationError`);
    - `SetPlatformAdmin(id int64, on bool) error`;
    - `SetName(id int64, name string) error`;
    - `Delete(id int64) error`.
  - Memberships:
    - `models.Membership{ID, UserID, SiteID int64; Role string; CanPublish bool}`;
    - `models.RoleAdmin = "admin"`, `models.RoleContributor = "contributor"`;
    - `models.Member{Membership Membership; User User}`.
  - `MembershipStore`, from `NewMembershipStore(db)`:
    - `Get(userID, siteID int64) (*Membership, error)`, which returns nil when there's none;
    - `Add(userID, siteID int64, role string, canPublish bool) error`, which returns `ErrRootProtected` for root; an invalid role, or an existing membership ("already a member of this site"), is a `ValidationError`;
    - `Update(userID, siteID int64, role string, canPublish bool) error`;
    - `Remove(userID, siteID int64) error`;
    - `ListForSite(siteID int64) ([]Member, error)`, ordered by email;
    - `ListForUser(userID int64) ([]Membership, error)`;
    - `CountBySite() (map[int64]int, error)`.
  - Identities:
    - `models.Identity{ID, UserID int64; Issuer, Subject, EmailAtLink string; CreatedAt time.Time}`;
    - `models.ErrIdentityConflict`, a plain `errors.New`.
  - `IdentityStore`, from `NewIdentityStore(db)`:
    - `Find(issuer, subject string) (*Identity, error)`, which returns nil when there's none;
    - `Link(userID int64, issuer, subject, email string) error`. It's a no-op if this exact identity is already linked to this user. It returns `ErrRootProtected` for root, and `ErrIdentityConflict` if the user has the same issuer with another subject, or the (issuer, subject) belongs to another user;
    - `ListForUser(userID int64) ([]Identity, error)`;
    - `Unlink(id, userID int64) error`.
  - Package-internal `linkIdentityTx(tx *gorm.DB, userID int64, issuer, subject, email string) error`, which `Link` and Task 3 share.
  - `(*SessionStore).DeleteForUser(userID int64) error`.

- [ ] **Step 1: Write the failing tests** (`people_test.go`)
  - `TestRootGuards`: on root, each of these returns an error matching `errors.Is(err, ErrRootProtected)`:
    - `SetEmail`, `SetPlatformAdmin(true)`, `SetPlatformAdmin(false)`, `SetName`, `Delete`;
    - `Memberships.Add`;
    - `Identities.Link`.

    Root is unchanged after all of them.
  - `TestFindOrCreateByEmailNormalises`: `FindOrCreateByEmail(" Bob@X.io ")` creates `bob@x.io`. A second call with `bob@x.io` returns the same ID. `FindByEmail("BOB@x.io")` finds it. `FindOrCreateByEmail("nope")` is a `ValidationError`.
  - `TestMembershipAddTwiceRejected`: the second `Add` for the same (user, site) is a `ValidationError`. A `"owner"` role is a `ValidationError`.
  - `TestReassignedAccountGuard`:
    - `Link(u, "iss", "sub1", …)` succeeds, and re-linking the same pair is nil.
    - `Link(u, "iss", "sub2", …)` gives `ErrIdentityConflict`.
    - `Link(u, "other-iss", "sub2", …)` succeeds.
    - `Link(otherUser, "iss", "sub1", …)` gives `ErrIdentityConflict`.
  - `TestDeleteUserKeepsPosts`: a post authored by u keeps its content after `Delete(u)`, with `AuthorID == nil` (needs Task 5's `AuthorID`, so write the row through `db.Exec` here). u's memberships, identities and sessions are gone.
  - `TestDeleteForUserRevokesOnlyThatUser`.

- [ ] **Step 2: Run; expect compile failures.** `go test ./internal/models`
- [ ] **Step 3: Implement.** `dbMembership` and `dbIdentity` map to the existing tables. A duplicate email in `SetEmail` is detected by a count before the update and returned as `ValidationError` ("<email> is already used by another person").
- [ ] **Step 4: Run tests: PASS.** `go test ./internal/models`
- [ ] **Step 5: Commit** "Add memberships and identities with root guards"

---

### Task 3: Invites

**Files:**
- Create: `internal/models/invite.go`, `internal/models/invite_test.go`

**Interfaces:**
- Consumes: Task 2 stores and `linkIdentityTx`.
- Produces:
  - `models.Invite{ID, SiteID int64; Email, Role string; CanPublish bool; ExpiresAt, CreatedAt time.Time}`.
  - `models.InviteTTL = 7 * 24 * time.Hour`.
  - `models.IdentityClaims{Issuer, Subject, Email, Name string}`.
  - `models.ErrInviteInvalid`.
  - `models.InviteEmailMismatch{Want string}`, which implements `error`.
  - `InviteStore`, from `NewInviteStore(db)`:
    - `Create(siteID int64, email, role string, canPublish bool, createdBy int64, now time.Time) (*Invite, string, error)`, which returns the token;
    - `Lookup(token string, siteID int64, now time.Time) (*Invite, error)`, which returns nil if the token is unknown, used, expired or for another site;
    - `Regenerate(id, siteID int64, now time.Time) (string, error)`. It sets a new token hash and `expires_at = now + InviteTTL`;
    - `Cancel(id, siteID int64) error`;
    - `CancelPendingFor(siteID int64, email string) error`;
    - `ListPending(siteID int64, now time.Time) ([]Invite, error)`;
    - `Accept(token string, siteID int64, c IdentityClaims, now time.Time) (*User, error)`.

- [ ] **Step 1: Write the failing tests**
  - `TestInviteEmailNormalised`: an invite created for `" Alice@Example.COM "` is stored as `alice@example.com`, and `Accept` with claims email `alice@example.com` succeeds.
  - `TestInviteReplacesPending`: creating a second invite for the same (site, email) makes the first token's `Lookup` return nil, and `ListPending` holds one.
  - `TestInviteExpiredDoesNotBlock`: with an invite created at `now`, `Create` at `now + 8 days` succeeds (the expired row is deleted first).
  - `TestInviteRejectsExistingMember`: a `ValidationError` mentioning "already a member".
  - `TestInviteAcceptCreatesUserMembershipIdentity`:
    - the user exists with the invite's email;
    - the membership has the invite's role and `can_publish`;
    - the identity is linked;
    - a second `Accept` with the same token gives `ErrInviteInvalid`.
  - `TestInviteAcceptEmailMismatch`: an invite for `a@x.io` with claims email `b@x.io` gives `*InviteEmailMismatch` with `Want == "a@x.io"`. The invite is still unused.
  - `TestInviteAcceptKnownIdentityDifferentEmail`: an identity already linked to a user whose email is `c@x.io` accepting an invite for `a@x.io`, with claims email `a@x.io`, gives `*InviteEmailMismatch{Want: "a@x.io"}`.
  - `TestInviteAcceptReassignedGuard`: the existing user `a@x.io` has (iss, sub1). Accepting an invite for `a@x.io` with (iss, sub2) gives `ErrIdentityConflict`, and the invite is still unused (the transaction rolled back).
  - `TestInviteAcceptConcurrent`: two goroutines `Accept` the same token. Exactly one gets a user; the other gets `ErrInviteInvalid`.
  - `TestInviteOtherSite`: `Lookup` and `Accept` of a site-A token with site B's ID give nil and `ErrInviteInvalid`.
  - `TestInviteAcceptExistingMembershipKept`: a contributor added directly after the invite was made accepts an invite with role `admin`. The membership stays contributor and the invite is marked used.

- [ ] **Step 2: Run; expect compile failures.** `go test ./internal/models -run Invite`
- [ ] **Step 3: Implement.** `Accept` runs in one `db.Transaction`, in this order:
  1. Load the invite by token hash, site, unused and unexpired; otherwise `ErrInviteInvalid`.
  2. Compare the claims email (normalised) to the invite's email; if they differ, `InviteEmailMismatch`.
  3. Mark it used with a conditional `UPDATE`; zero rows means `ErrInviteInvalid`.
  4. Find the user: the owner of (issuer, subject) if known, whose email must equal the invite's (otherwise `InviteEmailMismatch`); otherwise find or create the user by the invite's email.
  5. `linkIdentityTx`.
  6. Insert the membership if none exists.
- [ ] **Step 4: Run tests: PASS.** `go test ./internal/models`
- [ ] **Step 5: Commit** "Add invites with one-shot transactional acceptance"

---

### Task 4: Membership-checked sessions

**Files:**
- Modify: `internal/auth/auth.go`, `internal/auth/auth_test.go`, plus every `auth.NewManager` caller (`main.go`, `internal/handlers/handler_test.go`, `internal/server/server_test.go`)

**Interfaces:**
- Consumes: `MembershipStore.Get`.
- Produces:
  - `auth.Actor{User *models.User; Session *models.Session; Membership *models.Membership}`. `Membership` is nil for root.
  - `auth.NewManager(sessions *models.SessionStore, users *models.UserStore, memberships *models.MembershipStore, rootPasswordHash string, secure bool) *Manager`.
  - `Actor.CanAdminSite` is **removed**. Callers use `authz.SiteAdmin` (Task 5). Until Task 5, `RequireSiteAdmin` uses `a.IsPlatform() || (a.Membership != nil && a.Membership.Role == models.RoleAdmin)`.

- [ ] **Step 1: Write the failing tests** (`auth_test.go`, driving `Middleware` with a stub `current`)
  - `TestMemberSessionValidWithMembership`: the actor has `Membership.Role == "contributor"`.
  - `TestMemberSessionInvalidAfterRemoval`: `Memberships.Remove`, then the same cookie gives a nil actor.
  - `TestPlatformAdminNeedsMembership`: a platform admin with no membership on the session's site gives a nil actor.
  - `TestPlatformFlagReadEachRequest`: after `SetPlatformAdmin(false)`, the next request's actor has `IsPlatform() == false`.
  - `TestMemberSessionOtherSiteInvalid`: a session issued for site A, presented while `current` reports site B, gives nil.
  - `TestMemberSessionNilSiteInvalid`: a non-root session row with a nil `site_id` gives nil, even on the platform host.
  - `TestDeletedUserSessionInvalid`.

  The existing root tests must keep passing.
- [ ] **Step 2: Run; expect failures.** `go test ./internal/auth`
- [ ] **Step 3: Implement** the rule in `Manager.actor`:
  - **Root:** the digest must match; no membership.
  - **Anyone else:** `sess.SiteID` must be non-nil and must equal the current site; `Memberships.Get(user, *sess.SiteID)` must be non-nil and is attached to the actor.

  Update the callers to pass `models.NewMembershipStore(db)`.
- [ ] **Step 4: Run** `go test ./...`: PASS.
- [ ] **Step 5: Commit** "Require a current membership for member sessions"

---

### Task 5: authz and post authorship

**Files:**
- Create: `internal/authz/authz.go`, `internal/authz/authz_test.go`
- Modify: `internal/models/post.go`, `internal/models/post_test.go`, `internal/handlers/handler.go` (`RequireSiteAdmin` → `authz.SiteAdmin`)

**Interfaces:**
- Consumes: `auth.Actor`, `models.Membership`, `models.Post`.
- Produces:
  - `models.Post.AuthorID *int64`. `Create` stores it; `Update` never changes it.
  - `(*PostStore).ListByAuthor(authorID int64) ([]Post, error)`: all statuses, newest first, site-scoped.
  - `models.Post.LiveOnce() bool`, true when `FirstPublishedAt != nil`.
  - `authz` functions. Each takes a `*auth.Actor` (nil is anonymous, which is false everywhere) and refers to the site the actor's session is on. Throughout, "own" means `p.AuthorID != nil && *p.AuthorID == a.User.ID`.

    | Function | True when |
    |---|---|
    | `SiteAdmin(a) bool` | `a.IsPlatform()`, or membership role `admin` |
    | `SiteMember(a) bool` | `SiteAdmin`, or any membership |
    | `CanSeePost(a, p *models.Post) bool` | `SiteAdmin`, or own |
    | `CanCreatePost(a) bool` | `SiteMember` |
    | `CanPublishOwn(a) bool` | `SiteAdmin`, or contributor with `CanPublish` |
    | `CanEditPost(a, p) bool` | `SiteAdmin`, or own and (`CanPublishOwn` or not `LiveOnce`) |
    | `CanPublishPost(a, p) bool` (covers unpublish) | `SiteAdmin`, or own and `CanPublishOwn` |
    | `CanDeletePost(a, p) bool` | `SiteAdmin`, or own and (`CanPublishOwn` or not `LiveOnce`) |
    | `CanManageMembers(a) bool` | `SiteAdmin` |
    | `CanManageMember(a, target *models.User) bool` | `SiteAdmin`, target isn't root, and (target isn't a platform admin, or `a.IsPlatform()`) |
    | `CanUsePlatform(a) bool` | `a.IsPlatform()` |
    | `CanChangeUser(a, target *models.User) bool` | `a.IsPlatform()` and target isn't root |

- [ ] **Step 1: Write the failing tests**
  - `TestRoleTable`: table-driven.
    - Actors:
      - `anon`, `root` (no membership);
      - `platformContrib` (platform admin with a contributor membership, no publish);
      - `siteAdmin`;
      - `contribPublish`, `contribNoPublish`.
    - Posts:
      - `ownDraft`, `ownLive` (FirstPublishedAt set, currently unpublished);
      - `otherDraft`, `otherLive`;
      - `orphan` (AuthorID nil, live).
    - Each row is (function, actor, post or target) → expected bool. Together the rows cover every cell of the spec's role table, plus these:
      - `contribNoPublish`: CanEditPost(ownDraft)=true, CanEditPost(ownLive)=false, CanDeletePost(ownDraft)=true, CanDeletePost(ownLive)=false, CanPublishPost(ownDraft)=false, CanSeePost(otherDraft)=false.
      - `contribPublish`: CanPublishPost(ownLive)=true, CanEditPost(otherLive)=false, CanDeletePost(ownLive)=true.
      - `platformContrib`: SiteAdmin=true, CanEditPost(otherDraft)=true, CanUsePlatform=true.
      - `siteAdmin`: CanEditPost(orphan)=true, CanManageMember(platformAdminUser)=false, CanManageMember(contributorUser)=true, CanUsePlatform=false.
      - `root`: CanManageMember(platformAdminUser)=true, CanChangeUser(rootUser)=false.
      - `anon`: false for every function.
  - `TestCreateStoresAuthor` (post_test): `Create` with `AuthorID` set keeps it through `GetByID`. `Update` with a different `AuthorID` doesn't change it.
  - `TestListByAuthorScopedToSite`: the same author on sites A and B; `ForSite(A).ListByAuthor` returns only A's posts, drafts included.
- [ ] **Step 2: Run; expect compile failures.** `go test ./internal/authz ./internal/models`
- [ ] **Step 3: Implement.** `authz` is a set of pure functions over `*auth.Actor` and has no DB access. `RequireSiteAdmin` calls `authz.SiteAdmin`.
- [ ] **Step 4: Run** `go test ./...`: PASS.
- [ ] **Step 5: Commit** "Put the role table in authz and record post authors"

---

### Task 6: SSO client and the fake provider

**Files:**
- Create: `internal/sso/sso.go`, `internal/sso/flow.go`, `internal/sso/sso_test.go`, `internal/sso/ssotest/ssotest.go`
- Modify: `go.mod`, `go.sum` (`go get github.com/coreos/go-oidc/v3@v3.21.0 golang.org/x/oauth2@v0.37.0`)

**Interfaces:**
- Produces:
  - `sso.Issuer(providerType, domain string) string`.
  - `sso.Settings{Issuer, ClientID, ClientSecret, RedirectURL string}`.
  - `sso.Claims{Issuer, Subject, Email, Name string; EmailVerified bool}`. `Issuer` is `Settings.Issuer`; `Email` is lowercased.
  - `sso.ErrEmailNotVerified`, `sso.ErrFlowInvalid`.
  - `sso.ErrProvider`. It wraps discovery and exchange failures, so callers can `errors.Is` it.
  - `sso.Flow{State, Nonce, Verifier string; SiteID int64; Invite string; Expires time.Time}`.
  - `sso.NewFlow(siteID int64, invite string, now time.Time) Flow`. It generates the random values (32 bytes, base64url) and sets `Expires = now + 10 min`.
  - `(Flow).Encode(secret []byte) string`. The format is `base64url(json) + "." + base64url(hmac)`.
  - `sso.DecodeFlow(secret []byte, v string, now time.Time) (Flow, error)`. A bad signature, a malformed value or an expired flow gives `ErrFlowInvalid`.
  - `sso.Client`:
    - `sso.NewClient(hc *http.Client) *Client`, where a nil `hc` means `http.DefaultClient`;
    - `(*Client).AuthCodeURL(ctx context.Context, s Settings, f Flow) (string, error)`;
    - `(*Client).Exchange(ctx context.Context, s Settings, code string, f Flow) (Claims, error)`.

    Discovery results are cached per issuer, and failures are not cached.
  - `ssotest.New(t testing.TB) *Provider`, with fields `URL` (the issuer), `ClientID = "test-client"` and `ClientSecret = "test-secret"`.
  - `(*Provider).SetNext(id Identity)`, where `ssotest.Identity{Subject, Email, Name string; EmailVerified bool}`.
  - `(*Provider).Authorize(t testing.TB, authURL string) string`. It GETs `authURL` against the fake without following redirects, and returns the redirect `Location`: the callback URL with `code` and `state`.

- [ ] **Step 1: Write the failing tests** (`sso_test.go`, using `ssotest`)
  - `TestIssuer`: `google` maps to `https://accounts.google.com`, and (`auth0`, `acme.us.auth0.com`) to `https://acme.us.auth0.com/`.
  - `TestFlowRoundTrip`: encode then decode gives an equal flow.
  - `TestFlowTamperRejected`: one changed byte in the payload gives `ErrFlowInvalid`.
  - `TestFlowExpired`: decoding at `Expires + 1s` gives `ErrFlowInvalid`.
  - `TestAuthCodeURLHasPKCEAndScopes`: the URL's query has `code_challenge_method=S256`, a `code_challenge`, `scope=openid email profile`, `state == f.State` and `nonce == f.Nonce`.
  - `TestExchangeReturnsClaims`: via `Authorize` and then `Exchange`, the claims equal `SetNext`'s identity, with the email lowercased and `Issuer == fake.URL`.
  - `TestExchangeWrongNonce`: exchanging with a flow whose nonce differs gives an error.
  - `TestExchangeWrongVerifier`: the fake's token endpoint rejects a bad PKCE verifier, giving an error matching `ErrProvider`.
  - `TestExchangeUnverifiedEmail`: gives `ErrEmailNotVerified`.
  - `TestExchangeWrongAudience`: settings with a different client ID give an error.
  - `TestDiscoveryFailureNotCached`:
    1. `AuthCodeURL` against a closed server's URL gives an `ErrProvider`.
    2. Start a fake.
    3. `AuthCodeURL` with the new issuer succeeds. This checks that the failure was not cached under the first URL.
- [ ] **Step 2: Run; expect compile failures.** `go test ./internal/sso/...`
- [ ] **Step 3: Implement**
  - **`sso`:**
    - `oidc.NewProvider(oidc.ClientContext(ctx, hc), issuer)`.
    - `oauth2.Config{Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}`.
    - `AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))`.
    - `Exchange(ctx, code, oauth2.VerifierOption(verifier))`.
    - Verify with `p.Verifier(&oidc.Config{ClientID})`, then check that `idToken.Nonce` equals the flow's.
  - **`ssotest`:**
    - An `httptest.Server` with an RSA-2048 key generated per fake, serving:
      - `/.well-known/openid-configuration`;
      - `/jwks` (go-jose `JSONWebKeySet`);
      - `/authorize`, which records the code with the identity, nonce, client ID and challenge, then 302s to `redirect_uri?code=…&state=…`;
      - `/token`, which checks client credentials, the code, and that `S256(code_verifier)` equals the challenge, then returns `{"access_token","token_type":"Bearer","id_token"}`. The ID token is RS256 with iss = URL, aud = client ID, sub, email, email_verified, name, nonce, iat and exp (+5 min).
- [ ] **Step 4: Run tests: PASS.** `go test ./internal/sso/...`
- [ ] **Step 5: Commit** "Add the OIDC client, signed flow state and a fake provider"

---

### Task 7: Sign-in handlers

**Files:**
- Create: `internal/people/people.go`, `internal/people/people_test.go`, `internal/handlers/signin.go`, `internal/handlers/signin_test.go`
- Create: `internal/adminui/templates/{message,signed_in,invite}.html`
- Modify:
  - `internal/adminui/adminui.go` (`RenderStatus`, page names);
  - `internal/adminui/templates/layout.html` (a `{{block "head" .}}{{end}}` inside `<head>`);
  - `internal/adminui/templates/login.html` (the provider button);
  - `internal/handlers/handler.go` (Deps);
  - `internal/handlers/routes.go`;
  - `internal/handlers/handler_test.go` (env);
  - `internal/server/server.go`, `internal/server/server_test.go` (log redaction).

**Interfaces:**
- Consumes: Tasks 1–6.
- Produces:
  - `people`:
    - `people.ErrNoAccess`;
    - `people.New(users *models.UserStore, ids *models.IdentityStore, mems *models.MembershipStore, invites *models.InviteStore) *Service`;
    - `(*Service).SignIn(siteID int64, c models.IdentityClaims, invite string, now time.Time) (*models.User, error)`. It may return:
      - `ErrNoAccess`;
      - `*models.InviteEmailMismatch`;
      - `models.ErrInviteInvalid`.

      `ErrIdentityConflict` is mapped to `ErrNoAccess`;
    - `(*Service).AddMember(siteID int64, email, role string, canPublish bool) (*models.User, error)`. It does find-or-create, then `Memberships.Add`, then `Invites.CancelPendingFor`.
  - `handlers.Deps` gains:
    - stores: `Memberships *models.MembershipStore`, `Identities *models.IdentityStore`, `Invites *models.InviteStore`, `Sessions *models.SessionStore`;
    - services: `People *people.Service`, `SSO *sso.Client`;
    - `IssuerFor func(providerType, domain string) string`, where nil means `sso.Issuer`;
    - keys: `SessionSecret []byte`, `EncryptionKey []byte`.
  - `adminui.RenderStatus(w http.ResponseWriter, status int, page string, data map[string]any)`. `Render` calls it with 200. The `message` page takes `Title`, `Message`, and optional `LinkHref` and `LinkText`.
  - Routes:
    - `GET /auth/start` and `POST /auth/start` (form or query `invite`);
    - `GET /auth/callback`;
    - `GET /invite/{token}`.

    All of them run through `h.serve`, so they 404 on the platform host.
  - Handler-test helpers in `handler_test.go`:
    - `env.fake *ssotest.Provider`. `newEnv` configures site A as auth0 with the fake's client and a sealed secret, and sets `IssuerFor` to return `fake.URL`;
    - `(e *env) ssoLogin(host string, id ssotest.Identity, invite string) (*httptest.ResponseRecorder, *http.Cookie)` runs the start, authorize and callback steps and returns the callback response and the session cookie, if any;
    - `(e *env) member(email, role string, canPublish bool) *models.User`;
    - `(e *env) sessionFor(u *models.User) *http.Cookie` mints a site-A session via `Auth.Login`.

- [ ] **Step 1: Write the failing tests**
  - `people_test.go`:
    - `TestSignInKnownIdentity`.
    - `TestSignInEmailMatchLinks`: an added member signs in for the first time and the identity gets linked.
    - `TestSignInEmailMatchNeedsMembershipHere`: the user is a member on B only, signing in on A gives `ErrNoAccess`, and no identity is linked.
    - `TestSignInUnknownGives403Error`: `ErrNoAccess`.
    - `TestSignInReassignedIsNoAccess`.
    - `TestSignInKnownIdentityWithoutMembership`: `ErrNoAccess`.
    - `TestSignInRefreshesName`.
    - `TestSignInNeverRoot`: claims with an empty email give `ErrNoAccess` and never match root.
  - `signin_test.go`:
    - `TestSSOSignInFlow`: an added contributor gets a callback 200 whose body contains `http-equiv="refresh"` and `/admin/posts`. The cookie works on `/admin/posts` (200), and the `ntx_oauth` cookie is cleared (MaxAge < 0).
    - `TestSSOUnknownUser403`: the status is 403, the body contains "You don't have access to this site.", and there's no session cookie.
    - `TestSSOUnverifiedEmail403`.
    - `TestCallbackOtherSite`: a flow started on A whose callback is sent to B's host (with the A flow cookie) gives 400 and no session.
    - `TestCallbackStateMismatch`: 400.
    - `TestCallbackWithoutFlowCookie`: 400, body "Your sign-in expired. Try again." with a link to `/admin/login`.
    - `TestCallbackExpiredFlow`: a flow encoded with `Expires` in the past gives 400.
    - `TestCallbackProviderError`: `?error=access_denied&state=…` gives 400, "Sign-in was cancelled.", and no session.
    - `TestStartProviderUnreachable`: `IssuerFor` returns a closed URL; the status is 502, and the body says the sign-in provider couldn't be reached.
    - `TestStartUndecryptableSecret`: the stored secret is sealed with another key; 502, and the body mentions re-entering the client secret.
    - `TestStartNoProvider404`: site A with provider `""` gives 404 on `/auth/start`.
    - `TestInviteFlow`:
      1. Create an invite via `Invites.Create`.
      2. `GET /invite/<token>` gives 200 naming the site, the role and "Auth0", with a form posting to `/auth/start` and a hidden `invite`.
      3. `ssoLogin` with that invite gives a membership with the invite's role.
    - `TestInviteWrongAccount`: callback 403, and the body contains the invite's email.
    - `TestInvitePageInvalidToken`: 404 page "This invite link is no longer valid."
    - `TestLoginPageShowsProviderButton`: on site A the body contains `href="/auth/start"` and "Sign in with Auth0"; on the platform host it doesn't.
  - `server_test.go`: `TestRequestLogRedactsTokens`. A request to `/invite/SECRET123` and one to `/auth/callback?code=CODE9&state=S` produce a log without `SECRET123` or `CODE9`, but with `/invite/[redacted]` and `/auth/callback`.
- [ ] **Step 2: Run; expect compile failures.** `go test ./internal/people ./internal/handlers ./internal/server`
- [ ] **Step 3: Implement**
  - **Settings for the resolved site.** `settings(r, site) (sso.Settings, error)` builds the issuer from `IssuerFor`, decrypts the secret, and works out the callback URL as in the Global Constraints. A decrypt failure gives a 502 message page, logged.
  - **`/auth/start`:**
    1. 404 if the site has no provider.
    2. `NewFlow`.
    3. Set the `ntx_oauth` cookie (Global Constraints).
    4. 302 to `AuthCodeURL`.
    - `ErrProvider` gives a 502 message page, "Couldn't reach the sign-in provider. Try again later."
  - **`/auth/callback`**, in this order:
    1. Clear the cookie.
    2. Decode the flow; on `ErrFlowInvalid`, 400 "expired".
    3. If `error` is in the query, 400 "cancelled".
    4. If the site ID or state doesn't match, 400 "expired".
    5. `Exchange`: `ErrEmailNotVerified` gives the 403 no-access page; `ErrProvider` gives 502; any other verify error gives 400 "expired".
    6. `People.SignIn`: `InviteEmailMismatch` gives 403 "This invite is for <email>. Sign in with that account to accept it."; `ErrInviteInvalid` gives 404 invite-invalid; `ErrNoAccess` gives 403.
    7. `Auth.Login(w, user, &site.ID)`.
    8. Render `signed_in`.
  - **`/invite/{token}`:** `Invites.Lookup`, then render `invite`. If the site has no provider, show the message "This site has no sign-in provider yet."
  - **`server.requestLog`:** `logPath(r)` replaces anything after `/invite/` with `[redacted]` and drops the query for `/auth/callback`.
- [ ] **Step 4: Run** `go test ./...`: PASS.
- [ ] **Step 5: Commit** "Sign people in through their site's Google or Auth0 provider"

---

### Task 8: Posts under the role table

**Files:**
- Modify: `internal/handlers/admin.go`, `internal/handlers/routes.go`, `internal/handlers/handler.go` (`RequireMember`), `internal/adminui/templates/{posts,editor,layout}.html`
- Create: `internal/handlers/posts_test.go`

**Interfaces:**
- Consumes: `authz.*`, `PostStore.ListByAuthor`, `UserStore.GetByIDs`, and the env helpers from Task 7.
- Produces:
  - `(*Handler).RequireMember`. Off a site it's a 404; anonymous requests redirect to `/admin/login`; `!authz.SiteMember` gives 403.
  - The `/admin/posts…` routes move to `RequireMember`. `/admin/settings` stays on `RequireSiteAdmin`.
  - Template data:
    - the post list rows carry `Status` ("draft", "published" or "unpublished") and `Author` (the name, else the email, else "former member");
    - the editor gets `CanPublish`, `CanDelete` and `ReadOnly` booleans;
    - the layout gets `IsSiteAdmin` and `IsPlatform`, from `h.admin`, which drive the nav (settings and members only for site admins).

- [ ] **Step 1: Write the failing tests** (`posts_test.go`)
  - `TestContributorSeesOnlyOwnPosts`: the list shows the contributor's own draft but not another member's draft. `GET` of the other post's edit page gives 404.
  - `TestContributorNoPublishCannotPublish`: creating with `action=publish` gives 403 and the post isn't created. Creating with `action=save` gives a draft with `AuthorID` set to the contributor.
  - `TestContributorNoPublishLiveOnceReadOnly`:
    - an admin publishes a post authored by the contributor;
    - the contributor's edit page contains `readonly` and no publish, unpublish or delete buttons;
    - a POST with `action=save` and a new title gives 403;
    - `GET /blog/<slug>` still shows the old title.
  - `TestContributorNoPublishTagsStayPrivate`: a contributor saves a draft tagged `secret-tag`; the public `/blog` body doesn't contain `secret-tag`.
  - `TestContributorNoPublishDeletesOwnDraft`: a 303, and the post is gone.
  - `TestContributorWithPublish`: publishing their own post goes live, and so does editing it while live.
  - `TestContributorCannotOpenSettings`: 403 on `/admin/settings`.
  - `TestSiteAdminEditsOthersPosts`: a site admin edits and publishes a contributor's draft.
  - `TestPostListStatuses`: the statuses are draft, published and unpublished. An orphaned post (author deleted) shows "former member".
- [ ] **Step 2: Run; expect failures.** `go test ./internal/handlers -run 'Contributor|SiteAdminEdits|PostListStatuses'`
- [ ] **Step 3: Implement.**
  - **`Posts`:** `ListByAuthor` for non-admins, otherwise `List(false)`.
  - **Edit:** `postFromParam` gives 404 unless `CanSeePost`.
  - **`CreatePost`:** sets `AuthorID = actor.User.ID`. Publishing on create needs `CanPublishOwn`.
  - **`UpdatePost` gates:**
    - `delete` → `CanDeletePost`;
    - `publish` and `unpublish` → `CanPublishPost`;
    - `save` → `CanEditPost`;
    - `save` that changes the published state isn't possible.

    A refused action gets a 403 `message` page.
  - **Root:** root posts with `AuthorID` set to root's ID (root is a user row).
- [ ] **Step 4: Run** `go test ./...`: PASS.
- [ ] **Step 5: Commit** "Gate posts by the role table and show authors and statuses"

---

### Task 9: Members page

**Files:**
- Create: `internal/handlers/members.go`, `internal/handlers/members_test.go`, `internal/adminui/templates/members.html`
- Modify: `internal/handlers/routes.go`, `internal/adminui/adminui.go`

**Interfaces:**
- Consumes: `People.AddMember`, `MembershipStore`, `InviteStore`, `authz.CanManageMember`.
- Produces routes, all behind `RequireSiteAdmin`:

  | Route | Fields |
  |---|---|
  | `GET /admin/members` | |
  | `POST /admin/members` | add: `email`, `role`, `can_publish` |
  | `POST /admin/members/invite` | `email`, `role`, `can_publish` |
  | `POST /admin/members/{userID}` | `role`, `can_publish` |
  | `POST /admin/members/{userID}/remove` | |
  | `POST /admin/invites/{id}/cancel` | |
  | `POST /admin/invites/{id}/regenerate` | |

  - `can_publish` is the checkbox value `"on"`.
  - Invite and regenerate re-render the members page with `InviteLink` set, shown once.
  - The invite URL comes from `inviteURL(r, site, token) string` (Global Constraints).

- [ ] **Step 1: Write the failing tests**
  - `TestMembersAddPerson`: adding `New@X.io` as a contributor gives a 303. The person is listed as `new@x.io`, and a fresh `ssoLogin` with that email gets 200 (step 3.3 linking).
  - `TestMembersAddDuplicate`: the page shows the error "already a member", with status 200 and no 500.
  - `TestMembersInviteShowsLinkOnce`:
    - the POST response body contains `http://a.example.com/invite/` followed by a token;
    - a later `GET /admin/members` lists the pending invite but not the token;
    - regenerating gives a new link, and the old token's `/invite/` page gives 404.
  - `TestMembersCancelInvite`.
  - `TestMembersChangeRoleTakesEffect`: demoting an admin to contributor makes that admin's next `GET /admin/settings` a 403.
  - `TestSiteAdminCannotTouchPlatformAdmin`:
    - a platform admin holds a contributor membership on A;
    - a site admin's POST to `/admin/members/{id}` and to `.../remove` gives 403, and the membership is unchanged;
    - the page shows no controls for that row;
    - root can change it.
  - `TestMembersOtherSiteIDs`: an invite ID from site B passed to A's cancel route gives 404, and B's invite is untouched.
  - `TestContributorCannotOpenMembers`: a contributor gets 403 on `GET /admin/members` and on `POST /admin/members`.
- [ ] **Step 2: Run; expect failures.** `go test ./internal/handlers -run Members`
- [ ] **Step 3: Implement.**
  - Every change looks up the target user and calls `authz.CanManageMember` before writing; a refusal is a 403 `message` page.
  - Store `ValidationError`s re-render the page with `Error`.
  - Each member row in the template carries `Manageable bool`.
- [ ] **Step 4: Run** `go test ./...`: PASS.
- [ ] **Step 5: Commit** "Add the members page: add, invite, change and remove people"

---

### Task 10: Platform: provider settings and users

**Files:**
- Modify: `internal/handlers/platform.go`, `internal/adminui/templates/{platform_site_form,platform_sites,layout}.html`, `internal/handlers/routes.go`
- Create: `internal/handlers/users.go`, `internal/handlers/users_test.go`, `internal/adminui/templates/{platform_users,platform_user}.html`

**Interfaces:**
- Consumes: `SiteStore.SetProvider`, `secretbox.Seal`, user, membership, identity and session stores, `authz.CanChangeUser`.
- Produces:
  - **Site form:**
    - new fields `provider_type`, `provider_domain`, `provider_client_id` and `provider_client_secret` (a password input, never pre-filled), with "set" or "not set" shown;
    - the callback URL `https://<hostname>/auth/callback` shown read-only on the edit page;
    - on update, `Sites.Update`, then `SetProvider`, then `Registry.Reload`;
    - a non-empty secret is sealed with `EncryptionKey`.
  - **Sites list:** adds provider and member-count columns.
  - **User routes**, all behind `RequirePlatform`:

    | Route | Fields |
    |---|---|
    | `GET /admin/platform/users` | |
    | `GET /admin/platform/users/{id}` | |
    | `POST /admin/platform/users/{id}` | `email`, `platform_admin` checkbox |
    | `POST /admin/platform/users/{id}/memberships` | `site` (key), `role`, `can_publish` |
    | `POST /admin/platform/users/{id}/memberships/{siteKey}/remove` | |
    | `POST /admin/platform/users/{id}/identities/{identityID}/unlink` | |
    | `POST /admin/platform/users/{id}/revoke` | deletes all sessions |
    | `POST /admin/platform/users/{id}/delete` | |

  - Root's page shows no forms. Every POST on root gives 403, via `authz.CanChangeUser` (the stores also refuse).
  - Nav: "users" appears next to "sites" for platform actors.

- [ ] **Step 1: Write the failing tests** (`users_test.go`, plus additions to `handler_test.go`)
  - `TestPlatformProviderSaveAndClear`:
    1. Saving auth0, domain, client ID and secret `s3cret` makes the edit page show "set" and not contain `s3cret`.
    2. Changing the domain with the secret left empty shows the form error "enter the client secret again", and the stored domain is unchanged.
  - `TestPlatformSiteFormShowsCallbackURL`.
  - `TestPlatformUsersList`: lists root, and members with their sites and roles.
  - `TestGrantPlatformAdmin`: root grants a site-A contributor platform admin. That person's existing session (`sessionFor`) can now `GET /admin/platform/sites` (200). Revoking it makes the same request 403.
  - `TestPlatformAdminCanRevokeSelf`.
  - `TestRootUserHasNoControls`: the root user page has no `<form` with a `/admin/platform/users/` action. Each POST route on root's ID gives 403, and root is unchanged.
  - `TestUnlinkIdentity`: after unlinking, the next `ssoLogin` as the same subject still works by email match, and the identity is re-linked.
  - `TestRevokeSessions`: the user's existing cookie gets a 302 to login on `/admin/posts`.
  - `TestDeleteUserKeepsTheirPosts`: the public post is still served, and the admin list shows "former member".
  - `TestPlatformAddMembershipOnOtherSite`: adding u to site B as admin lets u's `ssoLogin` on B succeed.
- [ ] **Step 2: Run; expect failures.** `go test ./internal/handlers -run 'Platform|Grant|RootUser|Unlink|Revoke|DeleteUser'`
- [ ] **Step 3: Implement** the routes and pages as listed. Email edits go through `SetEmail` and show any `ValidationError` on the form.
- [ ] **Step 4: Run** `go test ./...`: PASS.
- [ ] **Step 5: Commit** "Add provider settings and the platform users views"

---

### Task 11: Wiring, isolation tests and docs

**Files:**
- Modify: `main.go`, `internal/server/server_test.go`, `docs/usage.md`, `docs/ai-context.md`, `README.md`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `openApp` builds the new stores, `people.New`, `sso.NewClient(nil)`, and `Deps` with `SessionSecret: []byte(cfg.SessionSecret)` and `EncryptionKey: cfg.EncryptionKey`.

- [ ] **Step 1: Write the failing tests** (`server_test.go`, which uses the full `server.New` stack and now needs the fake provider; extend `setup`)
  - `TestMemberSessionNotValidOnOtherSite`: a session from an `ssoLogin` on A, sent to B's `/admin/posts`, gives a 302 to login.
  - `TestInviteTokenOnlyOnItsSite`: an A invite token gets 404 at `b.example.com/invite/<token>`.
  - `TestSSOCallbackPassesCrossOriginProtection`: the callback is a GET from the provider with `Sec-Fetch-Site: cross-site` and still succeeds. The invite form's POST to `/auth/start` with `Sec-Fetch-Site: same-origin` succeeds, and with `cross-site` it's 403.
  - `TestSSOCallbackSetsSessionBehindProxy`: with `X-Forwarded-Proto: https` from a trusted proxy and `dev = false`, the callback URL sent to the provider is `https://a.example.com/auth/callback`, and the session cookie is `Secure`.
- [ ] **Step 2: Run; expect failures.** `go test ./internal/server`
- [ ] **Step 3: Implement** the wiring. Then update the docs:
  - **`usage.md`:** roles, setting a site's provider (Google/Auth0 console steps: the callback URL, scopes), members, invites, platform users, the encryption-key rotation note ("re-enter provider secrets after changing it"), and the log redaction.
  - **`ai-context.md`:** the new packages, the `authz` rule, the membership-checked session rule, and the sign-in flow.
  - **`README.md`:** a one-paragraph feature summary.
- [ ] **Step 4: Run** `go vet ./... && go test ./...`: PASS. Run `go build ./...`.
- [ ] **Step 5: Commit** "Wire people and SSO into serve and document them"
