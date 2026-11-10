-- +goose Up
-- ntx schema for web-managed sites, people and SSO. This replaces the
-- earlier 001-003 migrations outright: there is no upgrade path, and
-- db.Open refuses a database that recorded any version below 100.

CREATE TABLE users (
    id                INTEGER  PRIMARY KEY AUTOINCREMENT,
    email             TEXT     UNIQUE,             -- lowercase; NULL only for root
    name              TEXT     NOT NULL DEFAULT '',
    is_root           INTEGER  NOT NULL DEFAULT 0,
    is_platform_admin INTEGER  NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL
);
CREATE UNIQUE INDEX users_one_root ON users(is_root) WHERE is_root = 1;

CREATE TABLE identities (
    id            INTEGER  PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issuer        TEXT     NOT NULL,               -- the configured issuer, never the token's iss
    subject       TEXT     NOT NULL,
    email_at_link TEXT     NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL,
    UNIQUE (issuer, subject)
);

CREATE TABLE sites (
    id                         INTEGER  PRIMARY KEY AUTOINCREMENT,
    key                        TEXT     NOT NULL UNIQUE,
    hostname                   TEXT     NOT NULL UNIQUE,      -- lowercase
    name                       TEXT     NOT NULL,
    description                TEXT     NOT NULL DEFAULT '',
    about_slug                 TEXT     NOT NULL DEFAULT 'about',
    theme                      TEXT     NOT NULL,
    provider_type              TEXT     NOT NULL DEFAULT '',  -- '', 'google' or 'auth0'
    provider_client_id         TEXT     NOT NULL DEFAULT '',
    provider_client_secret_enc BLOB,
    provider_domain            TEXT     NOT NULL DEFAULT '',
    created_at                 DATETIME NOT NULL,
    updated_at                 DATETIME NOT NULL
);

CREATE TABLE memberships (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    site_id     INTEGER  NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    role        TEXT     NOT NULL CHECK (role IN ('admin', 'contributor')),
    can_publish INTEGER  NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL,
    UNIQUE (user_id, site_id)
);

CREATE TABLE invites (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    site_id     INTEGER  NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    email       TEXT     NOT NULL,                 -- lowercase
    role        TEXT     NOT NULL CHECK (role IN ('admin', 'contributor')),
    can_publish INTEGER  NOT NULL DEFAULT 0,
    token_hash  TEXT     NOT NULL UNIQUE,
    created_by  INTEGER  REFERENCES users(id) ON DELETE SET NULL,
    expires_at  DATETIME NOT NULL,
    used_at     DATETIME,
    created_at  DATETIME NOT NULL
);
CREATE UNIQUE INDEX invites_one_pending ON invites(site_id, email) WHERE used_at IS NULL;

CREATE TABLE sessions (
    id               INTEGER  PRIMARY KEY AUTOINCREMENT,
    token_hash       TEXT     NOT NULL UNIQUE,
    user_id          INTEGER  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    site_id          INTEGER  REFERENCES sites(id) ON DELETE CASCADE,  -- NULL: root on the platform host
    root_hash_digest TEXT,
    created_at       DATETIME NOT NULL,
    expires_at       DATETIME NOT NULL,
    last_seen_at     DATETIME NOT NULL
);

CREATE TABLE posts (
    id                 INTEGER  PRIMARY KEY AUTOINCREMENT,
    site_id            INTEGER  NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    author_id          INTEGER  REFERENCES users(id) ON DELETE SET NULL,
    slug               TEXT     NOT NULL,
    title              TEXT     NOT NULL,
    body               TEXT     NOT NULL,
    published          INTEGER  NOT NULL DEFAULT 0,
    first_published_at DATETIME,
    created_at         DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL,
    content_updated_at DATETIME NOT NULL, -- title, body or tags last changed
    UNIQUE (site_id, slug)
);

CREATE TABLE post_revisions (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    post_id         INTEGER  NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
    author_id       INTEGER  REFERENCES users(id) ON DELETE SET NULL,
    title           TEXT     NOT NULL,
    body            TEXT     NOT NULL,
    tags            TEXT     NOT NULL DEFAULT '[]', -- JSON array
    base_updated_at DATETIME NOT NULL,
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL
);

CREATE TABLE tags (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name    TEXT    NOT NULL,
    UNIQUE (site_id, name)
);

CREATE TABLE post_tags (
    post_id INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);

CREATE TABLE sections (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id  INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    slug     TEXT    NOT NULL,
    title    TEXT    NOT NULL DEFAULT '',
    vars     TEXT    NOT NULL DEFAULT '{}',   -- JSON object
    position INTEGER NOT NULL,
    UNIQUE (site_id, slug)
);

CREATE TABLE section_pages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    section_id INTEGER NOT NULL REFERENCES sections(id) ON DELETE CASCADE,
    slug       TEXT    NOT NULL,
    title      TEXT    NOT NULL DEFAULT '',
    vars       TEXT    NOT NULL DEFAULT '{}', -- JSON object
    position   INTEGER NOT NULL,
    UNIQUE (section_id, slug)
);

-- +goose Down
DROP TABLE section_pages;
DROP TABLE sections;
DROP TABLE post_tags;
DROP TABLE tags;
DROP TABLE post_revisions;
DROP TABLE posts;
DROP TABLE sessions;
DROP TABLE invites;
DROP TABLE memberships;
DROP TABLE sites;
DROP TABLE identities;
DROP TABLE users;
