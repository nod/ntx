package auth

import (
	"sync"
	"time"
)

const (
	limitFailures = 5
	limitWindow   = 15 * time.Minute
)

// Limiter counts failed root sign-ins per client IP. Only failures count,
// so a successful sign-in never uses up budget, and because the key is the
// real client IP (see clientip), one attacker can't lock out anyone else.
type Limiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{failures: make(map[string][]time.Time), now: time.Now}
}

// recent drops failures outside the window and returns what's left.
// Callers hold l.mu.
func (l *Limiter) recent(ip string) []time.Time {
	cutoff := l.now().Add(-limitWindow)
	kept := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, ip)
		return nil
	}
	l.failures[ip] = kept
	return kept
}

// Blocked reports whether ip has used up its failure budget.
func (l *Limiter) Blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) >= limitFailures
}

// Fail records one failed attempt from ip.
func (l *Limiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip] = append(l.recent(ip), l.now())
}
