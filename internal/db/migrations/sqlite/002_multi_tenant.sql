-- +goose Up

CREATE TABLE IF NOT EXISTS sites (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    key      TEXT    NOT NULL UNIQUE,
    hostname TEXT    NOT NULL UNIQUE
);

-- Upgrade path: any pre-existing posts/tags rows (from before multi-tenancy)
-- are backfilled onto this placeholder site. An upgrading operator's ntx.toml
-- must define a [[site]] block with key = "default" so EnsureSites reconciles
-- this row's hostname to their real site_url on next boot.
INSERT INTO sites (key, hostname) VALUES ('default', 'default');

-- Rebuild posts, tags, AND post_tags. All three are recreated before any
-- DROP, and post_tags (the child) is dropped FIRST -- dropping a parent while
-- post_tags still references it would cascade-delete every association.
CREATE TABLE posts_new (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    site_id     INTEGER  NOT NULL REFERENCES sites(id),
    slug        TEXT     NOT NULL,
    title       TEXT     NOT NULL,
    body        TEXT     NOT NULL,
    published   INTEGER  NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);
INSERT INTO posts_new (id, site_id, slug, title, body, published, created_at, updated_at)
    SELECT id, (SELECT id FROM sites WHERE key = 'default'), slug, title, body, published, created_at, updated_at
    FROM posts;

CREATE TABLE tags_new (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id INTEGER NOT NULL REFERENCES sites(id),
    name    TEXT    NOT NULL
);
INSERT INTO tags_new (id, site_id, name)
    SELECT id, (SELECT id FROM sites WHERE key = 'default'), name
    FROM tags;

CREATE TABLE post_tags_new (
    post_id INTEGER NOT NULL REFERENCES posts_new(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags_new(id)  ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);
INSERT INTO post_tags_new (post_id, tag_id)
    SELECT post_id, tag_id FROM post_tags;

DROP TABLE post_tags;
DROP TABLE posts;
DROP TABLE tags;

ALTER TABLE posts_new     RENAME TO posts;
ALTER TABLE tags_new      RENAME TO tags;
ALTER TABLE post_tags_new RENAME TO post_tags;

CREATE UNIQUE INDEX idx_posts_site_slug ON posts(site_id, slug);
CREATE UNIQUE INDEX idx_tags_site_name  ON tags(site_id, name);

-- +goose Down

DROP INDEX IF EXISTS idx_tags_site_name;
DROP INDEX IF EXISTS idx_posts_site_slug;

CREATE TABLE posts_old (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    slug        TEXT     NOT NULL UNIQUE,
    title       TEXT     NOT NULL,
    body        TEXT     NOT NULL,
    published   INTEGER  NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);
INSERT INTO posts_old (id, slug, title, body, published, created_at, updated_at)
    SELECT id, slug, title, body, published, created_at, updated_at FROM posts;

CREATE TABLE tags_old (
    id   INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT    NOT NULL UNIQUE
);
INSERT INTO tags_old (id, name) SELECT id, name FROM tags;

CREATE TABLE post_tags_old (
    post_id INTEGER NOT NULL REFERENCES posts_old(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags_old(id)  ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);
INSERT INTO post_tags_old (post_id, tag_id) SELECT post_id, tag_id FROM post_tags;

DROP TABLE post_tags;
DROP TABLE posts;
DROP TABLE tags;

ALTER TABLE posts_old     RENAME TO posts;
ALTER TABLE tags_old      RENAME TO tags;
ALTER TABLE post_tags_old RENAME TO post_tags;

DROP TABLE IF EXISTS sites;
