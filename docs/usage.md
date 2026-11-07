# ntx — usage guide

`ntx` is a self-hosted site server for one or more sites: blog, content sections, admin UI. Sites are created and managed in the browser; their look comes from themes you supply.

## Requirements

- Go 1.21+
- No CGo required (SQLite is pure-Go via `modernc.org/sqlite`)

## Quick start

```bash
# 1. Generate a config: prompts for the root password, generates the secrets
ntx gen-config -p > ntx.toml

# 2. Start the server
ntx serve

# 3. Sign in as root on the platform hostname (localhost by default)
open http://localhost:8080/admin/login
```

Then, in the admin:

1. Under **sites**, choose **new site**. Give it a key (e.g. `mysite`), the
   hostname it answers on (e.g. `mysite.localhost` locally, your domain in
   production) and a theme.
2. Visit `http://<hostname>:8080/admin/login` and sign in as root there to write
   posts and edit that site's settings and sections.

Sessions are bound to the hostname they were created on, so you sign in
separately on the platform hostname and on each site.

## Configuration

`ntx.toml` holds only server settings. Sites, sections, posts and people live in
the database and are managed in the browser.

Config is resolved in this order (later wins):

1. `--config <file>` flag (explicit path)
2. `NTX_*` environment variables (e.g. `NTX_PORT=9000`)
3. `./ntx.toml` in the working directory
4. Built-in defaults

| Setting | Notes |
|---|---|
| `bind_addr` | empty = localhost only; `"0.0.0.0"` for all interfaces |
| `port` | default 8080 |
| `db_dsn` | `sqlite://./ntx.db` (default) or a `postgres://` URL; one database for every site |
| `dev` | re-reads templates on every request; cookies without `Secure` |
| `trusted_proxies` | CIDRs allowed to set `X-Forwarded-For` / `X-Forwarded-Proto` (your reverse proxy); default none |
| `session_secret` | required, min 32 chars |
| `encryption_key` | required, 32 random bytes base64-encoded; encrypts stored secrets |
| `themes_path` | folder of themes, default `./themes` |
| `platform_hostname` | required; bare hostname where root signs in before any site exists. No site may use it |
| `root_password_hash` | required; bcrypt hash of root's password |

`gen-config` writes all of these with comments, generating `session_secret` and
`encryption_key`. `ntx hash-password` prints a hash for `root_password_hash`
(it reads one line from stdin when not on a terminal:
`echo "$PW" | ntx hash-password`).

### Coming from an earlier release

There is no upgrade path. A config that still has `[[site]]`,
`global_admin_password_hash`, `template_path` or `static_path` fails to load,
naming the key. A database created by an earlier release fails to open with a
message saying to delete it: delete `ntx.db` (and any `ntx.db-wal`/`ntx.db-shm`)
and start fresh.

## Root and the platform views

`root` is the one built-in account. Its password lives only in config: it can't
be changed, deleted or locked out from the browser. To change it, put a new
`root_password_hash` in `ntx.toml` and restart; every existing root session ends.

Sign-in failures are rate-limited per client IP (5 per 15 minutes, shared across
all hostnames). Successful sign-ins don't count, and behind a reverse proxy the
client IP comes from `X-Forwarded-For` only when the proxy is in
`trusted_proxies`, so one attacker can't lock root out for everyone.

The platform views (**sites**) list every site and create, edit and delete
them. Deleting a site asks you to type its key and permanently removes its
posts, tags, sections and sessions. Changing a site's hostname signs everyone
out of it.

## Sites

Requests are routed by matching the HTTP `Host` header (case-insensitive, port
ignored) against each site's hostname. An unmatched host gets a plain 404,
indistinguishable from a missing page. Behind a reverse proxy, forward the
original host (`proxy_set_header Host $host;`), or nothing will resolve.

A site has:

| Field | Notes |
|---|---|
| key | lowercase letters, digits, `-`, `_`; set once at creation; names its `ntx generate` output folder |
| hostname | bare hostname (no scheme, port or path), unique |
| name, description | template variables |
| about slug | `/about` redirects to `/blog/<about slug>` |
| theme | one of the folders in `themes_path` |

Posts and tags are scoped per site, so two sites may use the same post slug or
tag name independently.

## Themes

A theme is a folder under `themes_path`:

```
themes/
  default/
    templates/
      base.html              # layout: nav, footer, <head>
      home.html              # /
      blog/
        index.html           # /blog  (tag filter, post list)
        post.html            # /blog/<slug>
      sections/              # content sections (see below)
        projects/
          one/
            index.html       # /projects/one
    static/                  # served at /static/
      css/main.css
      assets/logo.svg
```

Theme names follow the same rule as site keys. The list in the site form shows
every folder under `themes_path` that has a `templates/` folder; the browser
never supplies a path. Sites on the same theme share one parsed template cache.

Admin pages (sign-in, posts, settings, sites) are built into the binary and are
not themed, so they work even if a site's theme is missing or broken.

### Template data

Every theme template receives:

