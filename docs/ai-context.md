# ntx — AI context reference

This document describes the ntx codebase for AI assistants. It covers architecture, conventions, data flow, and the constraints that govern extension work. Read this before touching any file.

## What ntx is

A self-hosted site server written in Go: one binary that serves blogs, content sections and an admin UI for one or more sites. The site that runs on nerdtx.net is one deployment of ntx.

**ntx is multi-tenant, and sites are managed in the browser.** `ntx.toml` holds only server settings. Sites, their sections, posts, tags and sessions live in the database. A request is matched to a site by its `Host` header (lowercased, port stripped) against each site's `hostname`, or to the configured `platform_hostname`, where root signs in before any site exists. Any code touching content must stay inside the resolved site's scope; that is the invariant this codebase exists to protect.

The design for roles and SSO is `docs/superpowers/specs/2026-11-07-roles-and-sso-design.md`. Phase 1 is built: sites in the database, root, the platform views, and themes. Phases 2 (people and Google/Auth0 SSO) and 3 (pending revisions) aren't built yet. Their tables already exist in the schema.

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
  models/site.go               # SiteStore: sites + sections CRUD with validation; ValidationError
  models/user.go               # UserStore: EnsureRoot, GetByID
  models/session.go            # SessionStore: DB-backed sessions (token hash, host-bound)
  models/post.go               # PostStore: site-scoped posts and tags (ForSite)
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
    EncryptionKey    []byte         // encryption_key, base64 → exactly 32 bytes; for secrets (phase 2)
    ThemesPath       string         // default ./themes
    PlatformHostname string         // required, bare host, lowercased
    RootPasswordHash string         // required, valid bcrypt
}
```

`Load` rejects the legacy keys `site`, `global_admin_password_hash`, `template_path` and `static_path`, naming the key and pointing to `docs/usage.md`. Don't add fallbacks for the required secrets.

## Data model

One goose migration per dialect, `100_initial.sql`. It holds every table the full design needs: `users`, `identities`, `sites`, `memberships`, `invites`, `sessions`, `posts`, `post_revisions`, `tags`, `post_tags`, `sections` and `section_pages`. `db.Open` returns `ErrLegacySchema` for a database that recorded any goose version below 100: there is no upgrade path. SQLite turns foreign keys on through the DSN (`_pragma=foreign_keys(1)`), and deletes cascade: deleting a site removes its posts, tags, sections and sessions.

- **Sites** (`models.Site`): `Key` is immutable after creation and names the generate output folder. `Hostname` is lowercase, unique, and never equal to `platform_hostname`. Changing it deletes the site's sessions. `Theme` is a theme name. Sections and pages are stored in their own tables with `vars` as JSON objects; `SiteStore.SetSections` validates the whole set before writing, so a rejected set changes nothing.
- **Users** (`models.User`): phase 1 has only root, created by `EnsureRoot`. Root's email is NULL, and a partial unique index allows one `is_root` row.
- **Sessions** (`models.Session`): the cookie holds a random token, and the DB stores its sha256. `SiteID` is the site the session was issued on, or nil for root on the platform host. Root sessions store `RootDigest(root_password_hash)`. `last_seen_at` is written at most once a minute.
- **Posts** (`models.PostStore`): every query is scoped by `ForSite(siteID)`, with the site condition written as SQL; GORM drops zero-valued struct fields from a struct-based Where. `FirstPublishedAt` is set on the first publish and never cleared. `AllTags` returns only tags on published posts, because it feeds public pages; `Stats` counts every tag.

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
| `GET/POST /admin/login`, `POST /admin/logout` | root sign-in on any site host or the platform host |
| `/admin/posts...`, `/admin/settings` | `RequireSiteAdmin`: needs a site, otherwise 404 (e.g. on the platform host) |
| `/admin/platform/sites...` | `RequirePlatform`: root (or, later, platform admins) on any host |

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

  Phase 1 accepts only root sessions. Phase 2 adds members, who also need a current membership on the session's site.
- **`Actor`.** `IsRoot()`, `IsPlatform()` and `CanAdminSite()` are all nil-safe.

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
