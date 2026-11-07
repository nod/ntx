# Roles & SSO, phase 1: sites in the database, root, platform views, themes. Implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sites, their sections and sessions live in the database. Root signs in with a password from config and manages sites in the browser. Site templates come from a themes folder. Admin pages are embedded in the binary.

**Architecture:**
- A fresh goose schema (`100_initial.sql`) holds every table the full design needs, and ntx refuses to start on pre-100 databases.
- An in-memory `sitereg.Registry` maps hostnames to sites and reloads after every site write.
- `auth.Manager` issues DB-backed sessions bound to one host. Phase 1 only accepts root sessions; phase 2 adds members.
- Admin and platform views render from an embedded `adminui` package. Public pages render from the resolved site's theme.

**Tech Stack:** Go 1.26, chi v5, GORM (glebarez/sqlite, postgres), goose v3, viper, `net/http.CrossOriginProtection`, `embed`.

**Spec:** `docs/superpowers/specs/2026-11-07-roles-and-sso-design.md`. Phase 1 is "Build order" item 1. Everything listed there is in scope; providers, members, invites and revisions are not.

## Global Constraints

- No upgrade path and no migrations from the current schema. One `100_initial.sql` per dialect replaces `001`–`003`.
- On startup, a database with any goose version below 100 fails with a message saying to delete it.
- Config keys: `bind_addr`, `port` (default 8080), `db_dsn` (default `sqlite://./ntx.db`), `dev`, `trusted_proxies` (list of CIDRs, default empty), `session_secret` (≥32 chars), `encryption_key` (base64, exactly 32 bytes decoded), `themes_path` (default `./themes`), `platform_hostname` (required, bare host), `root_password_hash` (required, valid bcrypt).
- `config.Load` rejects these keys, naming each one and pointing to `docs/usage.md`: `site`, `global_admin_password_hash`, `template_path`, `static_path`.
- Site `key` and theme names match `^[a-z0-9][a-z0-9_-]*$`. Section and page slugs match `^[A-Za-z0-9][A-Za-z0-9._-]*$`. Reserved section slugs are `blog`, `static`, `admin`, `about`, `auth` and `invite`.
- A hostname is stored lowercase and must be a bare host: no scheme, port, path or whitespace. It must differ from `platform_hostname` and be unique.
- Root login:
  - username `root` only;
  - failures only count, 5 per 15 minutes per client IP, shared across all hosts;
  - a root session stores `sha256(root_password_hash)` (hex) and is invalid once that no longer matches the config.
- Session cookie `ntx_session`:
  - a random 32-byte token (base64url), HttpOnly, `SameSite=Strict`, `Secure` unless `dev`, `Path=/`, no `Domain`;
  - the DB stores the token's sha256 (hex);
  - 24 h expiry;
  - `last_seen_at` is written at most once a minute.
- Every route is wrapped in `http.NewCrossOriginProtection().Handler(...)`.
- The CSP stays `default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:`. Admin JS ships as a file under `/admin/assets/`; no inline scripts.
- Public pages and `generate` show only published posts, and only tags attached to at least one published post.
- On the platform host, only `/admin/login`, `/admin/logout`, `/admin/platform/...` and `/admin/assets/...` respond. Everything else returns 404.
- Commit after each task. Run `go vet ./... && go test ./...` before each commit.

## Review Focus

