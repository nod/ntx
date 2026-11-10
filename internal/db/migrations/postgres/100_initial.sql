-- +goose Up
-- ntx schema for web-managed sites, people and SSO. This replaces the
-- earlier 001-003 migrations outright: there is no upgrade path, and
-- db.Open refuses a database that recorded any version below 100.

CREATE TABLE users (
    id                BIGSERIAL   PRIMARY KEY,
    email             TEXT        UNIQUE,          -- lowercase; NULL only for root
    name              TEXT        NOT NULL DEFAULT '',
    is_root           BOOLEAN     NOT NULL DEFAULT FALSE,
    is_platform_admin BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX users_one_root ON users(is_root) WHERE is_root;

CREATE TABLE identities (
    id            BIGSERIAL   PRIMARY KEY,
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issuer        TEXT        NOT NULL,            -- the configured issuer, never the token's iss
    subject       TEXT        NOT NULL,
    email_at_link TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (issuer, subject)
);

CREATE TABLE sites (
    id                         BIGSERIAL   PRIMARY KEY,
    key                        TEXT        NOT NULL UNIQUE,
    hostname                   TEXT        NOT NULL UNIQUE,      -- lowercase
    name                       TEXT        NOT NULL,
    description                TEXT        NOT NULL DEFAULT '',
    about_slug                 TEXT        NOT NULL DEFAULT 'about',
    theme                      TEXT        NOT NULL,
    provider_type              TEXT        NOT NULL DEFAULT '',  -- '', 'google' or 'auth0'
    provider_client_id         TEXT        NOT NULL DEFAULT '',
    provider_client_secret_enc BYTEA,
    provider_domain            TEXT        NOT NULL DEFAULT '',
    created_at                 TIMESTAMPTZ NOT NULL,
    updated_at                 TIMESTAMPTZ NOT NULL
);

CREATE TABLE memberships (
    id          BIGSERIAL   PRIMARY KEY,
    user_id     BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    site_id     BIGINT      NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    role        TEXT        NOT NULL CHECK (role IN ('admin', 'contributor')),
    can_publish BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL,
    UNIQUE (user_id, site_id)
);

CREATE TABLE invites (
    id          BIGSERIAL   PRIMARY KEY,
    site_id     BIGINT      NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    email       TEXT        NOT NULL,              -- lowercase
    role        TEXT        NOT NULL CHECK (role IN ('admin', 'contributor')),
    can_publish BOOLEAN     NOT NULL DEFAULT FALSE,
    token_hash  TEXT        NOT NULL UNIQUE,
    created_by  BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX invites_one_pending ON invites(site_id, email) WHERE used_at IS NULL;

CREATE TABLE sessions (
    id               BIGSERIAL   PRIMARY KEY,
    token_hash       TEXT        NOT NULL UNIQUE,
    user_id          BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    site_id          BIGINT      REFERENCES sites(id) ON DELETE CASCADE,  -- NULL: root on the platform host
    root_hash_digest TEXT,
    created_at       TIMESTAMPTZ NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    last_seen_at     TIMESTAMPTZ NOT NULL
);

CREATE TABLE posts (
    id                 BIGSERIAL   PRIMARY KEY,
    site_id            BIGINT      NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    author_id          BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    slug               TEXT        NOT NULL,
    title              TEXT        NOT NULL,
    body               TEXT        NOT NULL,
    published          BOOLEAN     NOT NULL DEFAULT FALSE,
    first_published_at TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL,
    content_updated_at TIMESTAMPTZ NOT NULL, -- title, body or tags last changed
    UNIQUE (site_id, slug)
);

CREATE TABLE post_revisions (
    id              BIGSERIAL   PRIMARY KEY,
    post_id         BIGINT      NOT NULL UNIQUE REFERENCES posts(id) ON DELETE CASCADE,
    author_id       BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    title           TEXT        NOT NULL,
    body            TEXT        NOT NULL,
    tags            JSONB       NOT NULL DEFAULT '[]',
    base_updated_at TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL
);

CREATE TABLE tags (
    id      BIGSERIAL PRIMARY KEY,
    site_id BIGINT    NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    name    TEXT      NOT NULL,
    UNIQUE (site_id, name)
);

CREATE TABLE post_tags (
    post_id BIGINT NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id  BIGINT NOT NULL REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);

CREATE TABLE sections (
    id       BIGSERIAL PRIMARY KEY,
    site_id  BIGINT    NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    slug     TEXT      NOT NULL,
    title    TEXT      NOT NULL DEFAULT '',
    vars     JSONB     NOT NULL DEFAULT '{}',
    position INTEGER   NOT NULL,
    UNIQUE (site_id, slug)
);

CREATE TABLE section_pages (
    id         BIGSERIAL PRIMARY KEY,
    section_id BIGINT    NOT NULL REFERENCES sections(id) ON DELETE CASCADE,
    slug       TEXT      NOT NULL,
    title      TEXT      NOT NULL DEFAULT '',
    vars       JSONB     NOT NULL DEFAULT '{}',
    position   INTEGER   NOT NULL,
    UNIQUE (section_id, slug)
);

-- +goose Down
DROP TABLE IF EXISTS section_pages;
DROP TABLE IF EXISTS sections;
DROP TABLE IF EXISTS post_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS post_revisions;
DROP TABLE IF EXISTS posts;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS invites;
DROP TABLE IF EXISTS memberships;
DROP TABLE IF EXISTS sites;
DROP TABLE IF EXISTS identities;
DROP TABLE IF EXISTS users;
