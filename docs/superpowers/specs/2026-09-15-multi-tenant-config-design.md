# Multi-tenant config design

## Goal

ntx currently runs as one process serving exactly one site: one `ntx.toml`,
one DB DSN, one admin account, one set of templates/static assets. This spec
makes a single ntx process serve multiple independent websites ("tenants"),
each defined as a block in one config file, routed by HTTP Host header.

## Non-goals

- No per-site database connections (one shared DB, isolated by a `site_id`
  column — see Data model).
- No multi-process / multi-binary deployment model.
- No dynamic site management (adding/removing a site still requires editing
  `ntx.toml` and restarting the process).
- No change to the static-site `generate` command's per-page rendering logic
  beyond looping it per site.

## Config schema

Global (process-wide) fields, unchanged unless noted:

```toml
bind_addr    = ""              # empty = localhost only
port         = 8080
db_dsn       = "sqlite://./ntx.db"
session_secret = "..."         # required, min 32 chars — signs all sessions
dev          = false
template_path = "./templates"  # shared fallback, used when a site omits its own
static_path   = "./static"     # shared fallback, used when a site omits its own
global_admin_password_hash = "" # optional bcrypt hash; grants cross-site admin access
```

Per-site fields, one `[[site]]` block per tenant:

```toml
[[site]]
key                 = "mysite"              # stable identifier, decoupled from hostname
site_url            = "mysite.example.com"  # routing key: matched against request Host
site_name           = "My Site"
site_description    = ""
about_slug          = "about"
admin_password_hash = ""                    # required: bcrypt hash, this site's admin
template_path       = ""                    # optional override of the global default
static_path         = ""                    # optional override of the global default

  [[site.section]]
  slug  = "projects"
  title = "Projects"

    [[site.section.pages]]
    slug  = "ntx"
    title = "ntx"
```

`SectionPage`/`Section` structs are unchanged, just nested one level deeper
under `site` instead of at the config root.

### Validation (at load time)

- At least one `[[site]]` block is required.
- Each site requires `key`, `site_url`, `admin_password_hash`.
- `key` and `site_url` must be unique across sites — duplicates are a load
  error, not a runtime surprise.
- `session_secret` validation (min 32 chars) is unchanged, still global.
- `global_admin_password_hash`, if set, is bcrypt-validated the same way as
  a site's `admin_password_hash` (non-empty, well-formed hash) but is
  otherwise optional.

## Routing

`server.New` gains a `resolveSite` middleware that runs before route
dispatch:

1. Take `r.Host`, strip a trailing `:port` if present.
2. Exact-match against the configured sites' `site_url`.
3. On match, store the resolved `*config.Site` on the request context.
4. On no match, respond 404 immediately (don't leak which hostnames are
   configured via a different error).

All existing handlers (`Home`, `BlogIndex`, `BlogPost`, `SectionPage`,
`AdminLogin`, etc.) read the site from context instead of a single `cfg`
field passed into `Handler`. Section routes (`/{sec.slug}/{page}`) are
registered once per unique section *slug* across all sites (chi routes
don't vary per Host), and the handler looks up which section applies for
the resolved site at request time — this replaces the current
per-config-section closure registration in `server.go`.

`TemplateCache` and static file serving become per-site: `Handler` holds a
`map[siteKey]*TemplateCache` and resolves `template_path`/`static_path` with
the site's override if set, else the global default, at boot (not per
request) — one cache/handler pair built per site at startup.

## Data model

New `sites` table, seeded (upserted by `key`) from config at boot — config
remains the source of truth; the DB copy exists so `posts`/`tags` have a
stable FK target and so site display fields could later be edited without a
restart (not implemented in this pass). After the boot-time upsert, each
in-memory `config.Site` is annotated with the DB-assigned `ID` (int64), which
is what `PostStore.ForSite` and the session `site_id` claim actually use —
config's `key` is never itself the FK, avoiding a schema change if a site's
`key` is ever renamed.

