package models

import (
	"log"

	"gorm.io/gorm"

	"github.com/nod/ntx/internal/config"
)

// dbSite is the internal GORM model. TableName ensures GORM uses "sites".
type dbSite struct {
	ID       uint   `gorm:"primarykey;autoIncrement"`
	Key      string `gorm:"uniqueIndex;not null"`
	Hostname string `gorm:"not null"` // not unique: see migration 003
}

func (dbSite) TableName() string { return "sites" }

// EnsureSites upserts each configured site into the sites table by key,
// keeping hostname in sync with config, and writes the DB-assigned ID
// back onto each config.Site so the rest of the app can use it as the
// scoping key for posts/tags/sessions. It then warns about any site row
// in the database that owns content but matches no configured site.
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
	return warnOrphanedSites(db, sites)
}

// warnOrphanedSites logs a warning for every site row that owns posts or
// tags but matches no [[site]] block in the config, because nothing else
// in the system notices.
//
// The single-site upgrade path is the case that matters: migration 002
// backfills all pre-existing posts/tags onto a placeholder site with
// key = "default", so an operator whose [[site]] block uses any other key
// gets a brand new site row, an empty blog, and -- without this warning --
// no error, no log line, and no clue that their content is still attached
// to the orphaned "default" row.
//
// This is deliberately a warning and not an error: removing a site from
// the config is a legitimate thing to do, and must not block startup.
func warnOrphanedSites(db *gorm.DB, sites []config.Site) error {
	configured := make(map[string]bool, len(sites))
	for i := range sites {
		configured[sites[i].Key] = true
	}

	var rows []dbSite
	if err := db.Find(&rows).Error; err != nil {
		return err
	}

	for _, row := range rows {
		if configured[row.Key] {
			continue
		}
		var posts, tags int64
		if err := db.Model(&dbPost{}).Where("site_id = ?", row.ID).Count(&posts).Error; err != nil {
			return err
		}
		if err := db.Model(&dbTag{}).Where("site_id = ?", row.ID).Count(&tags).Error; err != nil {
			return err
		}
		if posts == 0 && tags == 0 {
			continue
		}
		log.Printf("warning: site %q in the database matches no [[site]] block in your config "+
			"and owns %d posts / %d tags; that content will not be served. If you are upgrading "+
			"from a single-site install, set key = %q on one of your [[site]] blocks.",
			row.Key, posts, tags, row.Key)
	}
	return nil
}