| Field | Source | Example |
|---|---|---|
| `.SiteName` | the resolved site's name | `"nerdtx"` |
| `.SiteURL` | the resolved site's hostname | `"nerdtx.net"` |
| `.SiteDescription` | the resolved site's description | `"technical projects from deep in texas"` |

Page-specific fields are merged in alongside these: `/blog` gets `.Posts`,
`.AllTags` and `.ActiveTag`; `/blog/<slug>` gets `.Post` and `.Rendered`.
`.AllTags` lists only tags on published posts.

### Replacing the logo

The shipped themes reference `/static/assets/logo.svg`. Drop your own SVG into
the theme's `static/assets/`, or change the `src` in `base.html` and `home.html`.

## Blog posts

Posts are written in Markdown (CommonMark + GitHub Flavored Markdown). Raw HTML
in post bodies is stripped for safety. Sign in on a site's hostname and use
**posts**; each site's admin shows only its own posts.

## Content sections

Sections are URL namespaces backed by theme templates. Each section maps a slug
to a set of named pages; there is no index route. Edit a site's sections under
**settings**, as a JSON list:

```json
[
  {
    "slug": "projects",
    "title": "Projects",
    "vars": {"banner": "hero.png"},
    "pages": [
      {"slug": "one", "title": "Project One", "vars": {"year": "2024"}},
      {"slug": "two", "title": "Project Two"}
    ]
  }
]
```

This serves, on that site's hostname:
- `GET /projects/one` → the theme's `templates/sections/projects/one/index.html`
- `GET /projects/two` → `templates/sections/projects/two/index.html`

Section and page slugs must each be a single path segment (letters, digits,
`.`, `-`, `_`). The section slugs `blog`, `static`, `admin`, `about`, `auth` and
`invite` are reserved for built-in routes. A save with invalid JSON or an invalid
slug is rejected and changes nothing. A page whose template is missing from the
theme 404s.

Section pages receive:

| Field | Source |
|---|---|
| `.Title` | page `title` |
| `.SectionTitle` | section `title` |
| any `vars` key | section `vars`, then page `vars` (page wins on conflict) |

## Static site generation

`ntx generate` renders all public pages to disk as plain HTML files and copies static assets. The output can be served by any static host (nginx, S3, Cloudflare Pages, etc.).

```bash
ntx generate                        # writes to ./out
ntx generate --out /var/www/html    # custom output dir
ntx generate --config prod.toml --out ./dist
```

### What gets generated

Every site in the database is generated, in key order, each into its own
subdirectory named after its key:

| URL | Output file |
|---|---|
| `/` | `out/<key>/index.html` |
| `/blog` | `out/<key>/blog/index.html` |
| `/blog/<slug>` | `out/<key>/blog/<slug>/index.html` |
| `/<section>/<page>` | `out/<key>/<section>/<page>/index.html` |
| `static/**` | `out/<key>/static/**` (copied verbatim) |

So a site with key `mysite` gets its deployable tree at `out/mysite/`, and
each site is published independently. Only published posts, and only their
tags, are included. `static/` comes from the site's theme.

Admin routes are not generated. The `/about` redirect belongs in your web server config, not the static output.

Section pages whose templates don't exist are warned and skipped — the rest of the build continues.

### Blog tag filtering

Tag filter links (`?tag=X`) in the blog index won't work in a static deploy without JavaScript or pre-generating one page per tag. For now, `generate` produces only the unfiltered blog index. If you need per-tag pages, either handle them client-side or avoid tag filter links in your static template.

### Static nginx config

```nginx
server {
    listen 443 ssl;
    server_name mysite.com;

    root /var/www/out/mysite;   # one site's generated tree
    index index.html;

    location /about {
        return 302 /blog/about-slug;
    }

    location / {
        try_files $uri $uri/ =404;
    }
}
```

## Deployment

### Minimal systemd unit

```ini
[Unit]
Description=ntx site server
After=network.target

[Service]
ExecStart=/usr/local/bin/ntx serve --config /etc/ntx/ntx.toml
Restart=on-failure
User=www-data

[Install]
WantedBy=multi-user.target
```

### Reverse proxy (nginx)

`proxy_set_header Host $host;` is not optional: ntx picks the site from the
`Host` header, so a proxy that rewrites it will make every request 404. Also
forward the client address and scheme, and list the proxy's address in
`trusted_proxies` (the default covers a proxy on the same machine). Add one
`server` block per site, plus one for `platform_hostname`, all proxying to the
same ntx port:

```nginx
server {
    listen 443 ssl;
    server_name mysite.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

server {
    listen 443 ssl;
    server_name admin.example.com;   # platform_hostname

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### File locations (recommended)

```
/usr/local/bin/ntx          # binary
/etc/ntx/ntx.toml           # config (mode 600, owned by service user)
/var/lib/ntx/ntx.db         # database — shared by every site
/var/lib/ntx/themes/        # themes        → themes_path
```

## Dev mode

Set `dev = true` (or `NTX_DEV=true`) to reload theme templates from disk on every
request, so no restart is needed when editing HTML. Dev mode also drops the
`Secure` flag from cookies, so sign-in works over plain `http://localhost`.
