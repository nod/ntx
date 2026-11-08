# Roles and SSO, phase 3: pending revisions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A contributor without `can_publish` can edit their own live-once posts; the edit waits as a pending revision until a site admin publishes or discards it, with a warning when the post changed underneath it.

**Architecture:** The `post_revisions` table already exists in `100_initial.sql` (no schema change). Revision storage goes on the site-scoped `PostStore` in a new `internal/models/revision.go`. `authz` gains two rules. The editor gets a "pending" mode in place of phase 2's read-only stopgap, and a new review page (two plain panes, current and pending) carries Publish changes and Discard.

**Tech Stack:** Go 1.26, chi v5, GORM (glebarez/sqlite, Postgres via pgx), html/template admin UI in `internal/adminui`.

**Spec:** `docs/superpowers/specs/2026-11-07-roles-and-sso-design.md` (sections "Contributor rules in detail", "Post rules", "Admin views", "Build order" item 3).

## Global Constraints

- No migrations and no schema changes: use `post_revisions` as defined in both `100_initial.sql` files (`post_id` unique, `author_id` set null, `tags` JSON array, `base_updated_at`).
- "Editing their own **live-once** post never changes the post. The edit is saved as that post's pending revision. Saving again overwrites the pending revision. A pending revision cannot change the slug."
- "Publishing a revision copies [title, body, tags] onto the post and deletes the revision. It does not change whether the post is published."
- "If the post's `updated_at` no longer equals the revision's `base_updated_at` … the publish screen shows a warning and needs confirmation."
- "A never-published draft never gets a revision; edits to it change the post directly."
- "Neither drafts nor pending revisions affect any public page, `ntx generate` output, or the public tag list."
- Permission checks live only in `internal/authz`; handlers and templates ask it.
- Editor copy for a contributor without `can_publish` on a live-once post, exactly: "Your changes will be reviewed before they go live."
- Revisions and posts by a deleted user show the author as "former member".
- Timestamps compared for staleness or versioning are always values read back from the database, compared with `time.Time.Equal`. Revision timestamps are written as `time.Now().UTC().Truncate(time.Microsecond)` so SQLite (ns) and Postgres (µs) store the same value Go holds.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **The contributor saves again while an admin has the review page open.** The admin's Publish must not publish text they never saw: it fails with "saved again" and shows the latest version. (Test: Task 4 `TestPublishChangesRefusesNewerRevision`.)
2. **The post is edited directly after the review page loaded without a warning.** Publish without confirmation must re-show the page with the warning and change nothing. (Test: Task 4 `TestStaleChangesNeedConfirmation`.)
3. **Timestamp round trip.** An untouched post must never show the stale warning, including after the base timestamp travels through the editor form; on Postgres too (µs precision, JSONB tags). (Tests: Task 1 `TestPendingNotStaleWhenUntouched`, `TestRevisionsOnPostgres`; Task 3 `TestContributorNoPublishSavesPendingChanges` asserts not stale.)
4. **Forged form fields from a contributor on a live-once post:** a changed `slug`, or `action=publish|unpublish|delete`. Slug is ignored, the actions are 403, the post is untouched. (Test: Task 3 `TestPendingSaveIgnoresForgedFields`.)
5. **A draft published by an admin while its contributor still has it open.** The contributor's next save must become a pending revision marked stale, not overwrite or unpublish the live post (this also closes issue #14 for the save path). (Tests: Task 3 `TestDraftPublishedWhileContributorEdits`; Task 1 `TestUpdateContentKeepsPublishedState`.)

---

### Task 1: Revision storage

**Files:**
- Create: `internal/models/revision.go`
- Modify: `internal/models/post.go` (add `UpdateContent`)
- Modify: `internal/models/people_test.go` (split fixture: `newPeopleOn(t, d *gorm.DB)`, `newPeople` calls it with `newTestSiteDB(t)`)
- Test: `internal/models/revision_test.go`, `internal/models/revision_pg_test.go`

