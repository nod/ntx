# ntx — AI context reference

This document describes the ntx codebase for AI assistants. It covers architecture, conventions, data flow, and the constraints that govern extension work. Read this before touching any file.

## What ntx is

A self-hosted personal site server written in Go. It is not a framework or a CMS — it is a single binary that serves a blog, content sections, and an admin UI, configured entirely via `ntx.toml` and HTML templates you supply. The site that runs on nerdtx.net is one deployment of ntx.

**ntx is multi-tenant.** One process serves one or more websites, each declared
as a `[[site]]` block in `ntx.toml` and selected per request by matching the
HTTP `Host` header against that site's `site_url`. Posts, tags, templates,
static files and admin sessions are all scoped to a site. Any code touching
content must stay inside the resolved site's scope; that is the invariant this
codebase exists to protect.

## Module and Go version

```
module github.com/nod/ntx
go 1.26.4
```

## Directory layout

```
main.go                        # CLI entrypoint (cobra)
ntx.toml                       # local dev config (not committed in production)
internal/
  config/config.go             # Config + Site structs, viper load, validation
  db/db.go                     # SQLite/Postgres init and migrations
  db/migrations/               # .sql migration files (sqlite/ and postgres/)
  auth/auth.go                 # bcrypt password check, site-scoped sessions
  sitectx/sitectx.go           # carries the resolved *config.Site on the request context
  models/site.go               # dbSite, EnsureSites: upserts sites, assigns Site.ID
  models/post.go               # PostStore: site-scoped CRUD for blog posts
  handlers/handler.go          # TemplateCache, siteHandler, Handler struct, render()
  handlers/home.go             # GET /
  handlers/blog.go             # GET /blog, GET /blog/{slug}
  handlers/projects.go         # GET /{section}/{page} (content sections)
  handlers/generate.go         # h.Generate() — static site output
  handlers/admin.go            # /admin/* routes (auth-gated)
  server/server.go             # chi router wiring
templates/                     # HTML templates (user-supplied)
static/                        # CSS, images, JS (user-supplied)
samples/                       # example site configurations
docs/                          # documentation
```

## Config (`internal/config/config.go`)

Loaded by viper from `ntx.toml`, then env vars (`NTX_*`), then defaults.

Process-global settings live at the root of the file; per-site settings live in
`[[site]]` blocks, with sections nested as `[[site.section]]`.

```go
type Config struct {
    BindAddr                string // bind_addr ("" = localhost only)
    Port                    int    // port (default 8080)
    DBDSN                   string // db_dsn (default sqlite://./ntx.db)
    SessionSecret           string // required: min 32 chars
    Dev                     bool   // dev mode: re-parse templates on every request
    TemplatePath            string // template_path (default ./templates) — global default
    StaticPath              string // static_path (default ./static) — global default
    GlobalAdminPasswordHash string // optional: bcrypt hash valid on every site
    Sites                   []Site // [[site]] blocks — at least one required
}

type Site struct {
    ID                int64     // NOT from TOML: assigned by models.EnsureSites at boot
    Key               string    // key — required, unique; the stable content owner id
    SiteURL           string    // site_url — required, unique; the Host routing key
    SiteName          string    // site_name (default "ntx")
    SiteDescription   string    // site_description
    AboutSlug         string    // about_slug (default "about")
    AdminPasswordHash string    // required: bcrypt hash
    TemplatePath      string    // template_path — defaults to Config.TemplatePath at load
    StaticPath        string    // static_path — defaults to Config.StaticPath at load
    Sections          []Section // [[site.section]] blocks
}

type Section struct {
    Slug  string                 // mapstructure:"slug"
    Title string                 // mapstructure:"title"
    Pages []SectionPage          // mapstructure:"pages"
    Vars  map[string]interface{} // mapstructure:",remain" — all other keys
}

type SectionPage struct {
    Slug  string                 // mapstructure:"slug"
    Title string                 // mapstructure:"title"
    Vars  map[string]interface{} // mapstructure:",remain" — all other keys
}
```

