// Package ratelimit holds in-process fixed-window counters and cooldowns, a port of
// ~/dev/fund/src/server/rate-limit.ts. State is per process and resets on restart; a shared
// store is needed before running more than one instance.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	count   int
	resetAt time.Time
}

type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	lastHit map[string]time.Time
	now     func() time.Time
}

func New() *Limiter {
	return &Limiter{buckets: map[string]*bucket{}, lastHit: map[string]time.Time{}, now: time.Now}
}

// SetClock replaces the clock (tests).
func (l *Limiter) SetClock(now func() time.Time) { l.now = now }

// Take is a fixed-window counter. It returns ok, or the seconds to wait.
func (l *Limiter) Take(key string, limit int, window time.Duration) (ok bool, retryAfterSec int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cur := l.buckets[key]
	if cur == nil || !cur.resetAt.After(now) {
		l.buckets[key] = &bucket{count: 1, resetAt: now.Add(window)}
		return true, 0
	}
	if cur.count >= limit {
		return false, ceilSec(cur.resetAt.Sub(now))
	}
	cur.count++
	return true, 0
}

// Check reports whether a cooldown has elapsed, without marking it.
func (l *Limiter) Check(key string, cooldown time.Duration) (ok bool, retryAfterSec int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, seen := l.lastHit[key]
	if seen {
		if elapsed := l.now().Sub(prev); elapsed < cooldown {
			return false, ceilSec(cooldown - elapsed)
		}
	}
	return true, 0
}

// Mark records a cooldown hit now.
func (l *Limiter) Mark(key string) {
	l.mu.Lock()
	l.lastHit[key] = l.now()
	l.mu.Unlock()
}

func ceilSec(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		return 1
	}
	return s
}

// Rule is a per-IP route limit.
type Rule struct {
	Limit  int
	Window time.Duration
}

// Auth rules from ~/dev/fund/src/server/auth-rate-limit.ts.
var (
	SignIn      = Rule{10, 15 * time.Minute}
	SignUp      = Rule{5, time.Hour}
	EmailAction = Rule{3, time.Hour}
	Default     = Rule{120, time.Minute}
)
