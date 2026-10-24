package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseKiroUsage(t *testing.T) {
	var snap kiroUsageSnapshot
	text := "Estimated Usage | resets on 2026-11-01 | KIRO PRO MAX\nCredits (4,650.65 of 5000 covered in plan), 93.0%\nAdditional credits:\n"
	if !parseKiroUsage(text, &snap) {
		t.Fatal("not parsed")
	}
	if snap.ResetsOn != "2026-11-01" || snap.Plan != "KIRO PRO MAX" || snap.CreditsUsed != 4650.65 || snap.CreditsLimit != 5000 || snap.UsedPct != 93 {
		t.Fatalf("%+v", snap)
	}
}

func TestKiroAccountUsageOnlyForSignedInAccount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_ = writeKiroAccounts(map[string]kiroAccountConfig{
		"a@example.com": {LoginType: "github", Email: "a@example.com"},
		"b@example.com": {LoginType: "builder_id", Email: "b@example.com"},
	})
	calls := 0
	orig := kiroUsageRun
	kiroUsageRun = func(args ...string) (string, error) {
		calls++
		if args[0] == "whoami" {
			return "Logged in with GitHub\nEmail: A@example.com\n", nil
		}
		return "Estimated Usage | resets on 2026-11-01 | KIRO PRO MAX\nCredits (10 of 50 covered in plan), 20.0%\n", nil
	}
	t.Cleanup(func() { kiroUsageRun = orig; kiroUsageCached = time.Time{} })
	kiroUsageCached = time.Time{}
	ask := func(name string) string {
		rec := httptest.NewRecorder()
		handleKiroAccountUsage(rec, httptest.NewRequest(http.MethodPost, "/api/kiro/accounts/usage", strings.NewReader(`{"name":"`+name+`"}`)))
		return rec.Body.String()
	}
	if a := ask("a@example.com"); !strings.Contains(a, `"logged_in":true`) || !strings.Contains(a, `"credits_used":10`) {
		t.Fatalf("a: %s", a)
	}
	if b := ask("b@example.com"); !strings.Contains(b, `"logged_in":false`) || !strings.Contains(b, `"logged_in_email":"A@example.com"`) {
		t.Fatalf("b: %s", b)
	}
	if calls != 2 {
		t.Fatalf("CLI should run once per TTL (whoami + /usage), ran %d times", calls)
	}
}
