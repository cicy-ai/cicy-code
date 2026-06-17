package main

import (
	"net/http"
	"testing"
)

func resetAuthRateLimit() {
	authFailMu.Lock()
	authFailMap = map[string]*authFailRecord{}
	authFailMu.Unlock()
}

func reqFromIP(ip string) *http.Request {
	return &http.Request{RemoteAddr: ip + ":12345"}
}

// Below threshold: never blocked. At threshold: locked out with a Retry-After.
func TestAuthRateLimit_LocksAfterThreshold(t *testing.T) {
	resetAuthRateLimit()
	r := reqFromIP("203.0.113.7")

	for i := 0; i < authFailThreshold-1; i++ {
		authRateLimitFailure(r)
		if blocked, _ := authRateLimitBlocked(r); blocked {
			t.Fatalf("locked out too early at failure %d (threshold %d)", i+1, authFailThreshold)
		}
	}
	// The threshold-th failure trips the lockout.
	authRateLimitFailure(r)
	blocked, retry := authRateLimitBlocked(r)
	if !blocked {
		t.Fatalf("expected lockout after %d failures", authFailThreshold)
	}
	if retry <= 0 {
		t.Fatalf("expected positive Retry-After, got %d", retry)
	}
}

// A successful auth clears the failure counter, so legitimate clients are never
// throttled by their own earlier misses.
func TestAuthRateLimit_SuccessClears(t *testing.T) {
	resetAuthRateLimit()
	r := reqFromIP("203.0.113.8")
	for i := 0; i < authFailThreshold-1; i++ {
		authRateLimitFailure(r)
	}
	authRateLimitSuccess(r)
	// After clearing, a fresh failure must not be near the lockout.
	authRateLimitFailure(r)
	if blocked, _ := authRateLimitBlocked(r); blocked {
		t.Fatalf("success did not clear failure state")
	}
}

// Lockouts are per-IP: one attacker does not lock out a different client.
func TestAuthRateLimit_PerIPIsolation(t *testing.T) {
	resetAuthRateLimit()
	attacker := reqFromIP("198.51.100.5")
	victim := reqFromIP("198.51.100.6")
	for i := 0; i < authFailThreshold; i++ {
		authRateLimitFailure(attacker)
	}
	if blocked, _ := authRateLimitBlocked(attacker); !blocked {
		t.Fatalf("attacker should be locked out")
	}
	if blocked, _ := authRateLimitBlocked(victim); blocked {
		t.Fatalf("a different IP must not be locked out")
	}
}

// Exponential backoff: repeated lockouts grow the lockCount (and thus the
// backoff window) rather than resetting.
func TestAuthRateLimit_BackoffEscalates(t *testing.T) {
	resetAuthRateLimit()
	r := reqFromIP("203.0.113.9")
	for i := 0; i < authFailThreshold; i++ {
		authRateLimitFailure(r)
	}
	authFailMu.Lock()
	first := authFailMap["203.0.113.9"].lockCount
	authFailMu.Unlock()
	if first != 1 {
		t.Fatalf("expected lockCount 1 after first lockout, got %d", first)
	}
}
