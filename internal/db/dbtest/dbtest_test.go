package dbtest

import (
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestAwaitBlocked(t *testing.T) {
	d := Open(t)
	if d.Dialector.Name() != "postgres" {
		t.Skip("SQLite has one connection, so nothing ever waits on a lock")
	}
	if err := d.Exec("CREATE TABLE things (id INT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	tx := d.Begin()
	defer tx.Rollback()
	if err := tx.Exec("INSERT INTO things VALUES (1)").Error; err != nil {
		t.Fatal(err)
	}

	if err := awaitBlocked(tx, 1, 300*time.Millisecond); err == nil {
		t.Fatal("reported a waiter when nothing was waiting")
	}

	// first holds row 2 and waits on tx for row 1.
	first := make(chan error, 1)
	go func() {
		first <- d.Transaction(func(t2 *gorm.DB) error {
			if err := t2.Exec("INSERT INTO things VALUES (2)").Error; err != nil {
				return err
			}
			return t2.Exec("INSERT INTO things VALUES (1)").Error
		})
	}()
	if err := awaitBlocked(tx, 1, 10*time.Second); err != nil {
		t.Fatalf("missed the waiting insert: %v", err)
	}
	if err := awaitBlocked(tx, 2, 300*time.Millisecond); err == nil {
		t.Fatal("counted two waiters when one was waiting")
	}

	// second waits on first for row 2: behind tx, but not on it directly.
	second := make(chan error, 1)
	go func() { second <- d.Exec("INSERT INTO things VALUES (2)").Error }()
	if err := awaitBlocked(tx, 2, 10*time.Second); err != nil {
		t.Fatalf("missed the insert queued behind the first: %v", err)
	}

	select {
	case <-first:
		t.Fatal("the first insert finished while tx was still open")
	default:
	}
	tx.Rollback()
	if err := <-first; err != nil {
		t.Fatalf("first insert after rollback: %v", err)
	}
	if err := <-second; err == nil {
		t.Fatal("second insert of row 2 succeeded after the first committed it")
	}
}