Validation in `Load()` rejects: a missing or under-32-char `session_secret`; zero
`[[site]]` blocks; a site with no `key` or no `site_url`; a duplicate `key` or
`site_url` (case-insensitive) across sites; a `key` that is not
`[a-z0-9][a-z0-9_-]*` (it names a `generate` output dir); a `site_url` with a
scheme, port, path or whitespace (routing matches the bare Host); a section
slug that is not one path segment or is reserved (`blog`, `static`, `admin`,
`about` — see `config.CheckSectionSlug`, which `server.New` also calls); and any `admin_password_hash` (or a non-empty
`global_admin_password_hash`) that is missing or not a well-formed bcrypt hash.
`Load()` also fills per-site defaults, including resolving an unset per-site
`template_path`/`static_path` to the global value — so handlers never need to
consult the global paths.

`Site.ID` is deliberately not read from TOML. `models.EnsureSites` writes it
after upserting the `sites` table, and it is the FK every post, tag and session
is scoped by. Persisted ids start at 1, so `ID == 0` means EnsureSites never
ran; `handlers.New` (which both `serve` and `generate` go through) rejects
that, because its per-site map is keyed by ID and every site would collapse
into one entry.

## Template system (`internal/handlers/handler.go`)

### siteHandler and the Handler map

`Handler` holds one `siteHandler` per configured site, in a
`map[int64]*siteHandler` keyed by `Site.ID`. A `siteHandler` bundles everything
one site's request needs: its `config.Site`, a `*models.PostStore` already
scoped via `ForSite(site.ID)`, and its own `TemplateCache` over that site's
`template_path`.

`h.current(r)` reads the resolved site from `sitectx` and looks up its
`siteHandler`; a nil result means no site was resolved and callers treat it as a
404. Every handler starts there — a handler that reaches for a store or a
template any other way is a tenant-isolation bug.

### TemplateCache

Two modes:

- **Production** (`dev = false`): all templates parsed once at startup and cached in memory. `Has(name)` checks the in-memory map. `Get(name)` returns from map.
- **Dev** (`dev = true`): no cache. `Has(name)` calls `os.Stat`. `Get(name)` re-parses from disk on every request. This means you can edit templates without restarting the server.

Every template is parsed as: `template.New("base.html").ParseFiles(base.html, page.html)`. The page template must define a block that `base.html` renders — the exact block names are determined by your `base.html`.

### render()

```go
func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{})
```

Always merges `siteData()` (SiteName, SiteURL, SiteDescription) with the provided `data` map before executing. Page data wins on key collision with site data.

### renderToBytes()

```go
func (h *Handler) renderToBytes(sh *siteHandler, name string, data map[string]interface{}) ([]byte, error)
```

Same merge logic as `render()` but returns the result as `[]byte` instead of writing to a `ResponseWriter`. Takes its `siteHandler` explicitly because the static site generator has no request to resolve one from. Defined in `handlers/generate.go`.

### siteData()

A method on `siteHandler`, returning that site's own values:
```go
map[string]interface{}{
    "SiteName":        sh.site.SiteName,
    "SiteURL":         sh.site.SiteURL,
    "SiteDescription": sh.site.SiteDescription,
}
```

Always available in every template.

## Routing (`internal/server/server.go`)

Uses `github.com/go-chi/chi/v5`. One router serves every site.

### Host resolution

`resolveSite` is the first content middleware. It builds a
`map[lowercased site_url]*config.Site` at startup, strips any `:port` from
`r.Host`, lowercases it, and looks the site up. On a match it attaches the site
with `sitectx.With`; on no match it returns a bare `http.NotFound` — the same
404 a missing page gives, deliberately, so one tenant's hostnames leak nothing
about the others. Every route below therefore runs with a resolved site, and
`sitectx.From(r)` is how handlers reach it.

Port-stripping and case-insensitivity are requirements, not conveniences: a
reverse proxy forwarding `Host: host:port` would otherwise 404 every request.

### Routes

Routes registered at startup:

