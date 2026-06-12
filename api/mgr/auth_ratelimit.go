package main

import (
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// Brute-force protection for token auth. Previously verifyToken had no rate
// limit, so /api/auth/verify-token (and any Bearer-protected endpoint) could be
// hammered to guess a token — most importantly the global api_token, whose
// entropy depends on how it was provisioned. We track FAILED verifications per
// client IP and lock that IP out with exponential backoff once failures cross a
// threshold within a rolling window. A SUCCESSFUL auth clears the counter, so
// legitimate clients (valid token, high-frequency UI polling) are never
// throttled — only repeated failures are.
//
// Note on tunnels: when traffic arrives through an frp/ssh tunnel every request
// shares the loopback source IP, so a flood collapses onto one bucket. That is
// acceptable here: the limiter is fail-safe (deny on doubt) and an attack that
// trips the loopback lockout is a loud, logged signal rather than silent
// guessing. Legitimate callers still succeed between bursts and clear the bucket.

const (
	authFailWindow    = 60 * time.Second // failures count within this rolling window
	authFailThreshold = 20               // failures in the window before lockout
	authLockoutBase   = 30 * time.Second // first lockout; doubles each repeat
	authLockoutMax    = 15 * time.Minute // cap on exponential backoff
	authFailMapCap    = 4096             // prune stale records past this many
)

type authFailRecord struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
	lockCount   int
}

var (
	authFailMu  sync.Mutex
	authFailMap = map[string]*authFailRecord{}
)

func authClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// authRateLimitBlocked reports whether this client is currently locked out and,
// if so, the whole seconds remaining (for a Retry-After header).
func authRateLimitBlocked(r *http.Request) (bool, int) {
	ip := authClientIP(r)
	now := time.Now()
	authFailMu.Lock()
	defer authFailMu.Unlock()
	rec := authFailMap[ip]
	if rec == nil || now.After(rec.lockedUntil) {
		return false, 0
	}
	return true, int(time.Until(rec.lockedUntil).Seconds()) + 1
}

// authRateLimitFailure records a failed auth from this client and, past the
// threshold, locks the client out with exponential backoff.
func authRateLimitFailure(r *http.Request) {
	ip := authClientIP(r)
	now := time.Now()
	authFailMu.Lock()
	defer authFailMu.Unlock()
	authPruneLocked(now)
	rec := authFailMap[ip]
	if rec == nil {
		rec = &authFailRecord{windowStart: now}
		authFailMap[ip] = rec
	}
	if now.Sub(rec.windowStart) > authFailWindow {
		rec.failures = 0
		rec.windowStart = now
	}
	rec.failures++
	if rec.failures >= authFailThreshold {
		rec.lockCount++
		backoff := authLockoutBase << uint(rec.lockCount-1)
		if backoff <= 0 || backoff > authLockoutMax {
			backoff = authLockoutMax
		}
		rec.lockedUntil = now.Add(backoff)
		rec.failures = 0
		rec.windowStart = now
		log.Printf("[auth] rate-limit: %s locked out for %s after repeated auth failures", ip, backoff)
	}
}

// authRateLimitSuccess clears any failure state for this client on a good auth.
func authRateLimitSuccess(r *http.Request) {
	ip := authClientIP(r)
	authFailMu.Lock()
	delete(authFailMap, ip)
	authFailMu.Unlock()
}

// authPruneLocked drops stale records when the map grows large. Caller holds the
// mutex. A record is stale if it is not locked and its window has long expired.
func authPruneLocked(now time.Time) {
	if len(authFailMap) < authFailMapCap {
		return
	}
	for ip, rec := range authFailMap {
		if now.After(rec.lockedUntil) && now.Sub(rec.windowStart) > 2*authFailWindow {
			delete(authFailMap, ip)
		}
	}
}