**Interfaces:**
- Produces:
  ```go
  var ErrNotLiveOnce = errors.New("post has never been published")
  var ErrRevisionChanged = errors.New("pending changes were saved again, published or discarded")
  var ErrStale = errors.New("post was edited after these changes were started")

  type Revision struct {
      ID, PostID     int64
      AuthorID       *int64
      Title, Body    string
      Tags           []string
      BaseUpdatedAt  time.Time
      CreatedAt      time.Time
      UpdatedAt      time.Time
  }
  // Stale: the post was changed since the revision was started.
  func (r *Revision) Stale(p *Post) bool // !p.UpdatedAt.Equal(r.BaseUpdatedAt)

  func (s *PostStore) GetPending(postID int64) (*Revision, error)          // nil, nil when none
  func (s *PostStore) PendingIDs() (map[int64]bool, error)                  // post ids on this site with a revision
  func (s *PostStore) SavePending(postID, authorID int64, title, body string, tags []string, base time.Time) (*Revision, error)
  func (s *PostStore) PublishPending(postID int64, seen time.Time, allowStale bool) error
  func (s *PostStore) DiscardPending(postID int64, seen time.Time) error
  func (s *PostStore) UpdateContent(p *Post) error                          // slug, title, body, tags; never published
  ```

Rules the code must follow:
- Every method confines itself to `s.siteID` by joining or checking `posts.site_id = ?` as explicit SQL (see the `PostStore` comment on GORM's zero-value struct conditions). A post id from another site behaves as "not found": `GetPending` returns nil, the writes return an error.
- `SavePending` refuses (`ErrNotLiveOnce`) a post with no `first_published_at`. Creating: `base_updated_at = base`, `author_id = authorID`. Overwriting: title, body, tags, author and `updated_at` change; `base_updated_at` and `created_at` stay. Tags are trimmed, empties dropped, stored as a JSON array (`encoding/json`) in a `string` field; an empty list is `[]`.
- `PublishPending` runs in one transaction: load the revision (missing, or `UpdatedAt` not `Equal(seen)` → `ErrRevisionChanged`); load the post (`UpdatedAt` not `Equal(rev.BaseUpdatedAt)` and `!allowStale` → `ErrStale`); write title and body to the post (GORM bumps `updated_at`), replace its tags via `resolveTags`, delete the revision. It never writes `slug`, `published` or `first_published_at`.
- `DiscardPending` deletes the revision only if it exists and its `UpdatedAt` `Equal(seen)`, else `ErrRevisionChanged`.
- `UpdateContent` is `Update` without the `published` and `first_published_at` fields.

- [ ] **Step 1: Write the failing tests** in `revision_test.go` (fixture `newPeople`, posts via `NewPostStore(f.db).ForSite(f.a.ID)`):
  - `TestSavePendingRefusesNeverPublished`: `SavePending` on a draft → `errors.Is(err, ErrNotLiveOnce)`; `GetPending` → nil.
  - `TestSavePendingLeavesPostAlone`: published post "Live" with tag "old"; `SavePending(id, u.ID, "New", "nb", []string{" new ", ""}, post.UpdatedAt)`; post re-read has title "Live", tags `["old"]`; `GetPending` has title "New", body "nb", tags `["new"]`, `AuthorID == u.ID`; `PendingIDs()` == `{id: true}`; `AllTags()` does not contain "new".
  - `TestSavePendingOverwrites`: save twice with different titles and bases; one revision, second title, `BaseUpdatedAt` equals the first base, `UpdatedAt` after the first's.
  - `TestPendingNotStaleWhenUntouched`: post read back from the store, `SavePending(..., post.UpdatedAt)`, re-read both → `rev.Stale(post)` false. Then `Update` the post's title → re-read → `Stale` true.
  - `TestPublishPendingCopiesContent`: published post; save pending (title "New", tags `["t2"]`); `PublishPending(id, rev.UpdatedAt, false)` → nil; post has title "New", tags `["t2"]`, slug and `Published` unchanged; `GetPending` nil. Repeat on an unpublished live-once post: still unpublished after.
  - `TestPublishPendingStale`: save pending, then `Update` the post directly; `PublishPending(id, rev.UpdatedAt, false)` → `ErrStale`, post and revision unchanged; with `allowStale=true` → nil and content copied.
  - `TestPublishPendingRefusesNewerRevision`: save pending (rev1), save again (rev2 text "Second"); `PublishPending(id, rev1.UpdatedAt, true)` → `ErrRevisionChanged`, post unchanged; `DiscardPending(id, rev1.UpdatedAt)` → `ErrRevisionChanged`; `DiscardPending(id, rev2.UpdatedAt)` → nil, `GetPending` nil, post unchanged.
  - `TestPendingScopedToSite`: revision on site A's post; through `ForSite(f.b.ID)`: `GetPending` nil, `PendingIDs` empty, `SavePending`/`PublishPending`/`DiscardPending` return errors; site A's revision still there.
  - `TestRevisionCascadesAndOrphans`: deleting the post removes its revision; deleting the revision's author (`f.users.Delete`) leaves the revision with `AuthorID == nil`.
  - `TestUpdateContentKeepsPublishedState`: published post; `p.Published = false; p.Title = "T2"; UpdateContent(p)` → re-read is published with title "T2".

- [ ] **Step 2: Run them to verify they fail**

  Run: `go test ./internal/models/ -run 'Pending|Revision|UpdateContent'`
  Expected: build failure — `SavePending`, `Revision`, etc. undefined.

- [ ] **Step 3: Implement `internal/models/revision.go` and `UpdateContent`** per the Interfaces block and rules above. `dbRevision` maps `post_revisions` (`TableName`), with `Tags string`.

- [ ] **Step 4: Run them to verify they pass**

  Run: `go test ./internal/models/`
  Expected: PASS

- [ ] **Step 5: Postgres check.** Write `TestRevisionsOnPostgres` in `revision_pg_test.go`: `t.Skip` unless `NTX_TEST_POSTGRES_DSN` is set; `db.Open(dsn)`, `newPeopleOn(t, d)`, then the `TestSavePendingLeavesPostAlone` assertions plus not-stale (as `TestPendingNotStaleWhenUntouched`) and a `PublishPending` with tags. Run it against a throwaway container:

  ```bash
  docker run -d --rm --name ntx-pg-rev -e POSTGRES_PASSWORD=pw -p 55432:5432 postgres:17
  until docker exec ntx-pg-rev pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
  NTX_TEST_POSTGRES_DSN='postgres://postgres:pw@localhost:55432/postgres?sslmode=disable' go test ./internal/models/ -run TestRevisionsOnPostgres -v
  docker stop ntx-pg-rev
  ```
  Expected: `--- PASS: TestRevisionsOnPostgres` (not SKIP). If `db.Open` wants a different DSN scheme, use the one `internal/db` accepts. If Docker can't run, ledger a ruling and add the gap to the final report.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/models/
  git commit -m "Store pending revisions on posts"
  ```

### Task 2: Role rules for pending changes

**Files:**
- Modify: `internal/authz/authz.go`
- Test: `internal/authz/authz_test.go`

**Interfaces:**
- Consumes: `models.Post.LiveOnce()`.
- Produces:
  ```go
  // SavesAsPending: the actor's edits to p wait for a site admin — their
  // own live-once post, without can_publish.
  func SavesAsPending(a *auth.Actor, p *models.Post) bool
  // CanReviewChanges: publish or discard p's pending revision.
  func CanReviewChanges(a *auth.Actor, p *models.Post) bool // SiteAdmin(a)
  ```
  `CanEditPost` keeps its meaning: edits take effect directly.

- [ ] **Step 1: Add role-table cases** to `TestRoleTable` (add both functions to a new `reviewChecks` list, not `postChecks`, since admins get false from `SavesAsPending`):
  - `SavesAsPending`: `contribNoPublish`+`ownLive` true; `contribNoPublish`+`ownDraft` false; `contribNoPublish`+`otherLive` false; `contribPublish`+`pubOwnLive` false; `siteAdmin`, `platformContrib`, `root` on every post false; `anon` false.
  - `CanReviewChanges`: `siteAdmin`, `platformContrib`, `root` on every post true; `contribNoPublish`+`ownLive` false; `contribPublish`+`pubOwnLive` false; `anon` false.
  - Invariant loop: for every actor in `everyActor` and post in `everyPost`, `SavesAsPending && CanEditPost` is never true.

- [ ] **Step 2: Run to verify failure**

  Run: `go test ./internal/authz/`
  Expected: build failure — `SavesAsPending` undefined.

- [ ] **Step 3: Implement both functions** in `authz.go`.

- [ ] **Step 4: Run to verify pass**

  Run: `go test ./internal/authz/`
  Expected: PASS

- [ ] **Step 5: Commit**

  ```bash
  git add internal/authz/
  git commit -m "Add the pending-changes rules to the role table"
  ```

### Task 3: Editing a live-once post saves pending changes

**Files:**
- Modify: `internal/handlers/admin.go` (`editor`, `EditPost`, `UpdatePost`)
- Modify: `internal/adminui/templates/editor.html`
- Modify: `internal/adminui/adminui_test.go` if it references `ReadOnly`
- Test: `internal/handlers/posts_test.go` (replace `TestContributorNoPublishLiveOnceReadOnly`)

**Interfaces:**
- Consumes: Task 1 `GetPending`, `SavePending`, `UpdateContent`, `Revision.Stale`; Task 2 `SavesAsPending`, `CanEditPost`, `SiteAdmin`.
- Produces (editor template data, used again by Task 4's link): `Mode` = `"direct"` or `"pending"`; `Base` (RFC3339Nano string); `Pending *models.Revision` (the post's revision, if any); `ReviewLink bool`.

Behaviour:
- `EditPost`: `CanEditPost` → mode `direct`; else `SavesAsPending` → mode `pending`; else `h.forbidden` (403). `ReadOnly` and the phase 2 stopgap sentence are removed.
- Mode `pending`: form fields show the revision's title, body and tags if one exists, otherwise the post's; slug is shown `readonly`; the notice reads "Your changes will be reviewed before they go live."; the only button is Save; a hidden `base` input carries the post's `UpdatedAt` formatted `time.RFC3339Nano`.
- Mode `direct` with a revision present: site admins (`CanReviewChanges`) see "This post has pending changes." with a link to `/admin/posts/{id}/pending` (`ReviewLink`); anyone else (a contributor who gained `can_publish`) sees "Your earlier changes are waiting for a site admin."
- `UpdatePost`, after the unchanged delete branch: if `SavesAsPending(a, existing)`: any action other than `save` → 403; parse form `base` with `time.RFC3339Nano`, falling back to `existing.UpdatedAt` if absent or unparseable; `SavePending(existing.ID, a.User.ID, title, body, tags, base)` (slug ignored); redirect `/admin/posts`. Else if `CanEditPost`: as today, except `save` calls `UpdateContent` instead of `Update`. Else 403.

- [ ] **Step 1: Write the failing tests** in `posts_test.go`, deleting `TestContributorNoPublishLiveOnceReadOnly`:
  - `TestContributorNoPublishSavesPendingChanges`: contributor `me` (no publish), published post `live`. Snapshot `GET /`, `/blog`, `/blog/live`, `/blog?tag=fresh` bodies (no cookie). GET the editor: contains "Your changes will be reviewed before they go live.", slug input has `readonly`, has `value="save"`, lacks `value="publish"`, `value="unpublish"`, `value="delete"`; extract the `base` value. POST `postForm("Reworded", "live", "save")` with `tags=fresh` and that `base` → 303. Post re-read: title "Title live". `GetPending`: title "Reworded", tags `["fresh"]`, and `!rev.Stale(post)`. The four public bodies equal their snapshots. GET the editor again: shows "Reworded". Save again with title "Reworded twice" → still one revision, new title.
  - `TestPendingSaveIgnoresForgedFields`: same setup; POST with slug `"moved"` and action `save` → 303, post slug still `live`, revision has no slug to check (assert `GetBySlug("moved")` nil). Then each of `publish`, `unpublish`, `delete` → 403; post unchanged, still published.
  - `TestDraftPublishedWhileContributorEdits`: `me`'s draft `d`; GET editor (mode direct, extract nothing); a site admin publishes it via `UpdatePost` action `publish`; `me` POSTs action `save`, title "Late edit", with `base` set to the draft's original `UpdatedAt` (RFC3339Nano) → 303. Post: still published, title unchanged. Revision exists, `Stale(post)` true.
  - `TestEditorPendingNotices`: with a revision on `live`: site admin's editor contains "This post has pending changes." and `/admin/posts/{id}/pending`; promote `me` to `can_publish` (`e.mems.Update` or the store method phase 2 uses) → `me`'s editor contains "Your earlier changes are waiting for a site admin." and no link.
  - `TestSaveKeepsPublishedState`: contributor with `can_publish` saves (action `save`) their published post with a new title → post still published, new title. (Guards the `UpdateContent` switch.)

- [ ] **Step 2: Run to verify failure**

  Run: `go test ./internal/handlers/ -run 'Pending|DraftPublished|SaveKeeps|EditorPending'`
  Expected: FAIL — 403 on the pending save, missing notice text.

- [ ] **Step 3: Implement** the behaviour above in `admin.go` and `editor.html`.

- [ ] **Step 4: Run to verify pass**

  Run: `go test ./internal/handlers/ ./internal/adminui/`
  Expected: PASS

- [ ] **Step 5: Commit**

  ```bash
  git add internal/handlers/ internal/adminui/
  git commit -m "Save contributors' edits to live posts as pending changes"
  ```

### Task 4: Reviewing pending changes, and docs

**Files:**
- Create: `internal/adminui/templates/pending.html`
- Create: `internal/handlers/pending.go`
- Modify: `internal/handlers/admin.go` (`postRow`, `Posts`), `internal/handlers/routes.go`, `internal/adminui/adminui.go` (`pageNames` + `"pending"`), `internal/adminui/adminui_test.go` (`testPages` + `"pending"`), `internal/adminui/templates/posts.html`
- Modify: `docs/usage.md`, `docs/ai-context.md`, `README.md` (only if it lists roles' abilities)
- Test: `internal/handlers/pending_test.go`

**Interfaces:**
- Consumes: Task 1 `GetPending`, `PendingIDs`, `PublishPending`, `DiscardPending`, `ErrStale`, `ErrRevisionChanged`, `Revision.Stale`; Task 2 `CanReviewChanges`.
- Produces: routes in the existing `RequireMember` group —
  `GET /admin/posts/{id}/pending` → `h.PendingChanges`,
  `POST /admin/posts/{id}/pending/publish` → `h.PublishChanges`,
  `POST /admin/posts/{id}/pending/discard` → `h.DiscardChanges`.

Behaviour:
- All three: `postFromParam` (404 for unseen posts), then `!CanReviewChanges` → `h.forbidden` (403).
- `PendingChanges`: no revision → 404. Renders two plain panes, "current" and "pending", each with title, tags and body as escaped text; the revision's author (same naming as the posts list, "former member" when `AuthorID` is nil) and its `UpdatedAt`. When `rev.Stale(post)`: warning "This post was edited after these changes were started. Publishing them will overwrite those edits." and the publish form carries `confirm=1` with button text "publish anyway"; otherwise button "publish changes". Both forms carry hidden `revision` = `rev.UpdatedAt` (RFC3339Nano). Discard button uses `data-confirm="Discard these changes?"`.
- `PublishChanges`: parse `revision` (unparseable → 400 message page); `PublishPending(id, seen, r.FormValue("confirm") == "1")`. `nil` → 303 `/admin/posts`. `ErrStale` → re-render the pending page (now showing the warning) with status 409. `ErrRevisionChanged` with a revision still present → re-render the pending page with status 409 and the notice "The contributor saved again since you opened this page. Review the latest version."; with no revision → message page, 409, "These changes were already published or discarded.", link back to posts.
- `DiscardChanges`: same parsing and `ErrRevisionChanged` handling; success → 303 `/admin/posts`.
- Posts list: `postRow.Pending bool` from `PendingIDs()`; a `pending changes` badge beside the status; for site admins (`CanReviewChanges`) a "review" link to the pending page.

- [ ] **Step 1: Write the failing tests** in `pending_test.go` (helper in the file: `e.pending(u, post, title, tags)` calls `SavePending` with the post's re-read `UpdatedAt`; `revisionAt(rev)` formats RFC3339Nano):
  - `TestAdminPublishesPendingChanges`: `me`'s published post with revision "Reworded"/tag "fresh"; admin GET pending page → 200, contains "Title live" and "Reworded", "publish changes", no warning. POST publish with `revision` → 303. Public `/blog/live` shows "Reworded"; `/blog?tag=fresh` lists it; post still published, slug `live`; `GetPending` nil; posts list no longer says "pending changes".
  - `TestPublishChangesKeepsUnpublished`: same on an unpublished live-once post → content updated, `Published` false, `/blog/live` still 404.
  - `TestStaleChangesNeedConfirmation`: revision, then admin edits post directly (`UpdatePost` action `save`). POST publish without `confirm` → 409, body contains the warning and "publish anyway"; post keeps the admin's edit; revision remains. POST again with `confirm=1` → 303, revision's content live.
  - `TestPublishChangesRefusesNewerRevision`: admin reads the page (revision A's `revision` value); `me` saves again ("Second"); admin POSTs publish with A's value → 409, body contains "saved again" and "Second"; post unchanged; then discard with A's value → 409, revision still there.
  - `TestDiscardPendingChanges`: discard → 303; `GetPending` nil; post unchanged; publish with the old value → 409 "already published or discarded".
  - `TestContributorCannotReviewChanges`: `me` (no publish) on own post with revision: GET page, POST publish, POST discard → 403 each; revision unchanged. Another contributor's post with a revision → 404 each.
  - `TestPendingChangesSiteIsolation`: create site B as `TestMembersOtherSiteIDs` does; a published post on B (`e.posts.ForSite(b.ID)`) with a revision; site A's admin (`e.siteAdminCookie()`) on `hostA` GETs the pending page and POSTs publish and discard for B's post id → 404 each; B's revision and post unchanged.
  - `TestPendingPageFormerMember`: delete `me` via `e.users.Delete`; admin's pending page shows "former member"; publish still works.
  - `TestPostListShowsPendingChanges`: admin list contains "pending changes" and the review link; `me`'s list contains "pending changes" and no `/pending` link.

- [ ] **Step 2: Run to verify failure**

  Run: `go test ./internal/handlers/ -run 'Pending|PublishChanges|Discard|ReviewChanges|StaleChanges'`
  Expected: FAIL — 404s on the new routes, missing "pending changes".

- [ ] **Step 3: Implement** the handlers, routes, template, list changes and `pageNames`.

- [ ] **Step 4: Run to verify pass**

  Run: `go test ./internal/handlers/ ./internal/adminui/`
  Expected: PASS

- [ ] **Step 5: Docs.** `docs/usage.md` "People and roles": replace the phase 2 note that contributors can't edit published posts with how pending changes work (save → waits; admin reviews side by side; publish changes doesn't change published state; stale warning; discard). `docs/ai-context.md`: `revision.go` on `PostStore`, the two `authz` rules, the pending routes, the timestamp rule from Global Constraints.

- [ ] **Step 6: Full suite and vet**

  Run: `gofmt -l . && go vet ./... && go test ./... 2>&1 | tail -20`
  Expected: no gofmt output, vet clean, every package `ok`.

- [ ] **Step 7: Commit**

  ```bash
  git add internal/ docs/ README.md
  git commit -m "Review, publish and discard pending changes"
  ```