| Method | Pattern | Handler |
|---|---|---|
| GET | `/` | `h.Home` |
| GET | `/about` | redirect → `/blog/<about_slug>` of the resolved site |
| GET | `/blog` | `h.BlogIndex` |
| GET | `/blog/{slug}` | `h.BlogPost` |
| GET | `/{section}/{page}` | `h.SectionPage` (per `[[site.section]]` in config) |
| GET | `/static/*` | per-site file server, built at boot, keyed by `Site.ID` |
| GET | `/admin/login` | `h.AdminLogin` |
| POST | `/admin/login` | `h.AdminLoginPost` |
| POST | `/admin/logout` | `h.AdminLogout` (auth) |
| GET | `/admin/posts` | `h.AdminPosts` (auth) |
| GET | `/admin/posts/new` | `h.AdminNewPost` (auth) |
| POST | `/admin/posts` | `h.AdminCreatePost` (auth) |
| GET | `/admin/posts/{id}/edit` | `h.AdminEditPost` (auth) |
| POST | `/admin/posts/{id}` | `h.AdminUpdatePost` (auth) |

chi routes cannot vary by Host, so section routes are registered once per
*unique* section slug across all sites, and `handlers.SectionPage` decides
per-request which site's `Section` content applies by reading `sitectx`. A slug
configured on one site but not another therefore 404s on the site that didn't
configure it. Slugs no site configures are never routed — they 404 at the chi
level automatically.

The auth-gated group uses `session.Middleware`, which accepts a session only if
`auth.Session.Allows` the resolved site: the session is either global (an
explicit flag, issued by `SetGlobalSession` for the global admin password) or
a site session for exactly that site ID (`SetSiteSession`). The cookie payload
is `<issuedAt>:global` or `<issuedAt>:site:<id>` with `id > 0`; no site ID
doubles as "global", and an unresolved site fails closed.

## Template data by route

### All routes

| Key | Type | Value |
|---|---|---|
| `.SiteName` | string | resolved site's `site_name` |
| `.SiteURL` | string | resolved site's `site_url` |
| `.SiteDescription` | string | resolved site's `site_description` |

### `/blog`

| Key | Type | Value |
|---|---|---|
| `.Posts` | `[]models.Post` | this site's published posts, newest first |
| `.ActiveTag` | string | active tag filter (empty if none) |
| `.AllTags` | `[]string` | all of this site's tags |

### `/blog/{slug}`

| Key | Type | Value |
|---|---|---|
| `.Post` | `models.Post` | the post |
| `.Rendered` | `template.HTML` | Markdown rendered to safe HTML |

### `/{section}/{page}`

| Key | Type | Value |
|---|---|---|
| `.Title` | string | page `title` field from config |
| `.SectionTitle` | string | section `title` field from config |
| *(any extra key)* | interface{} | extra vars from section or page; page wins on conflict |

### Admin routes

See `internal/handlers/admin.go` for the full set. Common keys: `.Posts`, `.Post`, `.Error`, `.Tags`.

## Blog post model (`internal/models/post.go`)

