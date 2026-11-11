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

// Reserve admits one sign-in attempt from ip if it is under budget, and
// counts it as a failure straight away. Checking and counting are one
// locked step, so a burst of parallel attempts can't all slip past the
// check while their password checks run. Call Release if the attempt
// succeeds.
func (l *Limiter) Reserve(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.recent(ip)
	if len(recent) >= limitFailures {
		return false
	}
	l.failures[ip] = append(recent, l.now())
	return true
}

// Release gives back the attempt most recently reserved for ip: it
// succeeded, so it isn't a failure.
func (l *Limiter) Release(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.failures[ip]; len(f) > 0 {
		l.failures[ip] = f[:len(f)-1]
		if len(l.failures[ip]) == 0 {
			delete(l.failures, ip)
		}
	}
}

// Sweep drops every IP with no failures left inside the window and
// returns how many it dropped. An IP's entry is otherwise only trimmed
// when that IP tries again; serve runs this periodically so IPs that
// never return don't stay forever.
func (l *Limiter) Sweep() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for ip := range l.failures {
		if l.recent(ip) == nil {
			n++
		}
	}
	return n
}
