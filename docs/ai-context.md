# ntx — AI context reference

This document describes the ntx codebase for AI assistants. It covers architecture, conventions, data flow, and the constraints that govern extension work. Read this before touching any file.

## What ntx is

A self-hosted site server written in Go: one binary that serves blogs, content sections and an admin UI for one or more sites. The site that runs on nerdtx.net is one deployment of ntx.

**ntx is multi-tenant, and sites are managed in the browser.** `ntx.toml` holds only server settings. Sites, their sections, posts, tags and sessions live in the database. A request is matched to a site by its `Host` header (lowercased, port stripped) against each site's `hostname`, or to the configured `platform_hostname`, where root signs in before any site exists. Any code touching content must stay inside the resolved site's scope; that is the invariant this codebase exists to protect.

The design for roles and SSO is `docs/superpowers/specs/2026-11-07-roles-and-sso-design.md`. All three phases are built: 1 (sites in the database, root, platform views, themes), 2 (people, memberships, invites, Google/Auth0 SSO, platform admins) and 3 (pending revisions for contributors without `can_publish`).

## Module and Go version

```
module github.com/nod/ntx
go 1.26.4
```

## Directory layout

```
main.go                        # CLI entrypoint (cobra); openApp wires everything for serve/generate
internal/
  validate/validate.go         # naming rules: Key, Hostname, Segment, SectionSlug, ReservedSectionSlugs
  config/config.go             # server-only Config; rejects legacy [[site]]-era keys
  db/db.go                     # SQLite/Postgres open, goose migrations, legacy-schema refusal
  db/migrations/{sqlite,postgres}/100_initial.sql   # the whole schema
  db/dbtest/dbtest.go          # tests' database: in-memory SQLite, or a fresh Postgres schema
  models/site.go               # SiteStore: sites + sections CRUD with validation; ValidationError
  models/user.go               # UserStore: root bootstrap, people by email, edits (root-guarded)
  models/membership.go         # MembershipStore: role + can_publish per (user, site)
  models/identity.go           # IdentityStore: provider logins, reassigned-account guard
  models/invite.go             # InviteStore: hashed tokens, transactional Accept
  models/session.go            # SessionStore: DB-backed sessions (token hash, host-bound)
  models/post.go               # PostStore: site-scoped posts and tags (ForSite), authors
  models/unique.go             # isDuplicate, retryOnDuplicate: unique-value races become form errors
  models/revision.go           # PostStore pending revisions: save, publish (stale check), discard
  secretbox/secretbox.go       # AES-256-GCM for provider client secrets
  sso/                         # OIDC client (go-oidc), signed ntx_oauth flow state; ssotest = fake provider
  people/people.go             # sign-in policy (invite → identity → email match → membership), AddMember
  authz/authz.go               # the role table: every permission check
  clientip/clientip.go         # real client IP / scheme, believing X-Forwarded-* only from trusted_proxies
  auth/auth.go                 # Manager (Login/Logout/Middleware), Actor, RootDigest, bcrypt helpers
  auth/limiter.go              # failure-only sign-in limiter, per client IP
  themes/themes.go             # List and Dir: safe theme lookup under themes_path
  adminui/                     # embedded admin templates + assets (not themed)
  sitereg/registry.go          # in-memory hostname → site map, reloaded after site writes
  sitectx/sitectx.go           # request context: resolved *models.Site, or the platform-host flag
  handlers/handler.go          # Deps, Handler, TemplateCache (per theme), serve, render, Require*
  handlers/routes.go           # Mount: the route table
  handlers/{home,blog,section,admin,settings,platform,generate}.go
  handlers/signin.go           # /auth/start, /auth/callback, /invite/{token}
  handlers/members.go          # /admin/members, /admin/invites
  handlers/users.go            # /admin/platform/users
  server/server.go             # middleware stack and host resolution around Mount
themes/{default,blog,nerdtx}/  # shipped themes: templates/ and static/
docs/                          # documentation
```

## Config (`internal/config/config.go`)

Loaded by viper from `ntx.toml`, then `NTX_*` env vars, then defaults.

```go
type Config struct {
    BindAddr         string         // "" = localhost only
    Port             int            // default 8080
    DBDSN            string         // default sqlite://./ntx.db
    Dev              bool           // re-parse templates per request; cookies without Secure
    TrustedProxies   []netip.Prefix // trusted_proxies (CIDRs); default none
    SessionSecret    string         // required, ≥32 chars
    EncryptionKey    []byte         // encryption_key, base64 → exactly 32 bytes; seals provider client secrets
    ThemesPath       string         // default ./themes
    PlatformHostname string         // required, bare host, lowercased
    RootPasswordHash string         // required, valid bcrypt
}
```

