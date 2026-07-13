# ntx Codebase Improvement List

## Bugs / Correctness

1. **`loginLimiter` leaks memory** — `l.attempts[ip] = recent` keeps empty slices in the map
   forever when all attempts expire. Every unique IP that ever hits the login page accumulates a
   key. Should `delete(l.attempts, ip)` when `len(recent) == 0`.

2. **`loginLimiter.allow` has a slice-aliasing footgun** — `recent := l.attempts[ip][:0]` shares
   the backing array, then iterates over `l.attempts[ip]` while writing into it. It happens to be
   correct (you only overwrite positions you've already read), but it's the kind of code that fails
   a review and confuses anyone maintaining it. Just `var recent []time.Time`.

3. **Static site generation silently drops tag pages** — `generate.go` emits `/blog` with all
   posts but never generates `/blog?tag=X` pages. Tag cloud links in the generated static output
   are dead URLs. Either generate a page per tag or strip tag links from the static build.

4. **No slug validation** — The admin form accepts anything as a slug, including spaces, slashes,
   and unicode. A slug like `my post` produces a broken URL. Should validate against
   `^[a-z0-9-]+$` before save.

5. **No graceful shutdown** — `srv.ListenAndServe()` in `main.go` exits immediately on
   `SIGTERM`/`SIGINT`, killing in-flight requests. Should use `signal.NotifyContext` +
   `srv.Shutdown(ctx)`.

## Code Quality / Duplication

6. **`renderToBytes` in `generate.go` duplicates `render` in `handler.go`** — Both merge
   `siteData()`, execute the template into a `bytes.Buffer`, handle errors the same way. The only
   difference is the output destination. Could be one `renderTo(w io.Writer, ...)` function.

7. **Section data assembly is duplicated** — The logic that merges `sec.Vars`, `sec.Title`,
   `page.Vars`, `page.Title` into the template data map is written identically in `SectionPage`
   (handler) and the section loop in `generate.go`. Extract to a helper on `config.Section`.

8. **`blog.go` uses `var posts interface{}`** — Both branches of the `if tag != ""` block return
   `[]models.Post`. Type it correctly. `any` here only obscures the types and prevents the
   compiler from helping.

9. **18 `map[string]interface{}` literals across handlers** — Go 1.18+: `map[string]any` reads
   better. Minor, but consistent with modern Go style.

10. **`sampleConfig` in `main.go` and `docs/ntx.sample.toml` can drift** — Two sources of truth
    for the config format. The sample config string in `main.go` (`gen-config` output) will
    diverge from the toml file whenever someone updates one. Embed the toml file and use it for
    both.

## DB / Data Layer

11. **Missing indexes** — `posts.created_at` is in every `ORDER BY` and `post_tags.tag_id` is
    used in every tag lookup join. Neither has an index. Add to the migration (a separate
    `002_indexes.sql`).

12. **`Stats()` is 3 round trips** — Total, published count, and tag count are three separate
    queries. A single `SELECT COUNT(*), COUNT(*) FILTER (WHERE published = 1) FROM posts` (SQLite)
    or equivalent covers the first two. Minor at this scale, but needlessly chatty.

13. **`resolveTags` is N round trips** — One `FirstOrCreate` per tag. For a typical 3-tag post
    this is 3 queries inside an outer transaction. A batch upsert (`INSERT OR IGNORE` + batch
    `SELECT WHERE name IN (...)`) would be 2 queries regardless of tag count.

14. **`Post.ID int64` vs `dbPost.ID uint` asymmetry** — The cast works but the mismatch is a
    constant source of confusion and noise. Pick one (`int64` to match Go database convention) and
    use it throughout.

15. **Orphan tags** — Tags created by `resolveTags` survive post deletion (cascade only removes
    `post_tags` rows, not `tags` rows). `AllTags()` returns them, so the tag cloud shows tags that
    belong to no post. Either clean up in `Delete()` or filter `AllTags()` to only return tags
    with at least one published post.

## Operational Gaps

16. **GORM logger is noisy** — The default GORM logger prints red "record not found" to stdout on
    every not-found lookup (`GetBySlug`, `GetByID`). In production this fills logs with
    non-errors. Pass `logger.Silent` (or a custom logger) to `gorm.Config`.

17. **`log` vs `slog`** — The codebase uses the old `log` package throughout. Go 1.21 stabilized
    `slog` for structured, leveled logging. Not urgent, but worth migrating to eventually.

18. **`db.Open` passes `context.Background()` to goose** — Migrations run with no timeout and no
    cancellation. On a slow or stuck DB this hangs the process forever at startup. Pass a context
    with a deadline (e.g., 30s).

19. **No request timeout on handlers** — The server has `ReadTimeout`/`WriteTimeout`, but slow
    handlers (e.g., a sluggish DB query) can still hold the `WriteTimeout` open. For
    defense-in-depth, wrap handlers with `http.TimeoutHandler`.

20. **Static file server has no cache headers** — `http.FileServer` serves statics with no
    `Cache-Control`. Every asset request re-validates. Add a middleware or wrap the handler to set
    appropriate cache headers.
