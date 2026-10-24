// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Kiro usage for the account matrix. Kiro's usage API (GetUsageLimits) is
// authorised by the CLI's own login, so the only account we can query is the
// one kiro-cli on this host is signed in as. We read that identity from
// `kiro-cli whoami`, and its credits from the `/usage` slash command run
// non-interactively in a throwaway directory. Both are cached briefly: every
// card asks on load and the CLI takes a few seconds to answer.

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type kiroUsageSnapshot struct {
	LoggedInEmail string  `json:"logged_in_email"`
	LoginMethod   string  `json:"login_method"`
	Plan          string  `json:"plan"`
	ResetsOn      string  `json:"resets_on"`
	CreditsUsed   float64 `json:"credits_used"`
	CreditsLimit  float64 `json:"credits_limit"`
	UsedPct       float64 `json:"used_pct"`
	FetchedAt     string  `json:"fetched_at"`
	Error         string  `json:"error,omitempty"`
}

const kiroUsageTTL = 3 * time.Minute

var (
	kiroUsageMu     sync.Mutex
	kiroUsageCache  kiroUsageSnapshot
	kiroUsageCached time.Time
	// Overridable in tests.
	kiroUsageRun = runKiroCLI

	ansiRE          = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	kiroWhoEmailRE  = regexp.MustCompile(`(?im)^\s*Email:\s*(\S+@\S+)\s*$`)
	kiroWhoMethodRE = regexp.MustCompile(`(?im)Logged in with\s+(.+?)\s*$`)
	kiroUsageHeadRE = regexp.MustCompile(`(?i)resets on\s+([0-9]{4}-[0-9]{2}-[0-9]{2})\s*\|\s*(.+?)\s*$`)
	kiroUsageCredRE = regexp.MustCompile(`(?i)Credits\s*\(\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s+of\s+([0-9][0-9,]*(?:\.[0-9]+)?)`)
	kiroUsagePctRE  = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*%`)
)

func kiroCLIPath() string {
	if p, err := exec.LookPath("kiro-cli"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", "kiro-cli")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func runKiroCLI(args ...string) (string, error) {
	bin := kiroCLIPath()
	if bin == "" {
		return "", exec.ErrNotFound
	}
	// A throwaway cwd keeps `chat` from resuming or polluting any agent's session.
	dir, err := os.MkdirTemp("", "cicy-kiro-usage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	out, err := cmd.CombinedOutput()
	return ansiRE.ReplaceAllString(string(out), ""), err
}

func parseKiroNumber(s string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return v
}

// parseKiroUsage reads the `/usage` text, e.g.
//
//	Estimated Usage | resets on 2026-11-01 | KIRO PRO MAX
//	Credits (4650.65 of 5000 covered in plan), 93.0%
func parseKiroUsage(text string, snap *kiroUsageSnapshot) bool {
	found := false
	for _, line := range strings.Split(text, "\n") {
		if m := kiroUsageHeadRE.FindStringSubmatch(line); m != nil {
			snap.ResetsOn, snap.Plan = m[1], strings.TrimSpace(m[2])
			found = true
		}
		if m := kiroUsageCredRE.FindStringSubmatch(line); m != nil {
			snap.CreditsUsed, snap.CreditsLimit = parseKiroNumber(m[1]), parseKiroNumber(m[2])
			if p := kiroUsagePctRE.FindStringSubmatch(line[strings.Index(line, ")")+1:]); p != nil {
				snap.UsedPct = parseKiroNumber(p[1])
			} else if snap.CreditsLimit > 0 {
				snap.UsedPct = snap.CreditsUsed / snap.CreditsLimit * 100
			}
			found = true
		}
	}
	return found
}

func kiroUsageSnapshotNow(force bool) kiroUsageSnapshot {
	kiroUsageMu.Lock()
	defer kiroUsageMu.Unlock()
	if !force && !kiroUsageCached.IsZero() && time.Since(kiroUsageCached) < kiroUsageTTL {
		return kiroUsageCache
	}
	snap := kiroUsageSnapshot{FetchedAt: time.Now().UTC().Format(time.RFC3339)}
	who, err := kiroUsageRun("whoami")
	if m := kiroWhoEmailRE.FindStringSubmatch(who); m != nil {
		snap.LoggedInEmail = strings.TrimSpace(m[1])
	}
	if m := kiroWhoMethodRE.FindStringSubmatch(who); m != nil {
		snap.LoginMethod = strings.TrimSpace(m[1])
	}
	switch {
	case err == exec.ErrNotFound:
		snap.Error = "kiro-cli not installed"
	case snap.LoggedInEmail == "":
		snap.Error = "kiro-cli not logged in"
	default:
		text, _ := kiroUsageRun("chat", "--no-interactive", "/usage")
		if !parseKiroUsage(text, &snap) {
			snap.Error = "usage unavailable"
		}
	}
	kiroUsageCache, kiroUsageCached = snap, time.Now()
	return snap
}

// handleKiroAccountUsage answers for one matrix account: real credits when it
// is the account kiro-cli is signed in as, otherwise logged_in=false plus the
// email that is signed in, so the UI can say which one it is.
func handleKiroAccountUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Force bool   `json:"force"`
	}
	if r.Method != http.MethodPost || readBody(r, &req) != nil {
		httpErr(w, http.StatusBadRequest, "POST required")
		return
	}
	accounts, err := readKiroAccounts()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	account, ok := accounts[strings.TrimSpace(req.Name)]
	if !ok {
		httpErr(w, http.StatusNotFound, "kiro account not found")
		return
	}
	snap := kiroUsageSnapshotNow(req.Force)
	if snap.Error != "" && snap.LoggedInEmail == "" {
		J(w, M{"logged_in": false, "error": snap.Error})
		return
	}
	if !strings.EqualFold(snap.LoggedInEmail, strings.TrimSpace(account.Email)) {
		J(w, M{"logged_in": false, "logged_in_email": snap.LoggedInEmail})
		return
	}
	J(w, M{
		"logged_in":     true,
		"login_method":  snap.LoginMethod,
		"plan":          snap.Plan,
		"resets_on":     snap.ResetsOn,
		"credits_used":  snap.CreditsUsed,
		"credits_limit": snap.CreditsLimit,
		"used_pct":      snap.UsedPct,
		"fetched_at":    snap.FetchedAt,
		"error":         snap.Error,
	})
}