`Load` rejects the legacy keys `site`, `global_admin_password_hash`, `template_path` and `static_path`, naming the key and pointing to `docs/usage.md`. Don't add fallbacks for the required secrets.

## Data model

One goose migration per dialect, `100_initial.sql`. It holds every table the full design needs: `users`, `identities`, `sites`, `memberships`, `invites`, `sessions`, `posts`, `post_revisions`, `tags`, `post_tags`, `sections` and `section_pages`. `db.Open` returns `ErrLegacySchema` for a database that recorded any goose version below 100: there is no upgrade path. SQLite turns foreign keys on through the DSN (`_pragma=foreign_keys(1)`), and deletes cascade: deleting a site removes its posts, tags, sections and sessions.

- **Sites** (`models.Site`): `Key` is immutable after creation and names the generate output folder. `Hostname` is lowercase, unique, and never equal to `platform_hostname`. Changing it deletes the site's sessions. `Theme` is a theme name. Sections and pages are stored in their own tables with `vars` as JSON objects; `SiteStore.SetSections` validates the whole set before writing, so a rejected set changes nothing.
- **Users** (`models.User`): root (created by `EnsureRoot`; NULL email; a partial unique index allows one `is_root` row) and people, keyed by lowercase email. Every store write that would give root an email, identity, membership or name, or change or delete it, returns `ErrRootProtected`. Emails go through `models.NormalizeEmail`.
- **Memberships**: one per (user, site), role `admin` or `contributor`, plus `can_publish` (meaningful for contributors only).
- **Identities**: (configured issuer, subject) → user. `Link` refuses a second subject from the same issuer for one user (`ErrIdentityConflict`, the reassigned-account guard).
- **Invites**: sha256 of the token, 7-day expiry, one unused row per (site, email). `Accept` is one transaction: conditional `used_at` update, find-or-create the user, link the identity, add the membership.
- **Sessions** (`models.Session`): the cookie holds a random token, and the DB stores its sha256. `SiteID` is the site the session was issued on, or nil for root on the platform host. Root sessions store `RootDigest(root_password_hash)`. `last_seen_at` is written at most once a minute.
- **Posts** (`models.PostStore`): every query is scoped by `ForSite(siteID)`, with the site condition written as SQL; GORM drops zero-valued struct fields from a struct-based Where. `FirstPublishedAt` is set on the first publish and never cleared. `AllTags` returns only tags on published posts, because it feeds public pages; `Stats` counts every tag. `UpdateContent` saves content without touching the published state (the editor's Save uses it); `Update` also writes `published`. The three post writes share `writePost`, which holds the post's row lock; when `p.ContentUpdatedAt` is set (the handler fills it from the editor's `base`), a post whose content changed since returns `ErrEditConflict`, and the editor comes back (409) with the person's text and the current version as its base. `UpdateDraft` and `DeleteDraft` are for people who may only touch never-published drafts: the "never published" check is part of the write, so a post an admin publishes at the same moment is left alone (`ErrLiveOnce`).
- **Pending revisions** (`models.Revision`, `post_revisions`, methods on `PostStore`): at most one per post, only on live-once posts (`ErrNotLiveOnce`). Title, body and tags, never slug. `BaseUpdatedAt` is the post's `ContentUpdatedAt` (the `content_updated_at` column, moved only when the title, body or tags actually change, never by publishing, unpublishing or a slug change) when the contributor opened the editor (sent back in the form's hidden `base` field), kept on later saves; `Stale(post)` compares the two. The revision's `UpdatedAt` is its version: `PublishPending` and `DiscardPending` take the version the admin was shown and return `ErrRevisionChanged` if it moved, and `PublishPending` returns `ErrStale` unless its `overwrite` argument is the post's current `ContentUpdatedAt` (the "publish anyway" form sends the version the page showed, so a later edit is never overwritten unseen). Revision timestamps are written truncated to microseconds, so SQLite and Postgres store exactly what Go holds; compare only values read back from the database, with `Equal`. Every revision write first locks the post row (`lockPostInSite`, `SELECT ... FOR UPDATE` on Postgres), so writes to one post's revision run one at a time and a direct edit can't slip between a publish's stale check and its copy; publish and discard delete only the version they checked. Lock order is person, site, post: `lockPostInSite` first takes a key-share lock on the site row (`keyShare`), and `SavePending` on its author's user row before that, so deleting a site or a person waits for an in-flight post write instead of deadlocking with it (a delete locks the parent row, then cascades to the post). A new write under the post lock that adds a row pointing at a user or site must take that parent's key-share lock first, in this order. `revision_race_test.go` holds another request's writes open to test this; only Postgres runs the requests side by side, since SQLite runs them one after another.

Store methods return `*models.ValidationError` for problems with user input. Handlers show its message on the form; any other error is a 500.

## Request flow (`internal/server/server.go`)

Middleware, outermost first:

1. `http.CrossOriginProtection`, on the whole router: it rejects cross-origin state-changing requests. `SameSite=Strict` alone treats sibling subdomains as same-site.
2. `clientip.Middleware(cfg.TrustedProxies)`.
3. Logger, Recoverer, security headers. The CSP is `default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:`, so no inline scripts.
4. `resolveHost`: a site from `sitereg.Registry.ByHost`, otherwise the platform flag if the host equals `platform_hostname`, otherwise a bare 404.
5. `auth.Manager.Middleware`: attaches an `*auth.Actor` if the cookie's session is valid **for this host**. It never redirects.

Then `(*handlers.Handler).Mount` registers the routes (`internal/handlers/routes.go`):

| Route | Notes |
|---|---|
| `GET /`, `/about`, `/blog`, `/blog/{slug}`, `/static/*` | public, from the site's theme |
| `GET /{section}/{page}` | registered last; static segments win in chi |
| `/admin/assets/*` | embedded CSS/JS |
| `GET/POST /admin/login`, `POST /admin/logout` | root sign-in on any site host or the platform host; the provider button on sites that have one |
| `/auth/start`, `/auth/callback`, `/invite/{token}` | SSO and invites; site hosts only |
| `/admin/posts...` | `RequireMember`, then per-action `authz` checks |
| `/admin/members...`, `/admin/invites/...`, `/admin/settings` | `RequireSiteAdmin`: needs a site, otherwise 404 (e.g. on the platform host) |
| `/admin/platform/sites...`, `/admin/platform/users...` | `RequirePlatform`: root and platform admins, on any host they're signed in on |

Each handler that needs a site goes through `h.serve(w, r, func(w, r, site *models.Site))`, the single place an unresolved site becomes a 404. Theme templates render through `h.render(w, site, name, data)`, which merges `SiteName`, `SiteURL` (the hostname) and `SiteDescription`. Admin pages render through `h.admin(w, r, page, data)`, which adds `Actor`, `Site` and `Error` and uses `adminui`.

After any write to sites or sections, call `Registry.Reload()`. ntx is single-instance, so the in-memory registry is the cache.

## Auth (`internal/auth`)

- **Root sign-in.** `POST /admin/login` takes username `root` and checks the password against `RootPasswordHash` from config.
- **Rate limiting.** The `Limiter` counts **failures only**: 5 per 15 minutes per `clientip.IP`, shared across all hosts.
- **Sessions.** `Manager.Login(w, user, siteID)` issues a session bound to the current host. The cookie is `ntx_session`: HttpOnly, `SameSite=Strict`, `Secure` unless dev, no Domain.
- **Session validity.** A session is valid only if:
  - its `SiteID` matches the resolved site (or is nil on the platform host);
  - the user still exists;
  - for root, its stored digest matches the current hash.

  - for everyone else, a non-nil site and a **current membership** on it (platform admins included), attached as `Actor.Membership`. User, flag and membership are re-read every request, so revocations are immediate.
- **`Actor`.** `IsRoot()` and `IsPlatform()` are nil-safe. Every other permission is an `authz` function.
- **SSO.** `/auth/start` (GET, or POST from an invite page) sets the signed `ntx_oauth` cookie (state, nonce, PKCE verifier, site ID, invite; 10 min, `SameSite=Lax`, `Path=/auth`) and redirects to the provider. `/auth/callback` checks the cookie's site and state, exchanges the code, verifies the ID token and `email_verified`, then `people.SignIn`, then `Auth.Login`, and ends on an interstitial page (meta refresh) because the session cookie is `SameSite=Strict`. The callback URL is `https://<hostname>/auth/callback` (dev: request scheme and port). Tests use `sso/ssotest` and `Deps.IssuerFor`.

### Authorization

`internal/authz` is the role table. Handlers call it (`SiteAdmin`, `SiteMember`, `CanSeePost`, `CanEditPost`, `SavesAsPending`, `CanReviewChanges`, `CanPublishPost`, `CanDeletePost`, `CanManageMember`, `CanChangeUser`, …) and never compare roles themselves. `CanEditPost` means edits apply directly; `SavesAsPending` (never true together with it) means they become the post's pending revision. The review routes are `/admin/posts/{id}/pending` (GET), `.../pending/publish` and `.../pending/discard` (POST), in the `RequireMember` group, gated by `CanReviewChanges`. Route groups: `RequireMember` (posts), `RequireSiteAdmin` (members, settings), `RequirePlatform` (platform views). A post a contributor can't see is a 404.

## Themes and templates

A theme is `themes_path/<name>/templates/` plus `themes_path/<name>/static/`. Names follow `validate.Key`, and `themes.Dir` refuses anything that isn't a single existing folder, so the browser never supplies a path. Template caches are keyed by theme name and built lazily. In dev mode, templates are parsed per request.

Theme layout:
- `base.html` must exist; every page is parsed with it.
- `home.html`, `blog/index.html` and `blog/post.html`.
- Section pages live at `sections/{section}/{page}/index.html`. A missing section template is a 404 when served, and is skipped with a warning by generate.

One template function is registered: `join` (wraps `strings.Join`).

Admin templates live in `internal/adminui/templates/` (embedded) and are **not** part of themes. Their JS is `assets/admin.js`, driven by `data-` attributes (`data-slug-source`, `data-filter-list`, `data-confirm`), because the CSP forbids inline scripts.

## Static site generation (`internal/handlers/generate.go`)

`h.Generate(outDir)` walks `Registry.All()` in key order and stops at the first failing site. For each site, under `{outDir}/{key}/`, it writes:
1. home;
2. the blog index (published posts, unfiltered);
3. each published post;
4. each section page;
5. a copy of the theme's `static/`.

Admin routes and the `/about` redirect are not generated. Tag filtering isn't static-compatible.

## CLI commands (`main.go`)

| Command | Notes |
|---|---|
| `serve` | `openApp` (config → DB → EnsureRoot → registry → auth → handlers), then `server.New` |
| `generate` | `openApp`, then `Generate(--out)` |
| `hash-password` | bcrypt hash for `root_password_hash`; reads one line from stdin when not a terminal |
| `gen-config` | sample config with generated `session_secret` and `encryption_key`; `-p` prompts for the root password |
| `version` | build version |

## How to add a new public route

1. Add an exported method on `*Handler` whose body is `h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) { ... })`.
2. Register it in `handlers/routes.go` before the `/{section}/{page}` catch-all. If its first path segment could clash with a section slug, add that segment to `validate.ReservedSectionSlugs`.
3. Render with `h.render(w, site, "path/to/template.html", data)`.

Don't add a route for content that fits sections; define a section in the site's settings instead.

## Key constraints

- **No CGo.** SQLite is `modernc.org/sqlite` via `glebarez/sqlite`.
- **Tenant isolation.** Every content query goes through `PostStore.ForSite` or a site ID taken from `sitectx`. Never trust an ID from the request without scoping it to the resolved site.
- **Host-bound sessions.** Never accept a session on a host other than the one it was issued for.
- **No path from the browser.** Theme names, site keys, and section and page slugs are validated (`internal/validate`) before they touch the filesystem.
- **Markdown safety.** Post bodies render through goldmark, which drops raw HTML by default. Don't enable unsafe rendering.
- **Single instance.** The registry and the limiter are in memory. Running several processes against one database isn't supported.
- **Unique fields.** A write to a unique value (email, membership, identity, site key and hostname, sections, invite, tag) runs its pre-check and the write inside `retryOnDuplicate`: if another request took the value after the check, the write is retried once in a fresh savepoint, and the check then returns its usual form error (or finds the row). Post slugs have no pre-check; `slugTaken` turns the unique index's error into a form error. Never let a unique-constraint error reach a handler as a 500. Tags are resolved in sorted order, so two transactions creating the same new tags can't deadlock.
- **Both databases.** Tests open their database with `dbtest.Open(t)`, never `db.Open` directly. With `NTX_TEST_POSTGRES_DSN` set (any database on a Postgres server), each test gets its own schema there; CI runs the suite on SQLite and again on Postgres. Raw SQL in tests and code must work on both: `TRUE`/`FALSE`, not `1`/`0`. Race tests hold another request's write open in a transaction, start the call, then `dbtest.AwaitBlocked(t, tx)` (or `AwaitBlockedN` for several calls, counting ones queued behind another) before ending it: on Postgres that waits until the call is really stuck behind the transaction (and fails if it never is), so a slow runner can't make the test pass without exercising the race. Never use a sleep for this.

## Dependencies worth knowing

| Package | Role |
|---|---|
| `github.com/go-chi/chi/v5` | HTTP router |
| `gorm.io/gorm` (+ `glebarez/sqlite`, `gorm.io/driver/postgres`) | database access |
| `github.com/pressly/goose/v3` | migrations |
| `github.com/spf13/viper` | config loading (TOML + env) |
| `github.com/yuin/goldmark` | Markdown rendering |
| `golang.org/x/crypto` | bcrypt |
| `github.com/spf13/cobra` | CLI commands |
