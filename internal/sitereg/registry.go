// Package sitereg keeps the hostname → site map in memory. ntx runs as a
// single instance, so reloading after every site write is enough to keep it
// current.
package sitereg

import (
	"sync"

	"github.com/nod/ntx/internal/models"
)

type Registry struct {
	store  *models.SiteStore
	mu     sync.RWMutex
	all    []models.Site
	byHost map[string]int
	byID   map[int64]int
}

// New loads every site from store.
func New(store *models.SiteStore) (*Registry, error) {
	r := &Registry{store: store}
	return r, r.Reload()
}

// Reload re-reads every site. Call it after any write to sites or sections.
func (r *Registry) Reload() error {
	sites, err := r.store.List()
	if err != nil {
		return err
	}
	byHost := make(map[string]int, len(sites))
	byID := make(map[int64]int, len(sites))
	for i, s := range sites {
		byHost[s.Hostname] = i
		byID[s.ID] = i
	}
	r.mu.Lock()
	r.all, r.byHost, r.byID = sites, byHost, byID
	r.mu.Unlock()
	return nil
}

// copySite returns a copy that callers can't use to change the registry.
func copySite(s models.Site) *models.Site {
	s.Sections = append([]models.Section(nil), s.Sections...)
	for i := range s.Sections {
		s.Sections[i].Pages = append([]models.SectionPage(nil), s.Sections[i].Pages...)
	}
	return &s
}

// ByHost returns the site for a lowercased, port-free hostname, or nil.
func (r *Registry) ByHost(host string) *models.Site {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if i, ok := r.byHost[host]; ok {
		return copySite(r.all[i])
	}
	return nil
}

// ByID returns the site with id, or nil.
func (r *Registry) ByID(id int64) *models.Site {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if i, ok := r.byID[id]; ok {
		return copySite(r.all[i])
	}
	return nil
}

// All returns every site, ordered by key.
func (r *Registry) All() []models.Site {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]models.Site, len(r.all))
	for i, s := range r.all {
		out[i] = *copySite(s)
	}
	return out
}
