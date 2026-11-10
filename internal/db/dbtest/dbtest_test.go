package dbtest

import (
	"testing"
	"time"
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

	if err := awaitBlocked(tx, 300*time.Millisecond); err == nil {
		t.Fatal("reported a waiter when nothing was waiting")
	}

	done := make(chan error, 1)
	go func() { done <- d.Exec("INSERT INTO things VALUES (1)").Error }()
	if err := awaitBlocked(tx, 10*time.Second); err != nil {
		t.Fatalf("missed the waiting insert: %v", err)
	}
	select {
	case <-done:
		t.Fatal("the insert finished while tx was still open")
	default:
	}
	tx.Rollback()
	if err := <-done; err != nil {
		t.Fatalf("insert after rollback: %v", err)
	}
}
