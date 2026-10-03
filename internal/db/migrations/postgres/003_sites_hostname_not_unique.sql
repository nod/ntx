-- +goose Up

-- See the sqlite copy of this migration for why: sites.hostname is never
-- read, site_url uniqueness is already validated in config.Load, and the
-- DB-level UNIQUE only turned ordinary config edits (renaming a key, swapping
-- two sites' site_urls) into opaque startup failures. The constraint name is
-- the one Postgres assigns to the inline UNIQUE in 002's CREATE TABLE.
ALTER TABLE sites DROP CONSTRAINT sites_hostname_key;

-- +goose Down

-- Re-adding UNIQUE(hostname) only succeeds while no two sites share a
-- hostname, which this migration's Up section stopped guaranteeing.
ALTER TABLE sites ADD CONSTRAINT sites_hostname_key UNIQUE (hostname);
