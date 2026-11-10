package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	// BaseUpdatedAt is the post's ContentUpdatedAt when the contributor
	// started editing; kept when the revision is saved again.
	BaseUpdatedAt time.Time
	CreatedAt     time.Time
	// UpdatedAt identifies this version of the revision: publish and
	// discard name the version they were shown.
	UpdatedAt time.Time
}

// Stale reports whether p was changed since the revision was started.
// Both times must be values read back from the database.
func (r *Revision) Stale(p *Post) bool { return !p.ContentUpdatedAt.Equal(r.BaseUpdatedAt) }

type dbRevision struct {
	ID            uint `gorm:"primarykey;autoIncrement"`
	PostID        int64
	AuthorID      *int64
	Title         string
	Body          string
	Tags          string // JSON array
	BaseUpdatedAt time.Time
	// Written explicitly (see dbNow), never by GORM.
	CreatedAt time.Time `gorm:"autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"autoUpdateTime:false"`
}

func (dbRevision) TableName() string { return "post_revisions" }

// dbNow is truncated to microseconds, so the value Go holds is the
// value both SQLite and Postgres store, and a version read back compares
// Equal to the one written.
func dbNow() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

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

// lockPostInSite loads postID, if it belongs to this store's site, and
// locks its row until tx ends. Every write to a post's revision takes this
// lock first, so saves, publishes and discards of the same post run one at
// a time, and a direct edit of the post (which updates the row) cannot land
// between a publish's stale check and its copy. SQLite has no row locks;
// its single connection already runs transactions one at a time.
//
// It first takes a key-share lock on the site's row, which keeps the site
// from being deleted until tx ends. A write under the post lock that adds a
// row pointing at the site (a new tag) needs that lock anyway; taken late,
// it can deadlock with a site delete, which locks the site and then waits
// on the post. Taken first, the delete simply waits. Locks are always
// taken in the order person, site, post.
func (s *PostStore) lockPostInSite(tx *gorm.DB, postID int64) (*dbPost, error) {
	if err := keyShare(tx, &dbSite{}, s.siteID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("post %d not found in site %d", postID, s.siteID)
		}
		return nil, err
	}
	var p dbPost
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("site_id = ?", s.siteID).First(&p, postID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("post %d not found in site %d", postID, s.siteID)
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// keyShare locks the row of model with this id against being deleted (or
// its key changed) until tx ends, and returns gorm.ErrRecordNotFound if it
// is already gone. SQLite ignores the lock, as it does every row lock.
func keyShare(tx *gorm.DB, model any, id int64) error {
	var ids []int64
	err := tx.Model(model).Clauses(clause.Locking{Strength: "KEY SHARE"}).Where("id = ?", id).Pluck("id", &ids).Error
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
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
		// The revision points at its author: lock that row before the
		// post (see lockPostInSite), or deleting the author deadlocks.
		if err := keyShare(tx, &dbUser{}, authorID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("person %d not found", authorID)
			}
			return err
		}
		p, err := s.lockPostInSite(tx, postID)
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
		now := dbNow()
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
// revision was started, nothing happens (ErrStale) unless overwrite is the
// post's current ContentUpdatedAt: the version the admin saw and agreed to
// overwrite. The zero time agrees to nothing. Slug and published state are
// never changed.
func (s *PostStore) PublishPending(postID int64, seen, overwrite time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		p, err := s.lockPostInSite(tx, postID)
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
		if !p.ContentUpdatedAt.Equal(r.BaseUpdatedAt) && !p.ContentUpdatedAt.Equal(overwrite) {
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
		if err := tx.Model(&target).Where("site_id = ?", s.siteID).Updates(map[string]any{"title": rev.Title, "body": rev.Body, "content_updated_at": dbNow()}).Error; err != nil {
			return err
		}
		if err := tx.Model(&target).Association("Tags").Replace(tags); err != nil {
			return err
		}
		return deleteVersion(tx, r)
	})
}

// deleteVersion deletes r only if it is still the version that was checked,
// else ErrRevisionChanged (and the caller's transaction rolls back).
func deleteVersion(tx *gorm.DB, r *dbRevision) error {
	res := tx.Where("updated_at = ?", r.UpdatedAt).Delete(&dbRevision{}, r.ID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrRevisionChanged
	}
	return nil
}

// DiscardPending deletes postID's pending changes if they are still the
// version seen (else ErrRevisionChanged). The post is not touched.
func (s *PostStore) DiscardPending(postID int64, seen time.Time) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.lockPostInSite(tx, postID); err != nil {
			return err
		}
		r, err := s.pendingOn(tx, postID)
		if err != nil {
			return err
		}
		if r == nil || !r.UpdatedAt.Equal(seen) {
			return ErrRevisionChanged
		}
		return deleteVersion(tx, r)
	})
}
