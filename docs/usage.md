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
| `dev` | cookies without `Secure`; sign-in URLs use the request's scheme and port |
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

The request log never shows invite tokens (`/invite/[redacted]`) or the
provider's code (`/auth/callback` and `/auth/start` are logged without their
query). An invite is accepted only through the invite page's form, never from
a token in a link to `/auth/start`.

Sign-in failures are rate-limited per client IP (5 per 15 minutes, shared across
all hostnames). Successful sign-ins don't count, and behind a reverse proxy the
client IP comes from `X-Forwarded-For` only when the proxy is in
`trusted_proxies`, so one attacker can't lock root out for everyone.

The platform views (**sites**) list every site and create, edit and delete
them. Deleting a site asks you to type its key and permanently removes its
posts, tags, sections and sessions. Changing a site's hostname signs everyone
out of it.

## People and roles

Everyone except root signs in through a site's provider, and only if they hold
a membership on that site. A Google or Auth0 login on its own grants nothing.

| Role | Where | Can |
|---|---|---|
| root | everywhere | everything; password in config, can't be changed or deleted from the web |
| platform admin | the site they sign in on, plus the platform views | everything root can, except change root |
| site admin | one site | posts (anyone's), members, settings |
| contributor | one site | their own posts; publishing only with **can publish** |

- **Contributors without can publish** write drafts and edit or delete their own
  drafts, but can't publish. Once a site admin has published one of their posts,
  they can still edit it, but their changes wait for review (see below). They
  can't change its slug, unpublish it or delete it.
- **Contributors with can publish** publish, unpublish, edit and delete their own posts.
- **Contributors** see only their own posts in the admin.
- **Platform admins** sign in through the provider of a site where they are a member,
  and from there use **sites** and **users** like root. Revoking the flag, removing
  the membership or deleting the person takes effect on their next request.

### Pending changes

When a contributor without **can publish** saves an edit to a post that has
been published, the post doesn't change. The edit is stored as the post's
**pending changes**, and readers, `ntx generate` and the public tag list don't
see it. Saving again replaces the pending changes; there is only ever one set
per post.

Site admins see a **pending changes** badge on the post in **posts**, and a
**review** link to a page showing the current post and the pending changes side
by side:

- **Publish changes** copies the pending title, body and tags onto the post. It
  doesn't publish or unpublish it: an unpublished post gets the new content and
  stays unpublished.
- **Discard** throws the pending changes away.

If someone edited the post's title, body or tags after the contributor started
editing, the page warns that publishing will overwrite those edits (publishing,
unpublishing or renaming the slug doesn't count), and the button becomes
**publish anyway**. If the contributor saves again while you have the page open,
or someone edits the post again, publishing stops and shows you the newer
version instead, so you never publish over, or publish, text you haven't seen.

### Setting up a site's sign-in provider

Root or a platform admin edits the site under **sites** and picks a provider:

- **Google:** in the Google Cloud console, create an OAuth client ID (type
  "Web application"). Add the callback URL shown on the site's page
  (`https://<hostname>/auth/callback`) as an authorised redirect URI. Enter the
  client ID and secret.
- **Auth0:** create a "Regular Web Application". Add the callback URL to
  *Allowed Callback URLs*. Enter the tenant domain (e.g. `acme.us.auth0.com`),
  the client ID and the client secret.

ntx asks for the `openid email profile` scopes and only accepts emails the
provider has verified. The client secret is encrypted with `encryption_key` and
never shown again; the form says whether one is stored. Changing the provider,
domain or client ID requires entering the secret again. If you change
`encryption_key`, re-enter every site's client secret.

If sign-in fails with "rejected this site's client ID or secret", the provider
answered but refused the credentials: check the client ID and re-enter the
secret. "Couldn't reach the sign-in provider" means it didn't answer usefully
at all. A code the provider refuses (expired, or the callback page reloaded)
just shows "Your sign-in expired" and the person signs in again.

### Members and invites

On a site, **members** lists its people and pending invites. Site admins (and
root and platform admins) can:

- **Add** someone by email: they get access straight away and are matched by
  that email the first time they sign in.
- **Invite** someone: ntx shows a link once, for you to copy and send. It works
  for 7 days, on that site only, and only for someone signing in with the invited
  email. **New link** replaces it; **cancel** withdraws it.
- Change a member's role or **can publish**, or remove them. Their posts stay.

Site admins can't change or remove a platform admin's membership.

### Platform users

**users** (root and platform admins) lists everyone across all sites. A
person's page can grant or revoke platform admin, change their email, add or
remove site memberships, unlink a sign-in identity (their next sign-in matches
by email again), sign them out everywhere, or delete them. Deleted people's
posts stay, shown as by a "former member". Root has no controls there.

A person who already has a login at a provider can't be linked to a second,
different login from the same provider (a reused or reassigned mailbox). They
get "You don't have access to this site" until a platform admin unlinks the old
identity.

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
| about slug | `/about` redirects to `/blog/<about slug>`; a post slug (lowercase letters, digits, single hyphens), `about` if empty |
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

Theme edits show without a restart. Before each page, ntx checks the theme's
`templates/` folder (each `.html` file's name, size and modification time) and
reparses the theme if anything changed. That covers edited, added and deleted
templates, and a deleted theme folder. A template that fails to parse makes
that theme's pages a server error (logged) until the file is fixed.

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

If someone else changes a post's title, body or tags while you have it open
(another editor, or pending changes being published), your save is refused
instead of silently replacing their work. The editor comes back with your
version and nothing saved; saving again from that page replaces their changes
with yours.

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

Set `dev = true` (or `NTX_DEV=true`) to run over plain `http://localhost`: dev
mode drops the `Secure` flag from cookies, and sign-in callback and invite URLs
use the request's scheme and port instead of `https://<hostname>`. Theme edits
show without a restart in either mode (see [Themes](#themes)).
