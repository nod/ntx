package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/nod/ntx/internal/validate"
)

// Site is one tenant, as managed in the platform views.
type Site struct {
	ID               int64
	Key              string // immutable after creation; names the generate output dir
	Hostname         string // lowercase; the Host routing key
	Name             string
	Description      string
	AboutSlug        string
	Theme            string
	ProviderType     string // '', 'google' or 'auth0' (configured in phase 2)
	ProviderClientID string
	ProviderDomain   string
	Sections         []Section
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Section is a URL namespace (/<slug>/<page>) backed by theme templates.
type Section struct {
	Slug  string         `json:"slug"`
	Title string         `json:"title,omitempty"`
	Vars  map[string]any `json:"vars,omitempty"`
	Pages []SectionPage  `json:"pages,omitempty"`
}

type SectionPage struct {
	Slug  string         `json:"slug"`
	Title string         `json:"title,omitempty"`
	Vars  map[string]any `json:"vars,omitempty"`
}

// ValidationError is a problem with what someone entered. Handlers show
// its message on the form; any other error is a server error.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

type dbSite struct {
	ID               uint `gorm:"primarykey"`
	Key              string
	Hostname         string
	Name             string
	Description      string
	AboutSlug        string
	Theme            string
	ProviderType     string
	ProviderClientID string
	ProviderDomain   string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (dbSite) TableName() string { return "sites" }

type dbSection struct {
	ID       uint `gorm:"primarykey"`
	SiteID   int64
	Slug     string
	Title    string
	Vars     string
	Position int
}

func (dbSection) TableName() string { return "sections" }

type dbSectionPage struct {
	ID        uint `gorm:"primarykey"`
	SectionID uint
	Slug      string
	Title     string
	Vars      string
	Position  int
}

func (dbSectionPage) TableName() string { return "section_pages" }

type SiteStore struct{ db *gorm.DB }

func NewSiteStore(db *gorm.DB) *SiteStore { return &SiteStore{db: db} }

// List returns every site ordered by key, with sections and pages loaded in
// position order.
func (s *SiteStore) List() ([]Site, error) {
	var rows []dbSite
	if err := s.db.Order("key").Find(&rows).Error; err != nil {
		return nil, err
	}
	var secRows []dbSection
	if err := s.db.Order("site_id, position").Find(&secRows).Error; err != nil {
		return nil, err
	}
	var pageRows []dbSectionPage
	if err := s.db.Order("section_id, position").Find(&pageRows).Error; err != nil {
		return nil, err
	}

	pagesBySection := map[uint][]SectionPage{}
	for _, p := range pageRows {
		vars, err := decodeVars(p.Vars)
		if err != nil {
			return nil, fmt.Errorf("section page %d vars: %w", p.ID, err)
		}
		pagesBySection[p.SectionID] = append(pagesBySection[p.SectionID], SectionPage{Slug: p.Slug, Title: p.Title, Vars: vars})
	}
	secsBySite := map[int64][]Section{}
	for _, r := range secRows {
		vars, err := decodeVars(r.Vars)
		if err != nil {
			return nil, fmt.Errorf("section %d vars: %w", r.ID, err)
		}
		secsBySite[r.SiteID] = append(secsBySite[r.SiteID], Section{Slug: r.Slug, Title: r.Title, Vars: vars, Pages: pagesBySection[r.ID]})
	}

	sites := make([]Site, len(rows))
	for i, r := range rows {
		sites[i] = Site{
			ID: int64(r.ID), Key: r.Key, Hostname: r.Hostname, Name: r.Name,
			Description: r.Description, AboutSlug: r.AboutSlug, Theme: r.Theme,
			ProviderType: r.ProviderType, ProviderClientID: r.ProviderClientID, ProviderDomain: r.ProviderDomain,
			Sections: secsBySite[int64(r.ID)], CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
	}
	return sites, nil
}

// normalize lowercases the hostname, fills defaults, and validates the
// fields every site write shares. excludeID is the site being updated (0
// on create), so a site doesn't conflict with itself.
func (s *SiteStore) normalize(site *Site, platformHostname string, excludeID int64) error {
	site.Hostname = strings.ToLower(strings.TrimSpace(site.Hostname))
	site.Name = strings.TrimSpace(site.Name)
	if site.Name == "" {
		site.Name = site.Key
	}
	site.AboutSlug = strings.TrimSpace(site.AboutSlug)
	if site.AboutSlug == "" {
		site.AboutSlug = "about"
	}
	if err := validate.Hostname(site.Hostname); err != nil {
		return invalid("%s", err)
	}
	if site.Hostname == strings.ToLower(platformHostname) {
		return invalid("hostname %q is the platform hostname and can't be used by a site", site.Hostname)
	}
	if err := validate.Key(site.Theme); err != nil {
		return invalid("theme: %s", err)
	}
	var n int64
	if err := s.db.Model(&dbSite{}).Where("hostname = ? AND id <> ?", site.Hostname, excludeID).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return invalid("hostname %q is already used by another site", site.Hostname)
	}
	return nil
}

// Create validates and inserts site, setting its ID.
func (s *SiteStore) Create(site *Site, platformHostname string) error {
	site.Key = strings.TrimSpace(site.Key)
	if err := validate.Key(site.Key); err != nil {
		return invalid("key: %s", err)
	}
	var n int64
	if err := s.db.Model(&dbSite{}).Where("key = ?", site.Key).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return invalid("a site with key %q already exists", site.Key)
	}
	if err := s.normalize(site, platformHostname, 0); err != nil {
		return err
	}
	r := dbSite{
		Key: site.Key, Hostname: site.Hostname, Name: site.Name, Description: site.Description,
		AboutSlug: site.AboutSlug, Theme: site.Theme,
	}
	if err := s.db.Create(&r).Error; err != nil {
		return err
	}
	site.ID = int64(r.ID)
	site.CreatedAt, site.UpdatedAt = r.CreatedAt, r.UpdatedAt
	return nil
}

// Update saves the editable fields of the site with site.ID. The key is
// immutable and site.Key is ignored. Changing the hostname deletes the
// site's sessions: cookies are bound to the old host anyway.
func (s *SiteStore) Update(site *Site, platformHostname string) error {
	var cur dbSite
	if err := s.db.First(&cur, site.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return invalid("site %d does not exist", site.ID)
		}
		return err
	}
	site.Key = cur.Key
	if err := s.normalize(site, platformHostname, site.ID); err != nil {
		return err
	}
	// Read before Updates: GORM writes the new values back into cur.
	hostnameChanged := cur.Hostname != site.Hostname
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&cur).Updates(map[string]any{
			"hostname":    site.Hostname,
			"name":        site.Name,
			"description": site.Description,
			"about_slug":  site.AboutSlug,
			"theme":       site.Theme,
			"updated_at":  time.Now(),
		}).Error; err != nil {
			return err
		}
		if hostnameChanged {
			return tx.Exec("DELETE FROM sessions WHERE site_id = ?", site.ID).Error
		}
		return nil
	})
}

