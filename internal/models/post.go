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
	Slug      string  `gorm:"uniqueIndex;not null"`
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
	ID   uint   `gorm:"primarykey"`
	Name string `gorm:"uniqueIndex;not null"`
}

func (dbTag) TableName() string { return "tags" }

type PostStore struct {
	db *gorm.DB
}

func NewPostStore(db *gorm.DB) *PostStore {
	return &PostStore{db: db}
}

func (s *PostStore) List(publishedOnly bool) ([]Post, error) {
	var records []dbPost
	q := s.db.Preload("Tags").Order("created_at desc")
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
	err := s.db.Preload("Tags").
		Joins("JOIN post_tags ON post_tags.post_id = posts.id").
		Joins("JOIN tags ON tags.id = post_tags.tag_id AND tags.name = ?", tag).
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
	err := s.db.Preload("Tags").Where("slug = ?", slug).First(&r).Error
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
	err := s.db.Preload("Tags").First(&r, id).Error
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
		if err := tx.Model(&r).Updates(map[string]any{
			"slug":      p.Slug,
			"title":     p.Title,
			"body":      p.Body,
			"published": p.Published,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&r).Association("Tags").Replace(tags)
	})
}

func (s *PostStore) Delete(id int64) error {
	return s.db.Delete(&dbPost{}, id).Error
}

func (s *PostStore) AllTags() ([]string, error) {
	var tags []dbTag
	if err := s.db.Order("name").Find(&tags).Error; err != nil {
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
	if err = s.db.Model(&dbPost{}).Count(&t).Error; err != nil {
		return
	}
	if err = s.db.Model(&dbPost{}).Where("published = ?", true).Count(&p).Error; err != nil {
		return
	}
	if err = s.db.Model(&dbTag{}).Count(&tc).Error; err != nil {
		return
	}
	total = int(t)
	published = int(p)
	drafts = total - published
	tagCount = int(tc)
	return
}

// resolveTags finds or creates each tag by name and returns the dbTag slice.
func (s *PostStore) resolveTags(names []string) ([]dbTag, error) {
	var tags []dbTag
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var t dbTag
		if err := s.db.Where(dbTag{Name: name}).FirstOrCreate(&t).Error; err != nil {
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
