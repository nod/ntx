package models

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Post struct {
	ID        int64
	Slug      string
	Title     string
	Body      string
	Published bool
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type PostStore struct {
	db *sql.DB
}

func NewPostStore(db *sql.DB) *PostStore {
	return &PostStore{db: db}
}

func (s *PostStore) List(publishedOnly bool) ([]Post, error) {
	q := `SELECT p.id, p.slug, p.title, p.body, p.published, p.created_at, p.updated_at
		FROM posts p`
	if publishedOnly {
		q += ` WHERE p.published = 1`
	}
	q += ` ORDER BY p.created_at DESC`

	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		var createdAt, updatedAt int64
		var pub int
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Body, &pub, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.Published = pub == 1
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range posts {
		tags, err := s.tagsForPost(posts[i].ID)
		if err != nil {
			return nil, err
		}
		posts[i].Tags = tags
	}

	return posts, nil
}

func (s *PostStore) ListByTag(tag string) ([]Post, error) {
	q := `SELECT p.id, p.slug, p.title, p.body, p.published, p.created_at, p.updated_at
		FROM posts p
		JOIN post_tags pt ON pt.post_id = p.id
		JOIN tags t ON t.id = pt.tag_id
		WHERE p.published = 1 AND t.name = ?
		ORDER BY p.created_at DESC`

	rows, err := s.db.Query(q, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		var createdAt, updatedAt int64
		var pub int
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Body, &pub, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		p.Published = pub == 1
		p.CreatedAt = time.Unix(createdAt, 0)
		p.UpdatedAt = time.Unix(updatedAt, 0)
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range posts {
		tags, err := s.tagsForPost(posts[i].ID)
		if err != nil {
			return nil, err
		}
		posts[i].Tags = tags
	}

	return posts, nil
}

func (s *PostStore) GetBySlug(slug string) (*Post, error) {
	q := `SELECT id, slug, title, body, published, created_at, updated_at FROM posts WHERE slug = ?`
	var p Post
	var createdAt, updatedAt int64
	var pub int
	err := s.db.QueryRow(q, slug).Scan(&p.ID, &p.Slug, &p.Title, &p.Body, &pub, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Published = pub == 1
	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updatedAt, 0)

	tags, err := s.tagsForPost(p.ID)
	if err != nil {
		return nil, err
	}
	p.Tags = tags

	return &p, nil
}

func (s *PostStore) GetByID(id int64) (*Post, error) {
	q := `SELECT id, slug, title, body, published, created_at, updated_at FROM posts WHERE id = ?`
	var p Post
	var createdAt, updatedAt int64
	var pub int
	err := s.db.QueryRow(q, id).Scan(&p.ID, &p.Slug, &p.Title, &p.Body, &pub, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Published = pub == 1
	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updatedAt, 0)

	tags, err := s.tagsForPost(p.ID)
	if err != nil {
		return nil, err
	}
	p.Tags = tags

	return &p, nil
}

func (s *PostStore) Create(p *Post) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	res, err := tx.Exec(
		`INSERT INTO posts (slug, title, body, published, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Slug, p.Title, p.Body, boolToInt(p.Published), now, now,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	p.ID = id
	if err := s.syncTagsTx(tx, p.ID, p.Tags); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostStore) Update(p *Post) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	if _, err := tx.Exec(
		`UPDATE posts SET slug=?, title=?, body=?, published=?, updated_at=? WHERE id=?`,
		p.Slug, p.Title, p.Body, boolToInt(p.Published), now, p.ID,
	); err != nil {
		return err
	}
	if err := s.syncTagsTx(tx, p.ID, p.Tags); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostStore) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM posts WHERE id = ?`, id)
	return err
}

func (s *PostStore) AllTags() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

func (s *PostStore) Stats() (total, published, drafts, tagCount int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(published),0), COUNT(*)-COALESCE(SUM(published),0) FROM posts`).
		Scan(&total, &published, &drafts)
	if err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM tags`).Scan(&tagCount)
	return
}

func (s *PostStore) tagsForPost(postID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT t.name FROM tags t JOIN post_tags pt ON pt.tag_id = t.id WHERE pt.post_id = ? ORDER BY t.name`,
		postID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

type dbExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

func (s *PostStore) syncTagsTx(tx dbExecer, postID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM post_tags WHERE post_id = ?`, postID); err != nil {
		return err
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags (name) VALUES (?)`, tag); err != nil {
			return fmt.Errorf("inserting tag %q: %w", tag, err)
		}
		var tagID int64
		if err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, tag).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO post_tags (post_id, tag_id) VALUES (?, ?)`, postID, tagID); err != nil {
			return err
		}
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
