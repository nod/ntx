# ntx — AI context reference

This document describes the ntx codebase for AI assistants. It covers architecture, conventions, data flow, and the constraints that govern extension work. Read this before touching any file.

## What ntx is

A self-hosted personal site server written in Go. It is not a framework or a CMS — it is a single binary that serves a blog, content sections, and an admin UI, configured entirely via `ntx.toml` and HTML templates you supply. The site that runs on nerdtx.net is one deployment of ntx.

## Module and Go version

```
module github.com/nerdtx/ntx
go 1.26.4
```

## Directory layout

```
main.go                        # CLI entrypoint (cobra)
ntx.toml                       # local dev config (not committed in production)
internal/
  config/config.go             # Config struct, viper load, validation
  db/db.go                     # SQLite init and migrations
  db/migrations/               # .sql migration files
  auth/auth.go                 # bcrypt password check, session management
  models/post.go               # PostStore: CRUD for blog posts
  handlers/handler.go          # TemplateCache, Handler struct, render()
  handlers/home.go             # GET /
  handlers/blog.go             # GET /blog, GET /blog/{slug}
  handlers/projects.go         # GET /{section}/{page} (content sections)
  handlers/admin.go            # /admin/* routes (auth-gated)
  server/server.go             # chi router wiring
templates/                     # HTML templates (user-supplied)
static/                        # CSS, images, JS (user-supplied)
samples/                       # example site configurations
docs/                          # documentation
```

## Config (`internal/config/config.go`)

Loaded by viper from `ntx.toml`, then env vars (`NTX_*`), then defaults.

```go
type Config struct {
    StaticPath        string    // static_path
    DBPath            string    // db_path
    Port              int       // port (default 8080)
    TemplatePath      string    // template_path (default ./templates)
    AboutSlug         string    // about_slug — /about redirects to /blog/<slug>
    AdminPasswordHash string    // required: bcrypt hash
    SessionSecret     string    // required: min 32 chars
    Dev               bool      // dev mode: re-parse templates on every request
    SiteName          string    // site_name
    SiteURL           string    // site_url
    SiteDescription   string    // site_description
    Sections          []Section // [[section]] blocks
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

Validation in `Load()` rejects missing `session_secret` (< 32 chars) and missing `admin_password_hash`.

## Template system (`internal/handlers/handler.go`)

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

### siteData()

Returns:
```go
map[string]interface{}{
    "SiteName":        h.cfg.SiteName,
    "SiteURL":         h.cfg.SiteURL,
    "SiteDescription": h.cfg.SiteDescription,
}
```

Always available in every template.

## Routing (`internal/server/server.go`)

Uses `github.com/go-chi/chi/v5`. Routes registered at startup:

| Method | Pattern | Handler |
|---|---|---|
| GET | `/` | `h.Home` |
| GET | `/about` | redirect → `/blog/<about_slug>` |
| GET | `/blog` | `h.BlogIndex` |
| GET | `/blog/{slug}` | `h.BlogPost` |
| GET | `/{section}/{page}` | `h.SectionPage` (per `[[section]]` in config) |
| GET | `/static/*` | file server from `static_path` |
| GET | `/admin/login` | `h.AdminLogin` |
| POST | `/admin/login` | `h.AdminLoginPost` |
| POST | `/admin/logout` | `h.AdminLogout` (auth) |
| GET | `/admin/posts` | `h.AdminPosts` (auth) |
| GET | `/admin/posts/new` | `h.AdminNewPost` (auth) |
| POST | `/admin/posts` | `h.AdminCreatePost` (auth) |
| GET | `/admin/posts/{id}/edit` | `h.AdminEditPost` (auth) |
| POST | `/admin/posts/{id}` | `h.AdminUpdatePost` (auth) |

Section routes are registered dynamically at startup by iterating `cfg.Sections`. Each section gets its own route `/{sec.Slug}/{page}` as a closure capturing the section value. Unknown sections are not routed — they 404 at the chi level automatically.

## Template data by route

### All routes

| Key | Type | Value |
|---|---|---|
| `.SiteName` | string | `site_name` config |
| `.SiteURL` | string | `site_url` config |
| `.SiteDescription` | string | `site_description` config |

### `/blog`

| Key | Type | Value |
|---|---|---|
| `.Posts` | `[]models.Post` | published posts, newest first |
| `.Tag` | string | active tag filter (empty if none) |
| `.Tags` | `[]string` | all tags across all posts |

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

`PostStore` wraps a `*sql.DB`. Key methods: `List()`, `GetBySlug()`, `Create()`, `Update()`.

## Auth (`internal/auth/auth.go`)

Session-based. `SessionManager` stores session tokens in SQLite with expiry. The `Middleware` method is a chi middleware that 401s or redirects unauthenticated requests. Password check uses `bcrypt.CompareHashAndPassword`.

## Template file conventions

- `base.html` must exist at the root of `template_path`. Every page template is parsed with it.
- Section pages live at `templates/sections/{section-slug}/{page-slug}/index.html`.
- Blog templates live at `templates/blog/index.html` and `templates/blog/post.html`.
- Admin templates live at `templates/admin/`.
- `home.html` is at the root of `template_path`.

Templates use standard Go `html/template`. One template function is registered: `join` (wraps `strings.Join`).

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
