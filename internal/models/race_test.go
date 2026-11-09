package models

import (
	"testing"
	"time"

	"gorm.io/gorm"
)

// Race tests hold another request's writes open in an uncommitted
// transaction while a store call runs, then commit or roll them back. On
// Postgres the call runs alongside the open transaction, as two requests
// would; on SQLite (one connection) it simply waits for it, so the same
// expectations hold for both.

// whileOpen starts op, gives it time to run into other's writes, ends
// other (commit or rollback), and returns op's result.
func whileOpen(t *testing.T, other *gorm.DB, commit bool, op func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- op() }()
	time.Sleep(200 * time.Millisecond)
	end := other.Rollback
	if commit {
		end = other.Commit
	}
	if err := end().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("still blocked after the other transaction ended")
		return nil
	}
}

// begin opens the other request's transaction.
func begin(t *testing.T, d *gorm.DB) *gorm.DB {
	t.Helper()
	tx := d.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	return tx
}
