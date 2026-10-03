package models

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Post is the public type. Tags are []string for template/handler compatibility.
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

// dbPost is the internal GORM model. TableName ensures GORM uses "posts".
type dbPost struct {
	ID        uint    `gorm:"primarykey;autoIncrement"`
	SiteID    int64   `gorm:"not null;uniqueIndex:idx_posts_site_slug"`
	Slug      string  `gorm:"not null;uniqueIndex:idx_posts_site_slug"`
	Title     string  `gorm:"not null"`
	Body      string  `gorm:"not null"`
	Published bool    `gorm:"default:false"`
	Tags      []dbTag `gorm:"many2many:post_tags;joinForeignKey:post_id;joinReferences:tag_id"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (dbPost) TableName() string { return "posts" }

// dbTag is the internal GORM model for tags. TableName ensures GORM uses "tags".
type dbTag struct {
	ID     uint   `gorm:"primarykey"`
	SiteID int64  `gorm:"not null;uniqueIndex:idx_tags_site_name"`
	Name   string `gorm:"not null;uniqueIndex:idx_tags_site_name"`
}

func (dbTag) TableName() string { return "tags" }

// PostStore is scoped to one site once ForSite is called. Persisted site
// ids start at 1, so an unscoped store (siteID still 0) matches no real
// site's rows -- but only because every query below spells its site_id
// condition out as SQL. GORM drops zero-valued fields from a struct-based
// Where, so Where(dbTag{SiteID: 0, Name: name}) would emit "name = ?"
// alone and happily return another site's row; see resolveTags.
type PostStore struct {
	db     *gorm.DB
	siteID int64
}

func NewPostStore(db *gorm.DB) *PostStore {
	return &PostStore{db: db}
}

// ForSite returns a copy of the store scoped to siteID. Every query and
// write made through the returned store is confined to that site's rows.
func (s *PostStore) ForSite(siteID int64) *PostStore {
	return &PostStore{db: s.db, siteID: siteID}
}

func (s *PostStore) List(publishedOnly bool) ([]Post, error) {
	var records []dbPost
	q := s.db.Preload("Tags", "site_id = ?", s.siteID).Where("site_id = ?", s.siteID).Order("created_at desc")
	if publishedOnly {
		q = q.Where("published = ?", true)
	}
	if err := q.Find(&records).Error; err != nil {
		return nil, err
	}
	posts := make([]Post, len(records))
	for i, r := range records {
		posts[i] = toPost(r)
	}
	return posts, nil
}

func (s *PostStore) ListByTag(tag string) ([]Post, error) {
	var records []dbPost
	err := s.db.Preload("Tags", "site_id = ?", s.siteID).
		Where("posts.site_id = ?", s.siteID).
		Joins("JOIN post_tags ON post_tags.post_id = posts.id").
		Joins("JOIN tags ON tags.id = post_tags.tag_id AND tags.name = ? AND tags.site_id = ?", tag, s.siteID).
		Where("posts.published = ?", true).
		Order("posts.created_at desc").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	posts := make([]Post, len(records))
	for i, r := range records {
		posts[i] = toPost(r)
	}
	return posts, nil
}

func (s *PostStore) GetBySlug(slug string) (*Post, error) {
	var r dbPost
	err := s.db.Preload("Tags", "site_id = ?", s.siteID).Where("site_id = ? AND slug = ?", s.siteID, slug).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := toPost(r)
	return &p, nil
}

func (s *PostStore) GetByID(id int64) (*Post, error) {
	var r dbPost
	err := s.db.Preload("Tags", "site_id = ?", s.siteID).Where("site_id = ?", s.siteID).First(&r, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := toPost(r)
	return &p, nil
}

func (s *PostStore) Create(p *Post) error {
	if !slugRe.MatchString(p.Slug) {
		return fmt.Errorf("invalid slug %q: must match %s", p.Slug, slugRe)
	}
	tags, err := s.resolveTags(p.Tags)
	if err != nil {
		return err
	}
	r := dbPost{
		SiteID:    s.siteID,
		Slug:      p.Slug,
		Title:     p.Title,
		Body:      p.Body,
		Published: p.Published,
		Tags:      tags,
	}
	if err := s.db.Create(&r).Error; err != nil {
		return err
	}
	p.ID = int64(r.ID)
	p.CreatedAt = r.CreatedAt
	p.UpdatedAt = r.UpdatedAt
	return nil
}

func (s *PostStore) Update(p *Post) error {
	if !slugRe.MatchString(p.Slug) {
		return fmt.Errorf("invalid slug %q: must match %s", p.Slug, slugRe)
	}
	tags, err := s.resolveTags(p.Tags)
	if err != nil {
		return err
	}
	r := dbPost{ID: uint(p.ID)}
	return s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&r).Where("site_id = ?", s.siteID).Updates(map[string]any{
			"slug":      p.Slug,
			"title":     p.Title,
			"body":      p.Body,
			"published": p.Published,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("post %d not found in site %d", p.ID, s.siteID)
		}
		return tx.Model(&r).Association("Tags").Replace(tags)
	})
}

func (s *PostStore) Delete(id int64) error {
	// Matches Update's contract: a cross-site id deletes nothing, and that
	// has to be an error rather than a silent no-op.
	res := s.db.Where("site_id = ?", s.siteID).Delete(&dbPost{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("post %d not found in site %d", id, s.siteID)
	}
	return nil
}

func (s *PostStore) AllTags() ([]string, error) {
	var tags []dbTag
	if err := s.db.Where("site_id = ?", s.siteID).Order("name").Find(&tags).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	return names, nil
}

func (s *PostStore) Stats() (total, published, drafts, tagCount int, err error) {
	var t, p, tc int64
	if err = s.db.Model(&dbPost{}).Where("site_id = ?", s.siteID).Count(&t).Error; err != nil {
		return
	}
	if err = s.db.Model(&dbPost{}).Where("site_id = ? AND published = ?", s.siteID, true).Count(&p).Error; err != nil {
		return
	}
	if err = s.db.Model(&dbTag{}).Where("site_id = ?", s.siteID).Count(&tc).Error; err != nil {
		return
	}
	total = int(t)
	published = int(p)
	drafts = total - published
	tagCount = int(tc)
	return
}

// resolveTags finds or creates each tag by name (scoped to this site) and
// returns the dbTag slice.
//
// The site_id condition is written as explicit SQL rather than as a
// struct-based Where: GORM omits zero-valued struct fields from the
// generated WHERE clause, so an unscoped store would look up tags by name
// alone and attach another site's tag row to this site's post.
func (s *PostStore) resolveTags(names []string) ([]dbTag, error) {
	var tags []dbTag
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var t dbTag
		err := s.db.Where("site_id = ? AND name = ?", s.siteID, name).
			Attrs(dbTag{SiteID: s.siteID, Name: name}).
			FirstOrCreate(&t).Error
		if err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func toPost(r dbPost) Post {
	p := Post{
		ID:        int64(r.ID),
		Slug:      r.Slug,
		Title:     r.Title,
		Body:      r.Body,
		Published: r.Published,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
	for _, t := range r.Tags {
		p.Tags = append(p.Tags, t.Name)
	}
	return p
}