// Delete removes a site; the schema cascades to everything it owns.
func (s *SiteStore) Delete(id int64) error {
	res := s.db.Delete(&dbSite{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return invalid("site %d does not exist", id)
	}
	return nil
}

// SetSections replaces a site's sections and pages. It validates everything
// before writing, so a rejected set leaves the stored sections untouched.
func (s *SiteStore) SetSections(siteID int64, secs []Section) error {
	seen := map[string]bool{}
	for _, sec := range secs {
		if err := validate.SectionSlug(sec.Slug); err != nil {
			return invalid("%s", err)
		}
		if seen[sec.Slug] {
			return invalid("section slug %q is used twice", sec.Slug)
		}
		seen[sec.Slug] = true
		pageSeen := map[string]bool{}
		for _, pg := range sec.Pages {
			if err := validate.Segment(pg.Slug); err != nil {
				return invalid("section %q page %s", sec.Slug, err)
			}
			if pageSeen[pg.Slug] {
				return invalid("section %q has page slug %q twice", sec.Slug, pg.Slug)
			}
			pageSeen[pg.Slug] = true
		}
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		// section_pages cascade with their sections.
		if err := tx.Where("site_id = ?", siteID).Delete(&dbSection{}).Error; err != nil {
			return err
		}
		for i, sec := range secs {
			vars, err := encodeVars(sec.Vars)
			if err != nil {
				return err
			}
			row := dbSection{SiteID: siteID, Slug: sec.Slug, Title: sec.Title, Vars: vars, Position: i}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			for j, pg := range sec.Pages {
				pv, err := encodeVars(pg.Vars)
				if err != nil {
					return err
				}
				if err := tx.Create(&dbSectionPage{SectionID: row.ID, Slug: pg.Slug, Title: pg.Title, Vars: pv, Position: j}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func encodeVars(v map[string]any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func decodeVars(s string) (map[string]any, error) {
	if s == "" || s == "{}" {
		return nil, nil
	}
	var v map[string]any
	err := json.Unmarshal([]byte(s), &v)
	return v, err
}
