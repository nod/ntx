package auth

import (
	"testing"
	"time"
)

func TestLimiterCountsOnlyFailures(t *testing.T) {
	l := NewLimiter()
	if l.Blocked("a") {
		t.Fatal("a fresh limiter must not block")
	}
	for i := 0; i < 4; i++ {
		l.Fail("a")
	}
	if l.Blocked("a") {
		t.Fatal("4 failures must not block")
	}
	l.Fail("a")
	if !l.Blocked("a") {
		t.Error("5 failures must block that IP")
	}
	if l.Blocked("b") {
		t.Error("one IP's failures must not block another")
	}
}

func TestLimiterWindow(t *testing.T) {
	l := NewLimiter()
	start := time.Now()
	l.now = func() time.Time { return start }
	for i := 0; i < 5; i++ {
		l.Fail("a")
	}
	if !l.Blocked("a") {
		t.Fatal("expected a block within the window")
	}
	l.now = func() time.Time { return start.Add(15*time.Minute + time.Second) }
	if l.Blocked("a") {
		t.Error("failures older than 15 minutes must not count")
	}
}