```go
type Post struct {
    ID        int64
    Slug      string
    Title     string
    Body      string    // raw Markdown
    Tags      []string
    Published bool
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

`PostStore` is **site-scoped**. `NewPostStore(db)` returns an unscoped store
whose only legitimate use is `ForSite(siteID)`, which returns a copy confined to
one site's rows; every read and write through it carries an explicit
`site_id = ?` condition, including the `Preload("Tags", ...)` on tag reads and
the find-or-create in `resolveTags`. Those conditions are written as SQL strings
rather than struct-valued `Where`s on purpose — GORM drops zero-valued struct
fields, which would silently widen a query to every site.

Key methods: `List()`, `ListByTag()`, `GetBySlug()`, `GetByID()`, `Create()`,
`Update()`, `Delete()`, `AllTags()`, `Stats()`. `Update` and `Delete` return a
not-found error when the id belongs to another site, rather than silently
affecting nothing.

Schema notes: `posts(site_id, slug)` and `tags(site_id, name)` are uniquely
indexed, so slugs and tag names are unique *per site*, not globally. Tags are
never shared across sites — two sites using the same tag name each own a row.

### Sites (`internal/models/site.go`)

`EnsureSites(db, sites)` upserts each configured site by `key`, keeps its
`hostname` column in sync with `site_url`, and writes the DB-assigned id back to
`config.Site.ID`. It must run before `handlers.New` or `server.New`. It also
warns (via `log`) about any `sites` row that owns posts or tags but matches no
configured `key` — the single-site upgrade trap, where content stays attached to
migration 002's `default` placeholder site.

## CLI commands (`main.go`)

| Command | Notes |
|---|---|
| `serve` | loads config, opens DB, starts HTTP server |
| `generate` | renders all public pages to disk |
| `hash-password` | prompts for password (no echo), prints bcrypt hash |
| `gen-config` | prints a sample `ntx.toml` with a freshly generated `session_secret`; `-p` flag prompts for password and embeds the bcrypt hash |
| `version` | prints the build version |

## Auth (`internal/auth/auth.go`)

Session-based. `SessionManager` stores session tokens in SQLite with expiry. The `Middleware` method is a chi middleware that 401s or redirects unauthenticated requests. Password check uses `bcrypt.CompareHashAndPassword`.

## Template file conventions

- `base.html` must exist at the root of `template_path`. Every page template is parsed with it.
- Section pages live at `templates/sections/{section-slug}/{page-slug}/index.html`.
- Blog templates live at `templates/blog/index.html` and `templates/blog/post.html`.
- Admin templates live at `templates/admin/`.
- `home.html` is at the root of `template_path`.

Templates use standard Go `html/template`. One template function is registered: `join` (wraps `strings.Join`).

## Static site generation (`internal/handlers/generate.go`)

`h.Generate(outDir string) error` writes all public pages to disk. Call sequence:

1. Home → `{outDir}/index.html`
2. Blog index (published posts, no tag filter) → `{outDir}/blog/index.html`
3. Each published post → `{outDir}/blog/{slug}/index.html`
4. Each section page (from `site.Sections`) → `{outDir}/{section}/{page}/index.html`
5. Copy `site.StaticPath` → `{outDir}/static/`

Section pages whose templates are missing log a warning and are skipped; the build continues. Admin routes and the `/about` redirect are not generated.

URL-to-file mapping: `/foo/bar` → `{outDir}/foo/bar/index.html`, `/` → `{outDir}/index.html`.

The `generate` CLI command (`main.go`) accepts `--out` (default `./out`) and `--config`. The `out/` directory is gitignored.

**Tag filtering is not static-compatible.** `generate` only writes the unfiltered blog index. Do not generate per-tag pages without first adding support for it.

## How to add a new hard-coded route

1. Add a handler method on `*Handler` in `internal/handlers/`.
2. Register the route in `internal/server/server.go`.
3. Add the template at the appropriate path under `template_path`.
4. Call `h.render(w, r, "path/to/template.html", data)` — site data is merged automatically.

Do not add a new route for content that fits the `[[section]]` pattern — use config instead.

## How to add a new config field

1. Add the field to `Config` in `internal/config/config.go` with the `mapstructure:"field_name"` tag.
2. Optionally add a `v.SetDefault(...)` call in `Load()`.
3. Reference `h.cfg.FieldName` in the handler.

## Key constraints

- **No CGo.** SQLite is via `modernc.org/sqlite` (pure Go). Do not introduce CGo dependencies.
- **No ORM.** Database access uses `database/sql` directly. Keep it that way.
- **Markdown safety.** Blog post bodies run through a sanitizer before rendering. Raw HTML in posts is stripped. Do not loosen this.
- **Sections have no index route.** `GET /projects` is not registered. If you need a section landing page, add it as a page: `{ slug = "index", title = "Projects" }` and link to `/projects/index`.
- **Dev mode is for templates only.** Config and routes are still read once at startup. Changing `ntx.toml` requires a restart regardless of `dev`.
- **Session secret and password hash are required.** `config.Load()` returns an error if either is missing or the secret is under 32 chars. Do not add fallback defaults for these.

## Dependencies worth knowing

| Package | Role |
|---|---|
| `github.com/go-chi/chi/v5` | HTTP router |
| `github.com/spf13/viper` | Config loading (TOML + env) |
| `github.com/go-viper/mapstructure/v2` | Config struct decoding (supports `,remain`) |
| `github.com/yuin/goldmark` | Markdown rendering |
| `modernc.org/sqlite` | SQLite (pure Go, no CGo) |
| `golang.org/x/crypto` | bcrypt |
| `github.com/spf13/cobra` | CLI commands |