1. **Host normalisation.** A `Host` with a port or uppercase letters (`Example.COM:8443`) resolves to its site, and the platform hostname gets the same treatment. Tested in Task 11 (`TestHostResolutionNormalises`).
2. **A theme removed from disk.** If a site's theme folder disappears after the site was created, the public pages return 500 with a log line, while `/admin/*` (embedded) still works so root can pick another theme. Tested in Task 11 (`TestMissingThemeKeepsAdminWorking`).
3. **Malformed sections JSON.** Bad sections JSON, or `vars` that aren't an object, re-shows the settings form with the error and leaves the stored sections unchanged; it never returns 500. Tested in Task 10 (`TestSettingsRejectsBadSectionsJSON`).
4. **Editing a hostname into a conflict.** Changing a hostname to another site's hostname, or to the platform hostname, shows a form error and changes nothing. Tested in Task 3 (`TestUpdateRejectsHostnameConflicts`) and Task 10 (`TestPlatformEditShowsConflictError`).
5. **A session used on the wrong host.** A root session minted on site A does not authenticate on site B or on the platform host, and vice versa. Tested in Task 11 (`TestSessionBoundToIssuingHost`).

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/validate/validate.go` (new) | Name, hostname and slug rules shared by config, models and themes |
| `internal/config/config.go` (rewrite) | Server-only config; rejects legacy keys |
| `internal/db/db.go`, `internal/db/migrations/{sqlite,postgres}/100_initial.sql` | The fresh schema, legacy refusal, and the DSN foreign-key pragma |
| `internal/models/site.go` (rewrite) | `SiteStore`: sites and sections CRUD with validation |
| `internal/models/user.go`, `internal/models/session.go` (new) | Root bootstrap; DB sessions |
| `internal/models/post.go` | `first_published_at`; published-only `AllTags` |
| `internal/clientip/clientip.go` (new) | Real client IP and scheme behind trusted proxies |
| `internal/auth/` (rewrite) | Passwords, failure-only limiter, `Manager`, `Actor` |
| `internal/themes/themes.go` (new) | Listing and resolving theme folders safely |
| `internal/adminui/` (new) | Embedded admin templates and assets |
| `internal/sitereg/registry.go` (new) | The hostname → site map |
| `internal/sitectx/sitectx.go` | Carries `*models.Site` and the platform-host flag |
| `internal/handlers/` (rewrite) | Public, admin, settings, platform and generate handlers |
| `internal/server/server.go` (rewrite) | Router, host resolution, CSRF and middleware order |
| `main.go` | Wiring; new `gen-config` sample |
| `themes/{default,blog,nerdtx}/{templates,static}` | Moved from `templates/`, `static/` and `samples/*` |
| Docs | `README.md`, `docs/usage.md`, `docs/ai-context.md`, `docs/ntx.sample.toml` |

Deleted:
- `internal/db/migrations/*/00{1,2,3}_*.sql`
- `templates/`, `static/` and `samples/` (moved into `themes/`, except `samples/*/ntx.toml` and `samples/nerdtx/ntx.db`, which are deleted)
- `internal/handlers/projects.go` (replaced by `section.go`)
- every theme's `admin/` folder

---

### Task 1: Validation rules and the new config

**Files:**
- Create: `internal/validate/validate.go`, `internal/validate/validate_test.go`
- Rewrite: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `main.go` (only `sampleConfig`, `applyGenConfigSubstitutions` and the `gen-config` command), `main_test.go`

**Interfaces:**
- Produces:
  - `validate.Key(s string) error`
  - `validate.Hostname(s string) error`
  - `validate.Segment(s string) error`
  - `validate.SectionSlug(s string) error`
  - `validate.ReservedSectionSlugs` (exported `map[string]bool`)
  - `config.Config{BindAddr string; Port int; DBDSN string; Dev bool; TrustedProxies []netip.Prefix; SessionSecret string; EncryptionKey []byte; ThemesPath string; PlatformHostname string; RootPasswordHash string}`
  - `config.Load(cfgFile string) (*Config, error)`

Moving the rules into `validate` means they keep their current semantics, so there is no RED step for `Key` and `Hostname`.

- [ ] **Step 1: Port the rules into `validate`.** Move today's `checkSiteKey`, `checkSiteURL`, `CheckSectionSlug` and `reservedSectionSlugs` from `internal/config/config.go` into `validate`, renamed `Key`, `Hostname` and `SectionSlug`. Split the single-segment regex out as `Segment`. Add `auth` and `invite` to the reserved slugs.
- [ ] **Step 2: Write `validate_test.go`.** Port the table cases from today's `TestSiteKeyMustBeSafeDirName`, `TestSiteKeySafeNamesAccepted`, `TestSiteURLMustBeBareHostname`, `TestSiteURLBareHostnamesAccepted`, `TestReservedSectionSlugErrors` and `TestMalformedSectionSlugErrors`, and add:
  - `TestReservedIncludesAuthAndInvite`: `SectionSlug("auth")` and `SectionSlug("invite")` return errors.
  - `TestSegmentRejectsTraversal`: `Segment` rejects `".."`, `"../x"`, `"a/b"`, `""`, `"{page}"`, and accepts `"one"` and `"v1.2"`.
- [ ] **Step 3: Run the tests.** `go test ./internal/validate/` → PASS. `TestReservedIncludesAuthAndInvite` fails if Step 1 left out the new slugs.
- [ ] **Step 4: Write the failing config tests** in a rewritten `config_test.go`. A helper `base()` returns a valid TOML string containing every required key:
  - session secret: `supersecretkey1234567890abcdef12`
  - encryption key: `base64.StdEncoding.EncodeToString(make([]byte, 32))`
  - `platform_hostname = "localhost"`
  - root hash: the existing `validHash`

  Tests:
  - `TestLoadValidConfig`: the defaults are `Port == 8080`, `ThemesPath == "./themes"`, `DBDSN == "sqlite://./ntx.db"`, and `TrustedProxies` is empty.
  - `TestPlatformHostnameRequiredAndValidated`: missing → error; `"https://x.com"` → error; `"Admin.Example.com"` → loads as `"admin.example.com"`.
  - `TestRootPasswordHashRequiredAndBcrypt`: missing → error; `"notahash"` → error.
  - `TestEncryptionKeyMustBe32Bytes`: missing, non-base64, and 16-byte values each → error.
  - `TestTrustedProxiesParsed`: `trusted_proxies = ["127.0.0.1/32", "::1/128"]` → two prefixes; `["nope"]` → error.
  - `TestLegacyKeysRejected`: for each of `[[site]]` (a block with a key), `global_admin_password_hash = "x"`, `template_path = "./t"` and `static_path = "./s"`, `Load` errors and the message contains both the key name and `docs/usage.md`.
  - `TestShortSessionSecretErrors`: kept from today.
  - `TestEnvVarOverride`: kept from today, overriding `NTX_PORT`.
- [ ] **Step 5: Confirm RED.** `go test ./internal/config/` → FAIL (compile errors on the new fields).
- [ ] **Step 6: Rewrite `config.Load`** with the constraints from Global Constraints.
  - `trusted_proxies` unmarshals as `[]string`, then goes through `netip.ParsePrefix`.
  - Legacy keys are detected with `v.IsSet(key)` before unmarshalling.
  - Keep `checkBcryptHash`.
- [ ] **Step 7: Rewrite the `gen-config` sample** in `main.go` to the spec's TOML block (with comments), including `trusted_proxies = ["127.0.0.1/32", "::1/128"]`.
  - `applyGenConfigSubstitutions(sample, sessionSecret, encryptionKey, rootHash string) string` fills `session_secret = ""` and `encryption_key = ""`, plus `root_password_hash = ""` when a hash is given.
  - `gen-config` always generates both secrets. `-p` prompts for the root password.
  - Rewrite `main_test.go`:
    - `TestGenConfigOutputLoads`: a hash is given; write the output to a temp file; `config.Load` succeeds.
    - `TestGenConfigWithoutPasswordFailsLoadOnRootHash`: no hash; `Load`'s error mentions `root_password_hash`.
    - The two `readPasswordLine` tests stay.
    - Delete the two global-hash substitution tests; that key no longer exists.
- [ ] **Step 8: Run the tests.** `go test ./internal/validate/ ./internal/config/` → PASS. Because this is a rewrite of shared types, packages that depend on the old ones (`sitectx`, `handlers`, `server`, `main`) won't compile again until the task that rewrites them. `go build ./...` is required to be green from Task 11 onwards. Before that, each task's own packages must build and pass their tests. Don't stub or comment out code to force an early green build.
- [ ] **Step 9: Commit:** `Move validation into its own package and reshape config for web-managed sites`

### Task 2: Fresh schema, legacy refusal, foreign keys

**Files:**
- Delete: `internal/db/migrations/sqlite/00{1,2,3}_*.sql`, `internal/db/migrations/postgres/00{1,2,3}_*.sql`
- Create: `internal/db/migrations/sqlite/100_initial.sql`, `internal/db/migrations/postgres/100_initial.sql`
- Modify: `internal/db/db.go`
- Rewrite: `internal/db/db_test.go`

**Interfaces:**
- Produces:
  - `db.Open(dsn string) (*gorm.DB, error)` (unchanged signature)
  - `db.ErrLegacySchema` (error)
  - the tables from the spec's "Data model" section, with exactly those column names

- [ ] **Step 1: Write the failing tests.**
  - `TestFreshSchemaHasAllTables`: `Open("sqlite://:memory:")`. Query `sqlite_master`. The tables `users`, `identities`, `sites`, `memberships`, `invites`, `sessions`, `posts`, `post_revisions`, `tags`, `post_tags`, `sections` and `section_pages` all exist.
  - `TestRefusesLegacyDatabase`: create a file DB in `t.TempDir()`. Using `database/sql` with the glebarez driver, create `goose_db_version(id INTEGER PRIMARY KEY, version_id INTEGER, is_applied INTEGER, tstamp TEXT)` and insert `version_id` 3. Then `Open` returns an error that `errors.Is(err, db.ErrLegacySchema)` and whose message contains `delete`.
  - `TestDeletingSiteCascades`: insert a site, a post on it, and a session row on it, all with raw SQL. Delete the site. The post and session counts are 0. This proves foreign keys are on.
  - `TestOnlyOneRootUser`: inserting a second `users` row with `is_root = 1` fails.
  - `TestOnePendingInvitePerSiteEmail`: two `invites` rows for the same `(site_id, email)` with `used_at` NULL fails; with one `used_at` set, it succeeds.
- [ ] **Step 2: Confirm RED.** `go test ./internal/db/` → FAIL.
- [ ] **Step 3: Write both `100_initial.sql` files** from the spec's table list.
  - SQLite: booleans are `INTEGER NOT NULL DEFAULT 0`, times are `DATETIME`, JSON is `TEXT`.
  - Postgres: `BOOLEAN`, `TIMESTAMPTZ`, `JSONB`, `BIGSERIAL`.
  - Indexes:
    - `CREATE UNIQUE INDEX users_one_root ON users(is_root) WHERE is_root` (SQLite: `WHERE is_root = 1`)
    - `CREATE UNIQUE INDEX invites_one_pending ON invites(site_id, email) WHERE used_at IS NULL`
    - unique `(site_id, slug)` on posts, `(site_id, name)` on tags, `(site_id, slug)` on sections, and `(section_id, slug)` on section_pages
  - `sections` and `section_pages` each carry `position INTEGER NOT NULL`.
  - `-- +goose Down` drops everything.
- [ ] **Step 4: Change `db.Open`.**
  - Before running goose: if `goose_db_version` exists and has any `version_id` between 1 and 99, return `fmt.Errorf("%w: this database predates ntx's web-managed sites; delete it (and its -wal/-shm files) and start again", ErrLegacySchema)`.
  - SQLite opens `path + "?_pragma=foreign_keys(1)"`, or `file::memory:?_pragma=foreign_keys(1)` for `:memory:`. Drop the separate `PRAGMA foreign_keys` exec.
  - Keep WAL and `SetMaxOpenConns(1)`.
- [ ] **Step 5: Run the tests.** `go test ./internal/db/` → PASS.
- [ ] **Step 6: Commit:** `Replace the schema with a fresh 100_initial and refuse older databases`

### Task 3: SiteStore (sites and sections)

**Files:**
- Rewrite: `internal/models/site.go`, `internal/models/site_test.go`

**Interfaces:**
- Consumes: the `validate.*` rules from Task 1, and the schema from Task 2.
- Produces:
  - `models.Site{ID int64; Key, Hostname, Name, Description, AboutSlug, Theme, ProviderType, ProviderClientID, ProviderDomain string; Sections []Section; CreatedAt, UpdatedAt time.Time}`
  - `models.Section{Slug, Title string; Vars map[string]any; Pages []SectionPage}`
  - `models.SectionPage{Slug, Title string; Vars map[string]any}`
  - `models.ValidationError{Msg string}` (implements `error`)
  - `models.NewSiteStore(db *gorm.DB) *SiteStore`
  - `(*SiteStore).List() ([]Site, error)`: ordered by key, sections and pages loaded and ordered by position
  - `(*SiteStore).Create(s *Site, platformHostname string) error`: sets `s.ID`
  - `(*SiteStore).Update(s *Site, platformHostname string) error`: matched by `ID`; `Key` is ignored, because it's immutable
  - `(*SiteStore).Delete(id int64) error`
  - `(*SiteStore).SetSections(siteID int64, secs []Section) error`

- [ ] **Step 1: Write the failing tests.**
  - `TestCreateAndList`: create two sites, `b` then `a`. `List` returns `a` before `b`, with IDs set, the hostname lowercased, `AboutSlug` defaulted to `"about"`, and `Name` defaulted to the key.
  - `TestCreateValidates`: each of these returns a `*ValidationError` (checked with `errors.As`), and nothing is inserted:
    - a bad key `"../x"`;
    - a bad hostname `"https://x.com"`;
    - a hostname equal to `platformHostname`;
    - a duplicate key;
    - a duplicate hostname differing only in case;
    - a bad theme name `"../t"`.
  - `TestUpdateRejectsHostnameConflicts`: updating site A's hostname to B's, or to the platform hostname, returns a `*ValidationError`. Site A's stored hostname is unchanged.
  - `TestUpdateKeepsKey`: an update that passes a different `Key` leaves the stored key unchanged.
  - `TestHostnameChangeDeletesSessions`: insert a sessions row for the site with raw SQL. Change the hostname; the session is gone. An update that leaves the hostname unchanged keeps the session.
  - `TestSetSectionsValidatesAndReplaces`:
    - `blog` is a reserved slug → `*ValidationError`;
    - a page slug of `"../x"` → `*ValidationError`;
    - duplicate section slugs → `*ValidationError`;
    - each of those leaves the previously set sections intact;
    - a valid set replaces the old one completely and round-trips `Vars` through `List`.
  - `TestDeleteCascadesSections`: deleting a site leaves zero `sections` and `section_pages` rows.
- [ ] **Step 2: Confirm RED.** `go test ./internal/models/ -run 'Site|Section|Hostname|Create|Update|Delete'` → FAIL.
- [ ] **Step 3: Implement.**
  - Check uniqueness with a query before writing; ntx is single-instance.
  - `SetSections` runs in one transaction: delete the old rows, then insert the new ones with `position` = index.
  - `Vars` are stored with `json.Marshal`.
  - Delete `EnsureSites` and `warnOrphanedSites`.
- [ ] **Step 4: Run the tests.** The same command → PASS.
- [ ] **Step 5: Commit:** `Store sites and sections in the database`

### Task 4: Users, sessions, and post publishing fields

**Files:**
- Create: `internal/models/user.go`, `internal/models/session.go`, `internal/models/user_test.go`, `internal/models/session_test.go`
- Modify: `internal/models/post.go`, `internal/models/post_test.go`

**Interfaces:**
- Produces:
  - `models.User{ID int64; Email string; Name string; IsRoot, IsPlatformAdmin bool}`
  - `models.NewUserStore(db) *UserStore`
  - `(*UserStore).EnsureRoot() (*User, error)`
  - `(*UserStore).GetByID(id int64) (*User, error)`: returns `nil, nil` when the user doesn't exist
  - `models.Session{ID, UserID int64; SiteID *int64; RootHashDigest string; ExpiresAt, LastSeenAt time.Time}`
  - `models.NewSessionStore(db) *SessionStore`
  - `(*SessionStore).Create(userID int64, siteID *int64, rootHashDigest string, ttl time.Duration) (token string, err error)`
  - `(*SessionStore).Lookup(token string, now time.Time) (*Session, error)`: returns nil if the session is missing or expired
  - `(*SessionStore).Delete(token string) error`
  - `Post.FirstPublishedAt *time.Time`
  - `(*PostStore).AllTags()` now returns only tags on published posts

- [ ] **Step 1: Write the failing tests.**
  - `TestEnsureRootIdempotent`: two calls return the same ID, and `is_root` is set. The root's `Email` is `""` and is stored as NULL (checked with raw SQL).
  - `TestSessionCreateLookupDelete`:
    - the returned token is 43 characters (base64url of 32 bytes, no padding);
    - the stored `token_hash` isn't the token;
    - `Lookup` before expiry returns the session;
    - `Lookup` at `now + ttl + 1s` returns nil;
    - after `Delete`, `Lookup` returns nil.
  - `TestLookupTouchesAtMostOncePerMinute`: `Lookup` at `t0+30s` leaves `last_seen_at` unchanged; at `t0+61s` it moves.
  - In `post_test.go`, replace the `EnsureSites`-based helper with `NewSiteStore(...).Create`. Then add:
    - `TestFirstPublishedAtSetOnceAndKept`: create a draft and check `FirstPublishedAt` is nil. Publish it with `Update`; it's set. Unpublish and republish; the value is unchanged.
    - `TestAllTagsOnlyFromPublishedPosts`: a draft tagged `secret` and a published post tagged `go`. `AllTags` returns `["go"]`, and `Stats` still counts 2 tags.
- [ ] **Step 2: Confirm RED.** `go test ./internal/models/` → FAIL.
- [ ] **Step 3: Implement.**
  - Tokens come from `crypto/rand` and are encoded with `base64.RawURLEncoding`.
  - Hashes are `hex(sha256)`.
  - `PostStore.Create` and `Update` set `first_published_at = now` when `Published` is true and the column is NULL; they never clear it.
  - `AllTags` adds `JOIN post_tags … JOIN posts … WHERE posts.published = true`, with `DISTINCT`.
- [ ] **Step 4: Run the tests.** `go test ./internal/models/` → PASS.
- [ ] **Step 5: Commit:** `Add root bootstrap, DB sessions, and published-only public tags`

### Task 5: Client IP and scheme behind trusted proxies

**Files:**
- Create: `internal/clientip/clientip.go`, `internal/clientip/clientip_test.go`

**Interfaces:**
- Produces:
  - `clientip.Middleware(trusted []netip.Prefix) func(http.Handler) http.Handler`
  - `clientip.IP(r *http.Request) string`
  - `clientip.Scheme(r *http.Request) string` (`"http"` or `"https"`)

- [ ] **Step 1: Write the failing table test `TestClientIP`.**

  | case | peer | `X-Forwarded-For` | trusted | want IP |
  |---|---|---|---|---|
  | no proxy | `203.0.113.5:1234` | `1.2.3.4` | none | `203.0.113.5` |
  | trusted proxy | `127.0.0.1:9` | `1.2.3.4` | `127.0.0.1/32` | `1.2.3.4` |
  | spoofed prefix | `127.0.0.1:9` | `6.6.6.6, 1.2.3.4` | `127.0.0.1/32` | `1.2.3.4` |
  | proxy chain | `127.0.0.1:9` | `1.2.3.4, 10.0.0.2` | `127.0.0.1/32, 10.0.0.0/8` | `1.2.3.4` |
  | untrusted peer | `198.51.100.7:9` | `1.2.3.4` | `127.0.0.1/32` | `198.51.100.7` |

  Also `TestScheme`:
  - `X-Forwarded-Proto: https` from a trusted peer → `https`;
  - the same header from an untrusted peer → `http`;
  - `X-Forwarded-Proto: gopher` from a trusted peer → `http`.
- [ ] **Step 2: Confirm RED.** `go test ./internal/clientip/` → FAIL.
- [ ] **Step 3: Implement.** Walk the `X-Forwarded-For` entries right to left, skipping trusted ones; the first untrusted entry is the client. If every entry is trusted, use the leftmost. Store the IP and scheme in the request context.
- [ ] **Step 4: Run the tests.** → PASS.
- [ ] **Step 5: Commit:** `Derive client IP and scheme only from trusted proxies`

### Task 6: Auth: limiter, Manager, Actor

**Files:**
- Rewrite: `internal/auth/auth.go`, `internal/auth/auth_test.go`
- Create: `internal/auth/limiter.go`, `internal/auth/limiter_test.go`

**Interfaces:**
- Consumes: Task 4's stores.
- Produces:
  - `auth.CheckPassword(hash, pw string) error`
  - `auth.HashPassword(pw string) (string, error)`
  - `auth.RootDigest(hash string) string`
  - `auth.NewLimiter() *Limiter`
  - `(*Limiter).Blocked(ip string) bool`
  - `(*Limiter).Fail(ip string)`
  - `auth.Actor{User *models.User; Session *models.Session}`
  - `(*Actor).IsRoot() bool`
  - `(*Actor).IsPlatform() bool` (root or platform admin)
  - `(*Actor).CanAdminSite() bool` (phase 1: `IsPlatform()`)
  - `auth.NewManager(sessions *models.SessionStore, users *models.UserStore, rootPasswordHash string, secure bool) *Manager`
  - `(*Manager).Login(w http.ResponseWriter, user *models.User, siteID *int64) error`
  - `(*Manager).Logout(w http.ResponseWriter, r *http.Request) error`
  - `(*Manager).Middleware(current func(*http.Request) (siteID int64, onSite bool)) func(http.Handler) http.Handler`
  - `auth.ActorFrom(r *http.Request) *Actor` (nil when not signed in)

- [ ] **Step 1: Write the failing tests.**
  - `TestLimiterCountsOnlyFailures`: `Blocked("a")` is false. After `Fail("a")` ×5, `Blocked("a")` is true and `Blocked("b")` is false.
  - `TestLimiterWindow`: add an unexported `now func() time.Time` field and set it in the test. Failures older than 15 minutes don't count.
  - The Manager tests run against an in-memory DB, with root created by `EnsureRoot`:
    - `TestRootSessionValidOnIssuingSiteOnly`: log in with `siteID=&7`. A request where `current` returns `(7, true)` carries an Actor with `IsRoot()`. `(8, true)` has a nil Actor, and so does `(0, false)`, the platform host.
    - `TestPlatformHostSession`: log in with `siteID=nil`. It's valid only when `current` returns `(0, false)`.
    - `TestRootSessionInvalidAfterPasswordChange`: log in through a Manager built with hash H1. A second Manager on the same stores, built with hash H2, sees a nil Actor for that cookie.
    - `TestCookieAttributes`: `HttpOnly`, `SameSite=Strict`, `Path=/`, no Domain, and `Secure` equal to the `secure` argument.
    - `TestLogoutDeletesSession`: after `Logout`, the same cookie gives a nil Actor.
    - `TestNonRootSessionRejectedInPhase1`: insert a session for a non-root user with raw SQL. The Actor is nil.
- [ ] **Step 2: Confirm RED.** `go test ./internal/auth/` → FAIL.
- [ ] **Step 3: Implement.** The limiter is in memory, keyed by IP. The Middleware never redirects; it only attaches an Actor. Delete `Session`, `SetSiteSession`, `SetGlobalSession` and the cookie-signing code.
- [ ] **Step 4: Run the tests.** → PASS.
- [ ] **Step 5: Commit:** `Back sessions with the database and bind them to the issuing host`

### Task 7: Themes

**Files:**
- Create: `internal/themes/themes.go`, `internal/themes/themes_test.go`
- Move with `git mv`:
  - `templates/` → `themes/default/templates/`, and `static/` → `themes/default/static/`
  - `samples/blog/{templates,static}` → `themes/blog/`, and `samples/nerdtx/{templates,static}` → `themes/nerdtx/`
  - Then `git rm` `samples/*/ntx.toml`, `samples/nerdtx/ntx.db`, and each theme's `templates/admin/`.

**Interfaces:**
- Produces:
  - `themes.List(root string) ([]string, error)`: sorted names of the folders under `root` that pass `validate.Key` and contain a `templates/` folder
  - `themes.Dir(root, name string) (string, error)`: an error if the name is invalid or the folder is missing

- [ ] **Step 1: Write the failing tests.**
  - `TestListOnlyValidThemes`: a temp root holds `good/templates/`, `Bad Name/templates/`, `notheme/` (no `templates/`) and a file `x`. `List` returns `["good"]`.
  - `TestDirRejectsTraversal`: `Dir(root, "../etc")`, `Dir(root, "a/b")` and `Dir(root, "missing")` all error. `Dir(root, "good")` returns `filepath.Join(root, "good")`.
  - `TestShippedThemesValid`: `List("../../themes")` contains `default`, `blog` and `nerdtx`.
- [ ] **Step 2: Confirm RED, implement, then PASS.** `go test ./internal/themes/`
- [ ] **Step 3: Commit:** `Add themes and move the shipped templates into them`

### Task 8: Embedded admin UI

**Files:**
- Create:
  - `internal/adminui/adminui.go`
  - `internal/adminui/templates/{layout,login,posts,editor,settings,platform_sites,platform_site_form,platform_site_delete}.html`
  - `internal/adminui/assets/{admin.css,admin.js}`
  - `internal/adminui/adminui_test.go`

**Interfaces:**
- Produces:
  - `adminui.Render(w http.ResponseWriter, page string, data map[string]any)`: renders `layout.html` plus `<page>.html`, and sets `Content-Type: text/html; charset=utf-8`
  - `adminui.Assets() http.Handler`: serves `assets/` and is mounted at `/admin/assets/`
- Template data keys:
  - always: `Actor` (*auth.Actor, may be nil), `Site` (*models.Site, nil on the platform host), `Error` (string)
  - each page's own keys, as listed in Task 10

- [ ] **Step 1: Write the failing tests.**
  - `TestEveryPageRenders`: each page name renders into a `httptest.Recorder` with `data` holding only `Actor: nil, Site: nil`, without an error.
  - `TestNoInlineScripts`: no embedded template contains `<script>` without a `src=` attribute, and none contains `onclick=`.
  - `TestAssetsServed`: `GET /admin/assets/admin.js` through `http.StripPrefix("/admin/assets/", Assets())` → 200.
- [ ] **Step 2: Confirm RED, then implement.**
  - Templates are parsed once at package init with `template.ParseFS`, one template set per page.
  - The behaviour from today's inline scripts (slug autofill, the post filter, delete confirmation) moves into `admin.js`, which is driven by `data-` attributes: `data-slug-source`, `data-filter-list`, `data-confirm`.
  - Forms post to the routes listed in Task 10.
  - The look is minimal, built on `admin.css`; this layout is the builder's call.
- [ ] **Step 3: Run the tests.** `go test ./internal/adminui/` → PASS.
- [ ] **Step 4: Commit:** `Embed admin and platform templates in the binary`

### Task 9: Site registry and sitectx

**Files:**
- Create: `internal/sitereg/registry.go`, `internal/sitereg/registry_test.go`
- Rewrite: `internal/sitectx/sitectx.go`, `internal/sitectx/sitectx_test.go`

**Interfaces:**
- Produces:
  - `sitereg.New(store *models.SiteStore) (*Registry, error)`
  - `(*Registry).Reload() error`
  - `(*Registry).ByHost(host string) *models.Site`: the input is already lowercased, with the port stripped
  - `(*Registry).ByID(id int64) *models.Site`
  - `(*Registry).All() []models.Site`: ordered by key
  - `sitectx.With(r, *models.Site) *http.Request`
  - `sitectx.From(r) *models.Site`
  - `sitectx.WithPlatform(r) *http.Request`
  - `sitectx.IsPlatform(r) bool`

- [ ] **Step 1: Write the failing tests.**
  - `TestRegistryReload`:
    - create site `a`; `ByHost("a.example.com")` finds it;
    - create `b`; it's found only after `Reload`;
    - delete `a` and `Reload`; `ByHost` for `a` is nil;
    - `ByID` agrees with `ByHost`.
  - `TestRegistryReturnsCopies`: mutating the returned `*Site` doesn't change what the next `ByHost` returns.
  - `TestPlatformFlag`: `IsPlatform(WithPlatform(r))` is true; for a plain `r` it's false.
- [ ] **Step 2: Confirm RED, implement (an RWMutex around maps rebuilt from `store.List()`), then PASS.**
- [ ] **Step 3: Commit:** `Resolve sites from an in-memory registry`

### Task 10: Handlers

**Files:**
- Rewrite: `internal/handlers/handler.go` (TemplateCache keyed by theme, `serve`, rendering), `admin.go`, `blog.go`, `home.go`, `generate.go`, `handler_test.go`
- Create: `internal/handlers/section.go`, `internal/handlers/settings.go`, `internal/handlers/platform.go`
- Delete: `internal/handlers/projects.go`

**Interfaces:**
- Consumes: Tasks 3, 4, 6, 7, 8 and 9.
- Produces:
  - `handlers.Deps{Sites *models.SiteStore; Posts *models.PostStore; Users *models.UserStore; Registry *sitereg.Registry; Auth *auth.Manager; Limiter *auth.Limiter; ThemesPath, PlatformHostname string; RootPasswordHash string; Dev bool}`
  - `handlers.New(d Deps) *Handler`
  - Public handlers:
    - `Home`, `About`, `BlogIndex`, `BlogPost`, `Static`
    - `Section` (`GET /{section}/{page}`, reading `chi.URLParam(r, "section")`)
  - Admin handlers:
    - `LoginForm`, `LoginPost`, `Logout`
    - `Posts`, `NewPost`, `CreatePost`, `EditPost`, `UpdatePost`
    - `Settings`, `SettingsPost`
  - Platform handlers:
    - `PlatformSites`, `PlatformNewSite`, `PlatformCreateSite`
    - `PlatformEditSite`, `PlatformUpdateSite`
    - `PlatformDeleteSiteForm`, `PlatformDeleteSite`
  - `(*Handler).Generate(outDir string) error`
  - `(*Handler).RequireSiteAdmin(next http.Handler) http.Handler` and `(*Handler).RequirePlatform(next http.Handler) http.Handler`: redirect to `/admin/login` (303) when there's no Actor, and return 403 when the Actor lacks the right

Page data keys (beyond Actor, Site and Error):
- `posts`: `Posts`, `Total`, `Published`, `Drafts`, `TagCount`
- `editor`: `Post`
- `settings`: `SectionsJSON`
- `platform_sites`: `Sites` ([]models.Site), `Themes` ([]string)
- `platform_site_form`: `Form` (*models.Site), `Themes`, `IsNew`
- `platform_site_delete`: `Target` (*models.Site)

Behaviour:
- **Login.** `LoginPost` reads `username` and `password`. The limiter is keyed by `clientip.IP(r)`:
  - if `Blocked`, render "Too many attempts. Try again later.";
  - otherwise, check `username == "root"` and the password against `RootPasswordHash`;
  - on failure, call `Fail` and render "Invalid username or password.";
  - on success, call `Auth.Login` with `siteID` = the site's ID on a site host, or nil on the platform host, then redirect (303) to `/admin/posts` on a site or `/admin/platform/sites` on the platform host.
- **Settings.** `SettingsPost` saves `name`, `description` and `about_slug` with `Sites.Update`, then `sections` (a JSON array of `{slug,title,vars,pages:[{slug,title,vars}]}`) with `Sites.SetSections`.
  - JSON decode errors and `*ValidationError` re-render with `Error`, and leave the stored data unchanged: parse first, then write.
  - On success, call `Registry.Reload()` and redirect to `/admin/settings`.
- **Site create and edit.** The form's `theme` must be one of `themes.List(ThemesPath)`; otherwise the form shows an error. Create and update call `Registry.Reload()`.
  - The edit form shows this fixed warning next to the hostname field: "Changing the hostname signs everyone out of this site; the provider callback URL and outstanding invite links must be updated."
  - Delete requires the `confirm` field to equal the site key.
  - After deleting the site you're signed in on, redirect to `/admin/login`. Otherwise go to `/admin/platform/sites`.
- **Rendering.** Public rendering uses the theme's TemplateCache. If `themes.Dir` fails or a template is missing, log it and return 500 "Internal Server Error". Admin pages never touch themes.
- **Sections.** `Section` looks up the section and page in the resolved site's `Sections`. Unknown → 404. A missing template file → 404, as today.
- **Generate.** Iterates `Registry.All()`; otherwise it's today's behaviour, reading sections from the site and static files from the theme.

- [ ] **Step 1: Write the failing tests** in `handler_test.go`. Use a helper `newEnv(t)`: an in-memory DB, `EnsureRoot`, theme `t1` in a temp themes root (written by today's `writeTemplates` helper under `t1/templates`, with `t1/static/css/x.css`), and site `a` on `a.example.com` with theme `t1`. Handlers are called with `sitectx.With` and an Actor attached through a real Manager login.
  - `TestSettingsRejectsBadSectionsJSON`: posting `sections=[{"slug":"x","vars":[1]}]` (vars isn't an object) and `sections=not json` each give 200, the body contains the error, and `List()` shows the sections unchanged.
  - `TestSettingsSavesSectionsAndReloads`: valid JSON → 303. `Registry.ByHost("a.example.com").Sections` has the new section.
  - `TestPlatformEditShowsConflictError`: create site `b`. Posting site `a`'s edit form with hostname `b.example.com` → 200, the body contains the conflict error, and the hostname is unchanged.
  - `TestPlatformCreateRejectsUnknownTheme`: theme `nope` → 200 with an error, and no site is created.
  - `TestPlatformDeleteNeedsTypedKey`: `confirm=wrong` → the site still exists; `confirm=a` → the site is gone and `Registry.ByID` is nil.
  - `TestRequirePlatformRedirectsAnonymous`: no Actor → 303 to `/admin/login`.
  - `TestGenerateIsolatesSiteOutput` and `TestGenerateRunsSitesInOrder`: ported from today. Sites come from the registry, and the order is by key.
  - `TestGeneratePublicTagsOnlyPublished`: a draft tagged `secret`. The generated `blog/index.html` doesn't contain `secret`.
  - `TestHandlersNotFoundWithoutResolvedSite`: ported. Every public handler and every site-admin handler returns 404 with no site in context.
- [ ] **Step 2: Confirm RED.** `go test ./internal/handlers/` → FAIL.
- [ ] **Step 3: Implement** per Behaviour above. Keep `serve(w, r, fn)` as the single place an unresolved site is handled.
- [ ] **Step 4: Run the tests.** `go test ./internal/handlers/` → PASS.
- [ ] **Step 5: Commit:** `Serve public pages from themes and manage sites and settings in the browser`

### Task 11: Server and wiring

**Files:**
- Rewrite: `internal/server/server.go`, `internal/server/server_test.go`
- Modify: `main.go` (`serve` and `generate`)

**Interfaces:**
- Consumes: everything above.
- Produces: `server.New(cfg *config.Config, h *handlers.Handler, reg *sitereg.Registry, am *auth.Manager) *http.Server`

Middleware order, outermost first:
1. `CrossOriginProtection`
2. `clientip.Middleware(cfg.TrustedProxies)`
3. `middleware.Logger`
4. `middleware.Recoverer`
5. `securityHeaders`
6. `resolveHost`
7. `am.Middleware(...)`

`resolveHost` lowercases the host and strips the port. It then attaches the site from `reg.ByHost` if there is one. Otherwise, if the host equals `cfg.PlatformHostname`, it calls `sitectx.WithPlatform`. Otherwise it returns 404.

Routes:
- **Public:** `GET /`, `/about`, `/blog`, `/blog/{slug}`, `/static/*`, `/{section}/{page}` (registered last).
- **Admin:** `/admin/assets/*`, `GET/POST /admin/login`, `POST /admin/logout`.
- **Site admin** (under `RequireSiteAdmin`):
  - `GET /admin/posts`, `GET /admin/posts/new`, `POST /admin/posts`
  - `GET /admin/posts/{id}/edit`, `POST /admin/posts/{id}`
  - `GET/POST /admin/settings`
- **Platform** (under `RequirePlatform`):
  - `GET /admin/platform/sites`, `GET /admin/platform/sites/new`, `POST /admin/platform/sites`
  - `GET /admin/platform/sites/{key}`, `POST /admin/platform/sites/{key}`
  - `GET/POST /admin/platform/sites/{key}/delete`

On the platform host, every non-admin route returns 404 by way of `serve`.

- [ ] **Step 1: Write the failing tests** in a rewritten `server_test.go`. Helper `setup(t)`: an in-memory DB; themes root with theme `t1`; sites `a.example.com` and `b.example.com`; `platform_hostname = "admin.example.com"`; a root password `rootpass`. Requests use an explicit `req.Host`, as today's two-site tests do.
  - `TestHostResolutionNormalises`: `Host: A.Example.COM:8443` → 200 home of site `a`; `Host: ADMIN.example.com:443` + `GET /admin/login` → 200.
  - `TestUnknownHost404`.
  - `TestPlatformHostServesOnlyAdmin`: `GET /` and `GET /blog` on the platform host → 404; `GET /admin/login` → 200.
  - `TestRootLoginFlowOnSite`: a POST with `root`/`rootpass` on `a` → 303 to `/admin/posts`; with the cookie, `GET /admin/posts` → 200.
  - `TestRootLoginOnPlatformHost`: → 303 to `/admin/platform/sites`; that page → 200.
  - `TestSessionBoundToIssuingHost`: a cookie from `a` presented on `b` → `GET /admin/posts` gives 303 to login; on the platform host, `GET /admin/platform/sites` gives 303. A platform-host cookie on `a` → 303.
  - `TestLoginFailuresRateLimitedPerClientIP`: `trusted_proxies = 127.0.0.1/32`. Five bad POSTs with `X-Forwarded-For: 1.1.1.1` give "Too many attempts" on the sixth. A correct POST with `X-Forwarded-For: 2.2.2.2` → 303.
  - `TestSuccessfulLoginsDontConsumeBudget`: 6 correct logins → all 303.
  - `TestCrossOriginPostRejected`: `POST /admin/login` with `Sec-Fetch-Site: cross-site` → 403.
  - `TestMissingThemeKeepsAdminWorking`: delete `t1` from disk after setup. `GET /` on `a` → 500; `GET /admin/login` on `a` → 200.
  - `TestSectionRouteDynamic`: root saves sections `[{"slug":"projects","pages":[{"slug":"one","title":"One"}]}]` for `a` (theme `t1` has `sections/projects/one/index.html`). Then `GET /projects/one` on `a` → 200, and on `b` → 404.
  - `TestBlogSlugNotShadowedBySection`: `GET /blog/x` with a published post `x` → 200 (a static segment beats `/{section}/{page}`).
  - `TestTenantIsolation`, ported from today's `TestTwoSitesIsolatedByHost`: posts created on `a` aren't visible on `b`.
- [ ] **Step 2: Confirm RED.** `go test ./internal/server/` → FAIL.
- [ ] **Step 3: Implement `server.New`.** Then rewire `main.go`:
  - `serve`: `config.Load` → `db.Open` → `UserStore.EnsureRoot` → `sitereg.New` → `auth.NewManager(..., !cfg.Dev)` → `handlers.New` → `server.New`.
  - `generate`: the same stores and registry, then `h.Generate(out)`.
- [ ] **Step 4: Run the full suite.** `go build ./... && go vet ./... && go test ./...` → all PASS.
- [ ] **Step 5: Run the app.** Run `gen-config -p` into a temp dir and `serve` with `themes_path` pointing at the repo `themes/`. Then:
  - `curl -H 'Host: localhost' /admin/login` → 200;
  - log in as root on the platform host;
  - create site `demo` on host `demo.localhost` with theme `default`;
  - `curl -H 'Host: demo.localhost' /` → 200.
- [ ] **Step 6: Commit:** `Wire the router for web-managed sites, the platform host, and CSRF protection`

### Task 12: Docs

**Files:**
- Modify: `README.md`, `docs/usage.md`, `docs/ai-context.md`, `docs/ntx.sample.toml`

- [ ] **Step 1: Rewrite the docs.**
  - The config reference matches the new keys, and `docs/ntx.sample.toml` equals `gen-config`'s output minus the secrets.
  - Add a "Getting started" walkthrough: gen-config, serve, sign in as root on `platform_hostname`, create a site, pick a theme.
  - Add a "Themes" section: the folder layout and that admin pages aren't themed.
  - Add a note that existing databases must be deleted.
  - Remove `[[site]]`, upgrade/migration and `global_admin_password_hash` text.
  - Update `ai-context.md` to describe the packages above.
- [ ] **Step 2: Verify no stale references remain.** `grep -rn '\[\[site\]\]\|global_admin_password_hash\|template_path\|static_path\|EnsureSites' --include='*.md' --include='*.toml' --include='*.go' . | grep -v docs/superpowers` → no output.
- [ ] **Step 3: Commit:** `Document web-managed sites, themes and root sign-in`
