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

// copySite returns a copy that callers can't use to change the registry,
// down to the vars of every section and page.
func copySite(s models.Site) *models.Site {
	s.Sections = append([]models.Section(nil), s.Sections...)
	for i := range s.Sections {
		sec := &s.Sections[i]
		sec.Vars = copyVars(sec.Vars)
		sec.Pages = append([]models.SectionPage(nil), sec.Pages...)
		for j := range sec.Pages {
			sec.Pages[j].Vars = copyVars(sec.Pages[j].Vars)
		}
	}
	return &s
}

// copyVars deep-copies decoded JSON: maps and lists are copied at every
// depth; strings, numbers, bools and nil are immutable.
func copyVars(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = copyValue(v)
	}
	return out
}

func copyValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		return copyVars(v)
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = copyValue(e)
		}
		return out
	}
	return v
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
