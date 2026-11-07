# Roles, SSO and web-managed sites: design

Date: 2026-11-07
Status: reviewed (hostile review incorporated), awaiting owner approval

## Intent

ntx serves several sites from one process, but everything about a site lives in `ntx.toml` and every site has a single shared admin password. This design turns ntx into a small multi-site platform:

- A **root** account, whose password is set in config, creates and configures sites in the browser.
- Each site has its own sign-in provider (**Auth0 or Google**) and its own named people: **site admins** and **contributors**.
- **Platform admins** have root's powers but sign in with SSO.
- Contributors may or may not be trusted to publish (`can_publish`). Without it, nothing they write or change goes live until a site admin publishes it.

There are no production databases yet. The schema is rewritten and there is **no upgrade path**: no migrations from the current schema, and no import of `[[site]]` blocks.

## Constraints and assumptions

- **Single instance behind a TLS-terminating reverse proxy** (nginx, Caddy, etc.). Several replicas are not supported. In-memory state (the site map, the login rate limiter) is acceptable for that reason, and the docs say so.
- **The proxy is trusted for client IP and scheme** only when the connection comes from an address in the new `trusted_proxies` setting. See [Client IP and scheme](#client-ip-and-scheme).
- **Providers are platform-controlled.** Only root and platform admins configure a site's Auth0 tenant or Google OAuth client, and the platform operates those tenants. ntx therefore trusts a provider's `email_verified` claim. That trust is what makes email-based matching across sites acceptable; see [Identity linking](#identity-linking). Letting site owners bring their own tenant would break this assumption and is a non-goal.

## Goals

1. Root signs in with username `root` and a password whose bcrypt hash is in config. It works on a fresh install with zero sites, and keeps working even if every SSO provider is broken.
2. Root and platform admins create, edit and delete sites in the browser. A site's hostname, name, description, about slug, theme, sign-in provider and content sections are all stored in the database.
3. Each site has exactly one sign-in provider: Google or Auth0. Everyone except root signs in through a site's provider.
4. Access is by invitation only. A valid Google or Auth0 login grants nothing unless ntx has a membership for that person on that site.
5. Four roles: root, platform admin (both platform-wide), site admin and contributor (both per site), plus a per-membership `can_publish` capability on contributors.
6. Contributors without `can_publish` cannot make anything visible to readers, including edits to posts that are already live, and tags.

## Non-goals

- Sending email. Invites are links that an admin copies and sends themselves.
- Providers other than Google and Auth0: no generic OIDC configuration, SAML or GitHub. (The implementation uses OIDC for both, but the UI and validation offer only the two.)
- Site owners bringing and administering their own Auth0 tenant (see the constraint above).
- Password logins for anyone but root. No self sign-up.
- Custom roles or fine-grained permissions beyond the four roles and `can_publish`.
- MFA, account recovery and similar. Those are the provider's job; root's recovery is editing the config file.
- A review workflow beyond "drafts and pending changes wait for a site admin": no comments and no "ready for review" flag (left for later).
- Data migration from the current schema or from `[[site]]` config blocks.
- Running more than one ntx process against the same database.
- An audit log (worth adding later; out of scope here).

## Anti-goals (failures even if everything "works")

- Root can be locked out, deleted, demoted or have its password changed from the browser, or by an unauthenticated attacker's failed guesses.
- A provider login on its own grants access to anything.
- A contributor without `can_publish` can cause any text to be visible to readers.
- A browser form can make ntx read or write a path outside its themes folder or the `generate` output directory.
- Removing someone's role, membership or sessions doesn't take effect immediately.
- One site's provider, people, content or sessions leak into another site. This is the existing tenant-isolation bar, and it stays.

## Roles

| | root | platform admin | site admin | contributor |
|---|---|---|---|---|
| Scope | platform | platform | one site | one site |
| Signs in with | password (config) | SSO, on a site where they are a member | SSO | SSO |
| Platform views (all sites, all users) | yes | yes | no | no |
| Create, edit and delete sites; set theme, hostname and provider | yes | yes | no | no |
| Grant or revoke platform admin | yes | yes | no | no |
| Manage members and invites of a site | any site | any site | own site (not platform admins) | no |
| Edit site name, description, about slug and sections | any site | any site | own site | no |
| Write posts | on any site | on the site they're signed in to | own site | own site |
| Edit or publish others' posts, publish pending changes | on any site | on the site they're signed in to | own site | no |
| Publish, unpublish and delete own posts | yes | yes | yes | only with `can_publish` |

Rules:

- **root** is a `users` row with `is_root = true`, created at boot if missing. A partial unique index allows only one such row.
  - It never has an email, identities or memberships. The service layer enforces this on every write that touches users, identities or memberships, so root can't become reachable through SSO or email matching.
  - Its password is checked against `root_password_hash` in config; the hash is never stored in the database.
  - No web action can delete root, change its password or `is_root`, or remove its platform powers. This is enforced in the service layer, not only hidden in the UI.
- **Platform admin** is `users.is_platform_admin = true`.
  - A platform admin signs in through the provider of a site where they currently hold a membership (their "home" site or sites), and can use the platform views from there.
  - On the site they're signed in to, they have site-admin powers on content and members, whatever their membership role there.
  - A platform admin can grant themselves membership on any site, so in practice they have root's reach.
  - Platform admins can grant and revoke `is_platform_admin` for anyone except root, including themselves.
  - Site admins cannot change or remove a platform admin's membership; only root and platform admins can, from the platform views.
- **Root** likewise has site-admin powers on whichever site host it's signed in to.
- **Site admin** and **contributor** are the two membership roles, one membership per (user, site). `can_publish` is a boolean on the membership. It is meaningful only for contributors (site admins can always publish) and is ignored for site admins.
- A user may hold memberships on any number of sites, with a different role on each.

### Contributor rules in detail

A post is **live-once** if it has ever been published (`first_published_at` is set), whether or not it is published now.

- Contributors see and edit only posts they authored. They can see other people's published posts on the public site as usual, but not other people's drafts.
- **Without `can_publish`:**
  - They can create drafts and edit their own never-published drafts directly. They can delete their own never-published drafts.
  - Editing their own **live-once** post never changes the post. The edit is saved as that post's pending revision (see the data model). Saving again overwrites the pending revision. A pending revision cannot change the slug: the slug field is read-only for them on live-once posts.
  - They cannot publish, unpublish, or delete a live-once post.
- **With `can_publish`:** they can publish, unpublish and delete their own posts, and their edits to their own posts take effect directly.
- Site admins see every post on the site, with these statuses: draft, published, unpublished (live-once but not currently published), and "pending changes" on either of the last two. They can edit any post directly, publish any draft, and publish or discard any pending revision.

### What readers see

Public pages show only published posts, and only tags attached to at least one published post (`AllTags` changes accordingly; admin views still list every tag). Neither drafts nor pending revisions affect any public page, `ntx generate` output, or the public tag list.

## Sign-in

### Client IP and scheme

New config: `trusted_proxies`, a list of CIDRs (`gen-config` defaults it to `127.0.0.1/32` and `::1/128`, matching the default `bind_addr = "localhost"`). When the TCP peer is in that list, ntx takes:

- the client IP from the right-most `X-Forwarded-For` entry that isn't itself a trusted proxy;
- the scheme from `X-Forwarded-Proto`.

Otherwise it uses the peer address and the connection's own scheme. Rate limiting, logs and callback URLs all use this one function.

### Root

`/admin/login` shows a username-and-password form (alongside the site's SSO button, if the site has a provider). Only username `root` is accepted. Root can sign in on any site hostname and on the platform hostname.

Rate limiting:
- **Only failed attempts count.** (Today's limiter records every attempt before checking the password, `internal/handlers/admin.go`; that changes.)
- The budget is 5 failures per 15 minutes per client IP, from the function above, shared across all hosts.
- A successful login doesn't consume budget. Because the IP is the real client's, one attacker exhausting their budget doesn't lock out anyone else.

**Root sessions end when the password changes.** Each root session row stores `sha256(root_password_hash)` at creation. A root session whose stored value doesn't match the current config is invalid. So editing `root_password_hash` and restarting ends every existing root session.

### Platform hostname (bootstrap)

A fresh install has no sites, and the router 404s unknown hosts, so root needs somewhere to sign in. Config gains `platform_hostname` (required, bare hostname; `gen-config` default `localhost`). On that host, ntx serves only:
- `/admin/login` (root form only)
- `/admin/logout`
- `/admin/platform/...`
- the admin assets

No site may use `platform_hostname` as its hostname; site create and edit reject it.

### SSO (Google and Auth0)

Both providers are used as OpenID Connect providers via `github.com/coreos/go-oidc/v3` and `golang.org/x/oauth2`, using the authorization code flow with `state`, `nonce` and PKCE.

| Provider | Settings stored per site | Configured issuer |
|---|---|---|
| Google | client ID, client secret | `https://accounts.google.com` |
| Auth0 | Auth0 domain (e.g. `acme.us.auth0.com`), client ID, client secret | `https://<domain>/` |

Scopes are `openid email profile`. The callback URL is `https://<site hostname>/auth/callback`, and the provider settings page shows it for copying into Google or Auth0. In dev mode, the URL uses the request's scheme and `Host`, port included.

Flow:

1. **`GET /auth/start`** (optionally `?invite=<token>`):
   - Generates `state`, `nonce` and a PKCE verifier.
   - Stores them in a short-lived (10 min) signed cookie `ntx_oauth` (`SameSite=Lax`, HttpOnly, `Secure` unless dev, signed with `session_secret`), together with the **resolved site's ID** and the invite token, if any.
   - Redirects to the provider.
2. **`GET /auth/callback`**:
   - Requires the cookie, and requires its site ID to match the resolved site, so a flow started on one site can't finish on another.
   - Checks `state` and clears the cookie.
   - Exchanges the code and verifies the ID token (issuer, audience, expiry, nonce). go-oidc accepts Google's scheme-less `accounts.google.com` issuer.
   - Requires `email_verified = true`.
3. **Identity resolution**, in this order:
   1. **Invite present:** if the cookie carries an invite token, accept the invite ([Invites](#invites-and-adding-people)), whether or not the identity is already known. If the identity is already linked to a user, the invite's email must match that user's email; otherwise the invite finds or creates the user by email and links the identity.
   2. **Known identity:** look up `identities` by (configured issuer, `subject`). If found, that's the user. Identities store the **configured** issuer, never the token's `iss`, so Google's two issuer spellings can't create duplicate rows.
   3. **Email match:** otherwise, find a user whose email equals the verified email (lowercased) and who has a membership on **this** site, then link the identity. This is the "added by an admin" path; see [Identity linking](#identity-linking) for the guard.
   4. **No match:** otherwise, show "You don't have access to this site" with HTTP 403, without revealing whether the email is known anywhere else.
4. **Membership check:** the user must hold a membership on this site (platform admins included: this is the "attached to a site" rule). If not, the same 403.
5. **Session:** create a session for (user, this site) and finish with an interstitial page that moves to `/admin/posts` with `<meta http-equiv="refresh">` and a plain link. Inline script is blocked by the CSP, so no JS. The session cookie is `SameSite=Strict`, and this request chain started at the provider, so a plain 302 would arrive without the new cookie; the interstitial makes the final navigation same-site.

The request logger redacts the path after `/invite/` and the query string of `/auth/callback`, so tokens and codes never reach logs.

### Identity linking

An identity, once linked, is the person's login from then on.

- **One user across sites.** Because providers are platform-controlled, a person added or invited by email on several sites is one user. They may end up with several identities: one per Auth0 tenant, plus Google.
- **The reassigned-account guard.** An email match (step 3.3) or an invite (step 3.1) never links a new identity to a user who already has an identity with the **same issuer** and a different subject. That situation is a reused or reassigned mailbox, such as a Google Workspace address given to a new employee, and is refused with the 403 page. A platform admin resolves it by unlinking the old identity on purpose.
- **`users.email` is not updated from providers.** It is the address the person was added or invited with, stored lowercase. Only root and platform admins can change it, from the platform views, and never for root. This keeps email matching deterministic when a user has several providers.
- **Unlinking.** Platform admins can unlink an identity and revoke all of a user's sessions from the user's page, without deleting the user. That is the response to a compromised account. It is also how a site moves to a new provider: the next sign-in matches by email again.

### Sessions

Sessions move from stateless signed cookies to the database:

- **Cookie and row.** The cookie `ntx_session` holds a random 32-byte token (HttpOnly, `SameSite=Strict`, `Secure` unless dev, no `Domain` attribute, so it's bound to the host). The `sessions` table stores:
  - `sha256(token)`, `user_id`, `site_id` (NULL only for a root session on the platform hostname);
  - `created_at`, `expires_at` (24 h) and `last_seen_at`. `last_seen_at` is written at most once a minute per session, because SQLite runs on a single connection.
  - for root, `root_hash_digest` (see [Root](#root)).
- **Permissions are re-derived every request.** An authenticated request loads the session, the user and the user's **current** membership on the session's site, and works out permissions from those rows.
  - A non-root session with no current membership on its site is invalid, platform admins included.
  - So removing a membership, clearing `is_platform_admin`, unlinking or revoking, or deleting a user takes effect on the next request.
- **Host-bound.** A session is valid only on the host it was issued for: `session.site_id` must equal the resolved site, or be NULL on the platform hostname.
- **Logout and cleanup.** Logout deletes the row. Changing a site's hostname deletes that site's sessions. The builder decides how expired rows are cleaned up.
- **What it replaces.** This replaces `auth.Session`, `SetSiteSession` and `SetGlobalSession` from the multi-tenant work.

### CSRF

Every state-changing route is wrapped in Go's `http.CrossOriginProtection`, which checks `Sec-Fetch-Site` and `Origin`: every POST under `/admin/` (including `/admin/login`), and the invite-accept POST.

`SameSite=Strict` on its own is not enough. "Same site" covers sibling subdomains, so with tenants on `a.example.com` and `b.example.com`, a page on one could otherwise forge requests to the other.

### Secrets

- **Encryption.** Provider client secrets are encrypted with AES-256-GCM using `encryption_key` from config (32 random bytes, base64; `gen-config` generates it), with a random nonce per value. `encryption_key` is separate from `session_secret`, so rotating one doesn't break the other.
- **Never sent to the browser.** The settings form shows "set" or "not set". An empty field on save means "keep the current secret" only if `provider_type`, `provider_domain` and `provider_client_id` are all unchanged. Changing any of them clears the stored secret and requires it to be entered again. Otherwise a changed domain would send the existing secret to whatever server the new domain names.
- **Rotation.** Rotating `encryption_key` is out of scope; the docs say "re-enter provider secrets after changing it".

## Invites and adding people

Both paths are open to site admins (for their site, and not for platform admins), and to platform admins and root (for any site). Each sets the membership terms: an email (stored lowercase), a role, and `can_publish` for contributors.

- **Add:** creates (or finds, by email) a user and a membership right away. The person can then sign in on the site's login page, and step 3.3 links their identity on first sign-in.
- **Invite:** creates an `invites` row instead of a membership: site, email, role, `can_publish`, `created_by`, `expires_at` (7 days), `used_at`, and `sha256(token)`.
  - ntx shows the link `https://<site hostname>/invite/<token>` once, for the admin to copy.
  - The link opens a page saying "You've been invited to `<site>` as `<role>`. Sign in with `<provider>` to accept", whose button POSTs to start `/auth/start?invite=<token>`.
  - On callback, the invite must be unexpired and unused, and the provider's verified email must match the invite's email.
  - Acceptance is one conditional update (`UPDATE invites SET used_at = … WHERE id = … AND used_at IS NULL`), so two concurrent callbacks can't both succeed. It then finds or creates the user, creates the membership and links the identity, subject to the reassigned-account guard.
- **Wrong account.** An invite whose email doesn't match fails with a message naming the email the invite was for, so a person signed in to the wrong Google account knows to switch. Showing that address is acceptable: whoever holds the link was sent it.
- **One pending invite per address.** There is at most one unused, unexpired invite per (site, email): creating another replaces it. Inviting or adding an email that already has a membership on the site is an error.
- **Showing the link again** generates a new token and invalidates the old link. Tokens are never stored in recoverable form.
- **The members page** lists members (email, name, role, `can_publish`) and pending invites. Admins can change a member's role or `can_publish`, remove a member, cancel an invite, or regenerate an invite link. Member names come from the provider at sign-in; site admins see only members of their own site.

## Sites in the database

### What moves

`ntx.toml` keeps only server settings:

```toml
bind_addr          = "localhost"
port               = 8080
db_dsn             = "sqlite://./ntx.db"
dev                = false
trusted_proxies    = ["127.0.0.1/32", "::1/128"]
session_secret     = "…"           # signs the short-lived OAuth state cookie
encryption_key     = "…"           # base64, 32 bytes; encrypts provider secrets
themes_path        = "./themes"
platform_hostname  = "localhost"
root_password_hash = "$2a$…"
```

`[[site]]`, `[[site.section]]`, `global_admin_password_hash`, per-site `admin_password_hash`, `template_path` and `static_path` are removed. `config.Load` rejects a config that still contains `[[site]]` blocks or `global_admin_password_hash`, naming the key and pointing to the docs, so an old file fails loudly instead of being half-applied.

A site row holds:

- `key`: immutable after creation, validated as today (`[a-z0-9][a-z0-9_-]*`). Unique. Names the `ntx generate` output directory.
- `hostname`: stored lowercase, validated as today's `site_url` (bare host, no scheme, port or path), not equal to `platform_hostname`. Unique. Editable. Changing it shows a warning: the provider's callback URL must be updated, outstanding invite links stop working, and everyone on the site must sign in again. The site's sessions are deleted.
- `name`, `description`, `about_slug`.
- `theme`: a theme name (below).
- Provider: `provider_type` (`google`, `auth0`, or none), `provider_client_id`, encrypted `provider_client_secret`, and `provider_domain` (Auth0 only). A site with no provider can only be signed in to by root.

Sections move to `sections` (site, slug, title, vars as JSON, position) and `section_pages` (section, slug, title, vars as JSON, position).
- **Both slugs are validated:** section slugs and page slugs get `config.CheckSectionSlug`'s single-segment rule. Page slugs end up in template names and in `ntx generate` output paths, so they need it as much as section slugs.
- **Reserved section slugs** are `blog`, `static`, `admin`, `about`, plus `auth` and `invite`.
- Site admins edit sections in the browser. Vars are edited as a JSON object per section and per page; key/value rows would be friendlier but can come later.

Site deletion is root or platform admin only, and requires typing the site key to confirm. It is permanent: it cascades to the site's posts, tags, sections, memberships, invites and sessions. Users themselves are not deleted.

### Themes

`themes_path` contains one directory per theme:

```
themes/
  blog/
    templates/   base.html, home.html, blog/index.html, blog/post.html, sections/…
    static/
  nerdtx/
    templates/
    static/
```

- **Picking a theme.** A site's theme is picked from a list of the directory names found under `themes_path`. Names are validated with the `key` pattern, and the resolved path must stay inside `themes_path`. The browser never supplies a path.
- **Existing templates move.** The `templates/` and `static/` at the repo root, and `samples/*`, become themes under `themes/`.
- **Caching.** Template caches are keyed by theme, so sites on the same theme share one, as they do today by path. They load lazily, and dev mode re-reads templates on each request as now.

**Admin and platform views are not themed.** Their templates and CSS are embedded in the binary (`embed.FS`) and served under `/admin/assets/`. The `admin/` folders currently inside each theme are removed. This is what lets the platform hostname work with no theme, and keeps admin pages consistent across sites.

### Routing changes

- **Site resolution** reads from an in-memory hostname → site map, loaded at boot and reloaded after any site write. This is acceptable because ntx is single-instance.
- **Section routes** can no longer be registered once at boot, because sections change at runtime. A single `GET /{section}/{page}` route, registered after the built-ins, looks up the section for the resolved site. chi prefers static segments, so `/blog/{slug}`, `/static/*`, `/admin/...`, `/auth/...` and `/invite/...` keep precedence. Reserved-slug validation still applies, so a section can never be unreachable.
- **New routes:**
  - sign-in: `/auth/start`, `/auth/callback`, `/invite/{token}`
  - site admin: `/admin/members`, `/admin/settings`
  - platform: `/admin/platform/sites`, `/admin/platform/sites/new`, `/admin/platform/sites/{key}`, `/admin/platform/users`, `/admin/platform/users/{id}`
- **Authorization** moves from one "is there a valid session for this site" middleware to permission checks per action, in one place: a small `authz` package with functions like `CanEditPost(actor, post)` and `CanPublish(actor, site)`. Handlers call these rather than comparing roles inline. Tests cover the whole role table above.

## Data model

### Schema and the database

- **One fresh schema per dialect.** SQLite and Postgres each get a single goose migration, numbered **`100_initial.sql`**, replacing the current `001`–`003`.
- **Old databases are refused.** At startup, if the database has any goose version below 100 recorded, ntx refuses to start with a message saying the database predates this release and must be deleted. A rewritten `001` would otherwise be skipped silently on an existing database.
- **Removed:** `EnsureSites`, the orphaned-site warning and the `default` site backfill.
- **SQLite foreign keys.** SQLite enables foreign keys through the DSN (`_pragma=foreign_keys(1)`) rather than a one-off `PRAGMA`, so cascades hold on every connection.

### Tables

```
users          id, email (unique, lowercase, nullable: NULL only for root), name,
               is_root, is_platform_admin, created_at
               partial unique index on is_root WHERE is_root
identities     id, user_id → users (cascade), issuer (configured issuer), subject,
               email_at_link, created_at                       unique (issuer, subject)
sites          id, key (unique), hostname (unique), name, description, about_slug, theme,
               provider_type, provider_client_id, provider_client_secret_enc, provider_domain,
               created_at, updated_at
memberships    id, user_id → users (cascade), site_id → sites (cascade), role ('admin'|'contributor'),
               can_publish, created_at                         unique (user_id, site_id)
invites        id, site_id → sites (cascade), email (lowercase), role, can_publish,
               token_hash (unique), created_by → users (set null), expires_at, used_at, created_at
               partial unique index on (site_id, email) WHERE used_at IS NULL
sessions       id, token_hash (unique), user_id → users (cascade), site_id → sites (cascade, nullable),
               root_hash_digest (nullable), created_at, expires_at, last_seen_at
posts          id, site_id → sites (cascade), author_id → users (set null), slug, title, body,
               published, first_published_at (nullable), created_at, updated_at
               unique (site_id, slug)
post_revisions id, post_id → posts (cascade, unique), author_id → users (set null),
               title, body, tags (JSON array), base_updated_at, created_at, updated_at
tags, post_tags   as today, site-scoped
sections, section_pages   as above
```

On "one pending invite per address": an expired but unused invite also blocks a new one under that index, so creating an invite first deletes expired unused invites for that (site, email).

### Post rules

- **Pending revision.**
  - There is at most one per post. It carries title, body and tags, but not slug, since contributors can't change a live-once post's slug.
  - **Publishing** a revision copies those fields onto the post and deletes the revision. It does not change whether the post is published: on an unpublished live-once post it updates the content, and a site admin publishes separately. If the post's `updated_at` no longer equals the revision's `base_updated_at`, meaning someone edited the post directly since the contributor started, the publish screen shows a warning and needs confirmation, because publishing would overwrite those edits.
  - **Discarding** deletes the revision.
  - A never-published draft never gets a revision; edits to it change the post directly.
- **`first_published_at`** is set the first time a post is published and never cleared. It is what "live-once" and "never published" mean.
- **Deleted authors.** Posts and revisions by a deleted user keep their content, with `author_id` set to NULL and the author shown as "former member". Only site admins can edit them from then on.
- **Removing someone from a site** keeps their posts on that site. Their `author_id` still points at them.
- **Root bootstrap.** At boot, ntx makes sure exactly one `is_root` user exists (creating it if absent). `root_password_hash` must be set, or `config.Load` fails.

## Admin views

Exact layout is the builder's call; this is the inventory. All admin pages use the embedded admin templates.

**Site admin area** (on a site's host):
- `/admin/posts`: all posts for site admins, own posts for contributors. Shows status (draft, published, unpublished, pending changes) and author. Pending-changes posts link to a side-by-side view of current vs. pending, with Publish changes (and the stale-base warning) and Discard.
- Editor: Save, plus Publish, Unpublish and Delete, each shown only if allowed. For a contributor without `can_publish` editing a live-once post, the editor says "Your changes will be reviewed before they go live", and the slug is read-only.
- `/admin/members`: members, pending invites, Add person, Invite person.
- `/admin/settings`: name, description, about slug, sections.

**Platform views** (root and platform admins, on any host where they're signed in, and on the platform hostname):
- `/admin/platform/sites`: all sites with hostname, theme, provider and member count.
  - Create a site (key, hostname, name, theme).
  - Edit a site, including provider settings and the callback URL to copy.
  - Delete a site.
- `/admin/platform/users`: all users across sites, with their memberships, identities and platform-admin flag.
  - Grant or revoke platform admin.
  - Edit email; add or remove a membership on any site.
  - Unlink an identity, revoke all sessions, delete a user.
  - Root is listed but has no destructive controls.

## CLI

- `ntx serve`: as now.
- `ntx generate`: reads sites, sections and published posts from the database; output layout unchanged (`<out>/<key>/`).
- `ntx gen-config`: writes the new config shape, prompts for the root password, and generates `session_secret` and `encryption_key`.
- `ntx hash-password`: unchanged, used for `root_password_hash`.

## Build order

This is one design delivered as three PRs, each leaving `main` working:

1. **Sites in the database, root, platform views, themes.**
   - The new schema (all tables, including those used later), version refusal, and the config shape, including `trusted_proxies`.
   - Root login, the failure-only limiter, and DB-backed sessions with root invalidation.
   - The platform hostname and CSRF protection.
   - Platform views for sites, themes, and embedded admin templates.
   - Dynamic section routes, sections in the browser with page-slug validation, `generate` reading from the database, and published-only public tags.
   - At the end of this phase only root can sign in, and it can create sites and write posts on any of them.
2. **People and SSO.**
   - Users and identities, Google and Auth0 sign-in, and provider settings with the secret-clearing rule.
   - Memberships with both roles and `can_publish`, add and invite, and the members page.
   - The platform users view (unlink and revoke included) and platform admins.
   - Authorship and `first_published_at` enforcement.
   - The `authz` package with role-table tests, and "contributors see only their own posts".
   - At this stage a contributor without `can_publish` can't edit a live-once post at all: the editor is read-only for it.
3. **Pending revisions.** `post_revisions`, saving edits to live-once posts as pending, the pending-changes view, Publish changes with the stale-base warning, and Discard. This replaces phase 2's read-only stopgap.

Each phase gets its own implementation plan.

## Testing notes

- **SSO.** Tests use an in-process fake OIDC provider (an `httptest` server serving discovery, JWKS, authorize and token) with keys generated in the test. No network. Cover:
  - a callback finished on a different site from where it started;
  - an invite accepted by someone who already has an identity;
  - the reassigned-account guard;
  - concurrent invite acceptance;
  - an unverified email.
- **Roles.** The role table in "Roles" becomes a table-driven test over `authz`, run against every action.
- **Tenant isolation.** The tenant-isolation tests from the multi-tenant work stay, and are extended to memberships, invites and sessions. For example, a site-A session or invite must never work on site B.
- **Anti-goals.** Each gets a test:
  - root can't be deleted, demoted or given an email or identity through the service layer;
  - failed guesses from one IP don't block root from another;
  - an unknown provider login returns 403;
  - a contributor without `can_publish` editing a live-once post, or tagging a draft, leaves every public page unchanged;
  - a theme name, section slug or page slug containing `..` or `/` is rejected;
  - changing `root_password_hash` invalidates root sessions;
  - changing the provider domain clears the stored secret.

## Left to the builder

- Page layout and styling of the admin and platform views.
- Where the service layer sits relative to `models` and `handlers`. The rule is that permission checks live in `authz` and are called from one layer, not scattered.
- How expired sessions and invites are cleaned up.
- Whether the side-by-side pending view shows a diff or two plain panes.
- Whether the site cache reload is a full reload or an incremental update.

## Decisions

**By the owner:**
- The four roles, `can_publish`, and that edits to live posts wait for a site admin.
- Root as `root` with a config password, which can't be deleted or changed from the web.
- Platform admins attached to a site, and signing in through its provider.
- Only Auth0 and Google; only platform admins configure providers.
- Invite links rather than email.
- Sites in the database, with themes from a folder.
- No migrations.
- Single instance behind a proxy.
- Platform admins can delete sites.

**Calls made during design** (not explicitly decided by the owner):
1. Platform admins and root have site-admin powers on whichever site they're signed in to.
2. Invites are tied to an email, and the provider's verified email must match.
3. Contributors can delete their own never-published drafts even without `can_publish`.
4. Site deletion is permanent and cascades to all the site's content; users survive.
5. `platform_hostname` exists so root can sign in before any site exists, and no site may use it.
6. Admin templates are embedded and not themed.
7. Sessions move to the database; `encryption_key` is a new, separate config value.
8. `auth` and `invite` join the reserved section slugs.
9. Section vars are edited as JSON for now.
10. Contributors can't change a live-once post's slug; a pending revision carries title, body and tags only.
11. Publishing a pending revision doesn't change whether the post is published.
12. `users.email` is the added or invited address and isn't updated from providers.
13. The reassigned-account guard refuses a second subject from the same issuer for one user.
14. Site admins can't manage platform admins' memberships.
15. Changing a site's hostname deletes its sessions.
