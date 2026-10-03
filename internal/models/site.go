package models

import (
	"gorm.io/gorm"

	"github.com/nod/ntx/internal/config"
)

// dbSite is the internal GORM model. TableName ensures GORM uses "sites".
type dbSite struct {
	ID       uint   `gorm:"primarykey;autoIncrement"`
	Key      string `gorm:"uniqueIndex;not null"`
	Hostname string `gorm:"uniqueIndex;not null"`
}

func (dbSite) TableName() string { return "sites" }

// EnsureSites upserts each configured site into the sites table by key,
// keeping hostname in sync with config, and writes the DB-assigned ID
// back onto each config.Site so the rest of the app can use it as the
// scoping key for posts/tags/sessions.
func EnsureSites(db *gorm.DB, sites []config.Site) error {
	for i := range sites {
		var r dbSite
		err := db.Where(dbSite{Key: sites[i].Key}).
			Assign(dbSite{Hostname: sites[i].SiteURL}).
			FirstOrCreate(&r).Error
		if err != nil {
			return err
		}
		sites[i].ID = int64(r.ID)
	}
	return nil
}
