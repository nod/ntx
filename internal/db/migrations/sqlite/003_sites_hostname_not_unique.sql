-- +goose Up

-- Drop the UNIQUE constraint on sites.hostname. Nothing reads the column --
-- it is write-only bookkeeping -- while site_url uniqueness is already
-- validated in config.Load, which is the real enforcement point and reports a
-- clear error. The DB-level UNIQUE only produced opaque startup failures
-- ("ensuring sites: constraint failed: UNIQUE constraint failed:
-- sites.hostname") on ordinary config edits: renaming a site's key while
-- keeping its site_url, or swapping site_url between two sites.
--
-- SQLite cannot drop a column constraint in place -- the UNIQUE is backed by
-- an automatic index DROP INDEX refuses to touch -- so sites must be rebuilt,
-- and posts/tags carry a NOT NULL FK to sites(id). A rename-in-place shortcut
-- is not available: ALTER TABLE ... RENAME rewrites other tables' REFERENCES
-- clauses to follow the rename, so the children end up pointing at the
-- renamed original, and dropping it then fails the FK check. (PRAGMA
-- legacy_alter_table, which would suppress that rewriting, is a no-op inside
-- the transaction goose wraps each migration in.)
--
-- So all four tables are recreated before any DROP, exactly as 002 does, and
-- the children are dropped before their parents: with foreign_keys=ON a DROP
-- TABLE performs an implicit DELETE, which would cascade through post_tags
-- and fail the FKs posts/tags hold against sites.
CREATE TABLE sites_new (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    key      TEXT    NOT NULL UNIQUE,
    hostname TEXT    NOT NULL
);
INSERT INTO sites_new (id, key, hostname) SELECT id, key, hostname FROM sites;

CREATE TABLE posts_new (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    site_id     INTEGER  NOT NULL REFERENCES sites_new(id),
    slug        TEXT     NOT NULL,
    title       TEXT     NOT NULL,
    body        TEXT     NOT NULL,
    published   INTEGER  NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);
INSERT INTO posts_new (id, site_id, slug, title, body, published, created_at, updated_at)
    SELECT id, site_id, slug, title, body, published, created_at, updated_at FROM posts;

CREATE TABLE tags_new (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id INTEGER NOT NULL REFERENCES sites_new(id),
    name    TEXT    NOT NULL
);
INSERT INTO tags_new (id, site_id, name) SELECT id, site_id, name FROM tags;

CREATE TABLE post_tags_new (
    post_id INTEGER NOT NULL REFERENCES posts_new(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags_new(id)  ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);
INSERT INTO post_tags_new (post_id, tag_id) SELECT post_id, tag_id FROM post_tags;

DROP INDEX IF EXISTS idx_tags_site_name;
DROP INDEX IF EXISTS idx_posts_site_slug;

DROP TABLE post_tags;
DROP TABLE posts;
DROP TABLE tags;
DROP TABLE sites;

ALTER TABLE sites_new     RENAME TO sites;
ALTER TABLE posts_new     RENAME TO posts;
ALTER TABLE tags_new      RENAME TO tags;
ALTER TABLE post_tags_new RENAME TO post_tags;

CREATE UNIQUE INDEX idx_posts_site_slug ON posts(site_id, slug);
CREATE UNIQUE INDEX idx_tags_site_name  ON tags(site_id, name);

-- +goose Down

-- Restore uniqueness enforcement on hostname as an explicit index rather than
-- by rebuilding four tables a second time: an index enforces exactly what the
-- inline UNIQUE did. This only succeeds while no two sites share a hostname,
-- which the Up section above stopped guaranteeing.
CREATE UNIQUE INDEX idx_sites_hostname ON sites(hostname);
