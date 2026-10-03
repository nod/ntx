-- +goose Up

CREATE TABLE IF NOT EXISTS sites (
    id       BIGSERIAL PRIMARY KEY,
    key      TEXT      NOT NULL UNIQUE,
    hostname TEXT      NOT NULL UNIQUE
);

INSERT INTO sites (key, hostname) VALUES ('default', 'default');

ALTER TABLE posts ADD COLUMN site_id BIGINT REFERENCES sites(id);
UPDATE posts SET site_id = (SELECT id FROM sites WHERE key = 'default');
ALTER TABLE posts ALTER COLUMN site_id SET NOT NULL;
ALTER TABLE posts DROP CONSTRAINT posts_slug_key;
CREATE UNIQUE INDEX idx_posts_site_slug ON posts(site_id, slug);

ALTER TABLE tags ADD COLUMN site_id BIGINT REFERENCES sites(id);
UPDATE tags SET site_id = (SELECT id FROM sites WHERE key = 'default');
ALTER TABLE tags ALTER COLUMN site_id SET NOT NULL;
ALTER TABLE tags DROP CONSTRAINT tags_name_key;
CREATE UNIQUE INDEX idx_tags_site_name ON tags(site_id, name);

-- +goose Down

DROP INDEX IF EXISTS idx_tags_site_name;
DROP INDEX IF EXISTS idx_posts_site_slug;

ALTER TABLE tags ADD CONSTRAINT tags_name_key UNIQUE (name);
ALTER TABLE tags DROP COLUMN site_id;

ALTER TABLE posts ADD CONSTRAINT posts_slug_key UNIQUE (slug);
ALTER TABLE posts DROP COLUMN site_id;

DROP TABLE IF EXISTS sites;
