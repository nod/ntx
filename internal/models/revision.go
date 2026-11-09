package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	// ErrNotLiveOnce: only a post that has been published can have
	// pending changes; edits to a never-published draft apply directly.
	ErrNotLiveOnce = errors.New("post has never been published")
	// ErrRevisionChanged: the pending changes the caller looked at are
	// no longer the ones stored -- saved again, published or discarded.
	ErrRevisionChanged = errors.New("pending changes were saved again, published or discarded")
	// ErrStale: the post was edited directly after the pending changes
	// were started, and publishing them would overwrite those edits.
	ErrStale = errors.New("post was edited after these changes were started")
)

// Revision is a post's pending changes: a contributor's edit to a
// live-once post, waiting for a site admin. It never carries a slug.
type Revision struct {
	ID       int64
	PostID   int64
	AuthorID *int64
	Title    string
	Body     string
	Tags     []string
	// BaseUpdatedAt is the post's UpdatedAt when the contributor started
	// editing; kept when the revision is saved again.
	BaseUpdatedAt time.Time
	CreatedAt     time.Time
	// UpdatedAt identifies this version of the revision: publish and
	// discard name the version they were shown.
	UpdatedAt time.Time
}

// Stale reports whether p was changed since the revision was started.
// Both times must be values read back from the database.
func (r *Revision) Stale(p *Post) bool { return !p.UpdatedAt.Equal(r.BaseUpdatedAt) }

type dbRevision struct {
	ID            uint `gorm:"primarykey;autoIncrement"`
	PostID        int64
	AuthorID      *int64
	Title         string
	Body          string
	Tags          string // JSON array
	BaseUpdatedAt time.Time
	// Written explicitly (see revisionNow), never by GORM.
	CreatedAt time.Time `gorm:"autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"autoUpdateTime:false"`
}

func (dbRevision) TableName() string { return "post_revisions" }

// revisionNow is truncated to microseconds, so the value Go holds is the
// value both SQLite and Postgres store, and a version read back compares
// Equal to the one written.
func revisionNow() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func toRevision(r dbRevision) (*Revision, error) {
	var tags []string
	if err := json.Unmarshal([]byte(r.Tags), &tags); err != nil {
		return nil, fmt.Errorf("revision %d tags: %w", r.ID, err)
	}
	return &Revision{
		ID: int64(r.ID), PostID: r.PostID, AuthorID: r.AuthorID,
		Title: r.Title, Body: r.Body, Tags: tags,
		BaseUpdatedAt: r.BaseUpdatedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

// postInSite loads postID if it belongs to this store's site.
func (s *PostStore) postInSite(tx *gorm.DB, postID int64) (*dbPost, error) {
	var p dbPost
	err := tx.Where("site_id = ?", s.siteID).First(&p, postID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("post %d not found in site %d", postID, s.siteID)
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// pendingOn loads postID's revision through tx, confined to this site;
// nil when there is none.
func (s *PostStore) pendingOn(tx *gorm.DB, postID int64) (*dbRevision, error) {
	var r dbRevision
	err := tx.Joins("JOIN posts ON posts.id = post_revisions.post_id AND posts.site_id = ?", s.siteID).
		Where("post_revisions.post_id = ?", postID).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetPending returns postID's pending changes, or nil if it has none.
func (s *PostStore) GetPending(postID int64) (*Revision, error) {
	r, err := s.pendingOn(s.db, postID)
	if err != nil || r == nil {
		return nil, err
	}
	return toRevision(*r)
}

// PendingIDs lists the ids of this site's posts that have pending changes.
func (s *PostStore) PendingIDs() (map[int64]bool, error) {
	var ids []int64
	err := s.db.Model(&dbRevision{}).
		Joins("JOIN posts ON posts.id = post_revisions.post_id AND posts.site_id = ?", s.siteID).
		Pluck("post_revisions.post_id", &ids).Error
	if err != nil {
		return nil, err
	}
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m, nil
}

// SavePending stores authorID's edit to a live-once post as its pending
// revision, replacing any earlier one. base is the post's UpdatedAt when
// the edit started; it is recorded only when the revision is created.
// The post itself is not touched.
func (s *PostStore) SavePending(postID, authorID int64, title, body string, tags []string, base time.Time) (*Revision, error) {
	clean := []string{}
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			clean = append(clean, t)
		}
	}
	tagJSON, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	var saved dbRevision
	err = s.db.Transaction(func(tx *gorm.DB) error {
		p, err := s.postInSite(tx, postID)
		if err != nil {
			return err
		}
		if p.FirstPublishedAt == nil {
			return ErrNotLiveOnce
		}
		existing, err := s.pendingOn(tx, postID)
		if err != nil {
			return err
		}
		now := revisionNow()
		if existing == nil {
			saved = dbRevision{
				PostID: postID, AuthorID: &authorID, Title: title, Body: body, Tags: string(tagJSON),
				BaseUpdatedAt: base, CreatedAt: now, UpdatedAt: now,
			}
			return tx.Create(&saved).Error
		}
		// UpdatedAt is the revision's version: it must move on every save.
		if !now.After(existing.UpdatedAt) {
			now = existing.UpdatedAt.Add(time.Microsecond)
		}
		saved = *existing
		saved.AuthorID, saved.Title, saved.Body, saved.Tags, saved.UpdatedAt = &authorID, title, body, string(tagJSON), now
		return tx.Model(&dbRevision{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"author_id": authorID, "title": title, "body": body, "tags": string(tagJSON), "updated_at": now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return toRevision(saved)
}

// PublishPending copies postID's pending title, body and tags onto the
// post and deletes the revision, in one transaction. seen is the
// revision's UpdatedAt as shown to the admin: if it has changed since,
// nothing happens (ErrRevisionChanged). If the post was edited after the
// revision was started, nothing happens unless allowStale (ErrStale).
// Slug and published state are never changed.
func (s *PostStore) PublishPending(postID int64, seen time.Time, allowStale bool) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		p, err := s.postInSite(tx, postID)
		if err != nil {
			return err
		}
		r, err := s.pendingOn(tx, postID)
		if err != nil {
			return err
		}
		if r == nil || !r.UpdatedAt.Equal(seen) {
			return ErrRevisionChanged
		}
		if !allowStale && !p.UpdatedAt.Equal(r.BaseUpdatedAt) {
			return ErrStale
		}
		rev, err := toRevision(*r)
		if err != nil {
			return err
		}
		tags, err := s.resolveTagsOn(tx, rev.Tags)
		if err != nil {
			return err
		}
		target := dbPost{ID: p.ID}
		if err := tx.Model(&target).Where("site_id = ?", s.siteID).Updates(map[string]any{"title": rev.Title, "body": rev.Body}).Error; err != nil {
			return err
		}
		if err := tx.Model(&target).Association("Tags").Replace(tags); err != nil {
			return err
		}
		return tx.Delete(&dbRevision{}, r.ID).Error
	})
}

// DiscardPending deletes postID's pending changes if they are still the
// version seen (else ErrRevisionChanged). The post is not touched.
func (s *PostStore) DiscardPending(postID int64, seen time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.postInSite(tx, postID); err != nil {
			return err
		}
		r, err := s.pendingOn(tx, postID)
		if err != nil {
			return err
		}
		if r == nil || !r.UpdatedAt.Equal(seen) {
			return ErrRevisionChanged
		}
		return tx.Delete(&dbRevision{}, r.ID).Error
	})
}