```sql
CREATE TABLE sites (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    key      TEXT NOT NULL UNIQUE,
    hostname TEXT NOT NULL UNIQUE
);
```

`posts` and `tags` gain `site_id INTEGER NOT NULL REFERENCES sites(id)`.
Uniqueness constraints change from global to per-site:

- `posts.slug`: unique on `(site_id, slug)`, not globally unique.
- `tags.name`: unique on `(site_id, name)`, not globally unique.

### PostStore API

`PostStore` becomes site-scoped via `ForSite(siteID) *PostStore`, returning
a store narrowed to that site; all existing methods (`List`, `ListByTag`,
`GetBySlug`, `GetByID`, `Create`, `Update`) keep their current signatures —
only the constructor path changes, not each call site. `Handler` calls
`posts.ForSite(site.ID)` once when handling a request (or holds one scoped
store per site, built at startup alongside the per-site template cache).

### Migration / upgrade path

Existing single-site installs have unscoped `posts`/`tags` rows. The
migration:

1. Creates `sites` and inserts one row from a required
   `--default-site-key` flag passed to a one-time `ntx migrate-tenant`
   command (or equivalent goose migration input) — this is the identifier
   existing rows get assigned to.
2. Adds `site_id` columns as nullable, backfills them to the default site's
   id, then adds the `NOT NULL` constraint and the new unique indexes.

This is a breaking change for existing deployments: upgrading requires
converting `ntx.toml`'s root-level fields into one `[[site]]` block before
first boot on the new version, and running the backfill migration.

## Admin auth

Session tokens gain a `site_id` claim; a distinguished value (e.g. `0` /
absent) marks a **global** session. `SessionManager` and its cookie payload
carry this claim; `session.Middleware` becomes site-aware.

Login flow at `/admin/login` (unchanged URL, now resolved per-site by Host):

1. Check the submitted password against the resolved site's
   `admin_password_hash`. On match, issue a session scoped to that
   `site_id`.
2. Else, if `global_admin_password_hash` is set, check against it. On
   match, issue a global session.
3. Else, reject.

Authorization check in `session.Middleware`: a global session passes for
any site; a site-scoped session passes only when its `site_id` matches the
resolved request site's id. A session scoped to site A hitting site B's
`/admin/*` gets treated as unauthenticated (redirect to that site's login),
not a 403 — avoids confirming site A's session exists to site B.

`loginLimiter` keys attempts by `(site_id, IP)` instead of bare IP, so
brute-force lockout on one site doesn't collaterally lock out login
attempts on another site sharing the same process.

## `generate` command

Currently renders one static site into `--out`. Becomes: for each
configured site, resolve its `Handler` (per-site template cache + scoped
PostStore) and render into `<out>/<site.key>/`. No change to per-page
rendering logic — only the outer loop and output path change.

## Error handling summary

- Unmatched Host → 404, no detail on configured hostnames.
- Config load: missing required per-site fields, duplicate `key`/`site_url`,
  or a config with zero `[[site]]` blocks are load-time errors that prevent
  startup (matches current fail-fast behavior for `session_secret`).
- Cross-site session use → treated as unauthenticated, not 403.

## Testing

- **Config**: multi-site TOML parses correctly; missing required per-site
  field errors; duplicate `key`/`site_url` errors; template/static fallback
  resolution (site override present vs. absent).
- **Routing**: Host→Site resolution — exact match, port-stripped match,
  unmatched host → 404; two sites in one server answering correctly on
  their respective Hosts (integration-style test using `httptest`).
- **PostStore**: slug uniqueness is per-site (same slug valid in two sites,
  invalid twice in one); `ForSite` isolation (site A's store never returns
  site B's posts).
- **Admin auth**: site-scoped session accepted on its own site, rejected
  (as unauthenticated) on another site; global session accepted everywhere;
  login-limiter lockout is scoped per `(site_id, IP)`.
- **Generate**: multi-site output lands in separate `<out>/<key>/`
  directories with no cross-contamination.
