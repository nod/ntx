package models

import (
	"errors"

	"gorm.io/gorm"
)

// isDuplicate reports whether err is a unique-constraint failure, from
// either database.
func isDuplicate(d *gorm.DB, err error) bool {
	t, ok := d.Dialector.(gorm.ErrorTranslator)
	return ok && err != nil && errors.Is(t.Translate(err), gorm.ErrDuplicatedKey)
}

// retryOnDuplicate runs fn, a pre-check and the write it guards, in a
// transaction of its own (a savepoint when tx is already one). If the write
// hits a unique constraint -- another request wrote the same value after
// the pre-check -- it runs fn once more: the pre-check now sees that row
// and gives its own answer, a form error or the row itself. The savepoint
// matters on Postgres, where a failed statement spoils the transaction
// around it.
func retryOnDuplicate(tx *gorm.DB, fn func(tx *gorm.DB) error) error {
	err := tx.Transaction(fn)
	if isDuplicate(tx, err) {
		err = tx.Transaction(fn)
	}
	return err
}
