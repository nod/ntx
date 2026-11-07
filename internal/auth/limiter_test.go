package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterBudgetPerIP(t *testing.T) {
	l := NewLimiter()
	for i := 0; i < 5; i++ {
		if !l.Reserve("a") {
			t.Fatalf("attempt %d must be admitted", i+1)
		}
	}
	if l.Reserve("a") {
		t.Error("a 6th unreleased attempt must be refused")
	}
	if !l.Reserve("b") {
		t.Error("one IP's failures must not block another")
	}
}

func TestLimiterWindow(t *testing.T) {
	l := NewLimiter()
	start := time.Now()
	l.now = func() time.Time { return start }
	for i := 0; i < 5; i++ {
		l.Reserve("a")
	}
	if l.Reserve("a") {
		t.Fatal("expected a block within the window")
	}
	l.now = func() time.Time { return start.Add(15*time.Minute + time.Second) }
	if !l.Reserve("a") {
		t.Error("failures older than 15 minutes must not count")
	}
}

// A burst of parallel attempts must not get more than the budget past the
// check: the reservation and the check are one step.
func TestLimiterReserveIsAtomic(t *testing.T) {
	l := NewLimiter()
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Reserve("a") {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := admitted.Load(); got != limitFailures {
		t.Errorf("60 parallel attempts: %d admitted, want exactly %d", got, limitFailures)
	}
}

// A successful attempt gives its reservation back, so correct logins never
// use up the budget.
func TestLimiterReleaseReturnsBudget(t *testing.T) {
	l := NewLimiter()
	for i := 0; i < 10; i++ {
		if !l.Reserve("a") {
			t.Fatalf("attempt %d refused although every earlier one was released", i+1)
		}
		l.Release("a")
	}
	for i := 0; i < limitFailures; i++ {
		l.Reserve("a")
	}
	if l.Reserve("a") {
		t.Error("after the budget of unreleased attempts, Reserve must refuse")
	}
}
