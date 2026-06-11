package main

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type runtimeMembershipSnapshot struct {
	Kind              string
	Tag               string
	ExpiresAt         string
	RenewURL          string
	UpgradeURL        string
	ShowRenew         *bool
	ShowUpgrade       *bool
	TrialExpiresAt    string
	TrialExpiresEpoch string
	IsPro             *bool
	SyncedAt          string
}

func resolveMembershipURL(path string, envKey string) string {
	value := strings.TrimSpace(os.Getenv(envKey))
	if value == "" {
		return ""
	}
	teamID := strings.TrimSpace(os.Getenv("CICY_TEAM_ID"))
	if teamID == "" {
		return value
	}
	if !strings.Contains(value, path) {
		return value
	}
	if strings.Contains(value, "team_id=") {
		return value
	}
	if strings.Contains(value, "?") {
		return value + "&team_id=" + teamID
	}
	return value + "?team_id=" + teamID
}

func resolveTrialExpiresAtFromEnv() (string, string) {
	raw := strings.TrimSpace(os.Getenv("CLOUD_TRIAL_RUNTIME_EXPIRES_AT"))
	if raw == "" {
		return "", ""
	}

	if epoch, err := strconv.ParseInt(raw, 10, 64); err == nil && epoch > 0 {
		return strconv.FormatInt(epoch, 10), time.Unix(epoch, 0).UTC().Format(time.RFC3339)
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return strconv.FormatInt(ts.Unix(), 10), ts.UTC().Format(time.RFC3339)
	}
	return "", raw
}

func resolveIsProFromEnv() (bool, bool) {
	raw := strings.TrimSpace(os.Getenv("CICY_IS_PRO"))
	if raw == "" {
		return false, false
	}

	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on", "pro":
		return true, true
	case "0", "false", "no", "off", "trial", "free":
		return false, true
	default:
		return false, true
	}
}

func resolveBoolEnv(key string) (bool, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false, false
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, true
	}
}

func boolPtr(value bool) *bool {
	v := value
	return &v
}

func runtimeMembershipSnapshotFromEnv() runtimeMembershipSnapshot {
	snapshot := runtimeMembershipSnapshot{}
	if expiresEpoch, expiresAt := resolveTrialExpiresAtFromEnv(); expiresAt != "" {
		snapshot.TrialExpiresAt = expiresAt
		snapshot.TrialExpiresEpoch = expiresEpoch
	}
	if isPro, exists := resolveIsProFromEnv(); exists {
		snapshot.IsPro = boolPtr(isPro)
	}
	if value := strings.TrimSpace(os.Getenv("CICY_MEMBERSHIP_KIND")); value != "" {
		snapshot.Kind = value
	}
	if value := strings.TrimSpace(os.Getenv("CICY_MEMBERSHIP_TAG")); value != "" {
		snapshot.Tag = value
	}
	if value := strings.TrimSpace(os.Getenv("CICY_MEMBERSHIP_EXPIRES_AT")); value != "" {
		snapshot.ExpiresAt = value
	}
	if value := resolveMembershipURL("/team/pay", "CICY_MEMBERSHIP_RENEW_URL"); value != "" {
		snapshot.RenewURL = value
	}
	if value := resolveMembershipURL("/team/upgrade", "CICY_MEMBERSHIP_UPGRADE_URL"); value != "" {
		snapshot.UpgradeURL = value
	}
	if value, exists := resolveBoolEnv("CICY_MEMBERSHIP_SHOW_RENEW"); exists {
		snapshot.ShowRenew = boolPtr(value)
	}
	if value, exists := resolveBoolEnv("CICY_MEMBERSHIP_SHOW_UPGRADE"); exists {
		snapshot.ShowUpgrade = boolPtr(value)
	}
	return snapshot
}

func loadRuntimeMembershipSnapshot() runtimeMembershipSnapshot {
	// Membership is sourced from the runtime env vars only. The legacy
	// teamcenter/master fetch (CICY_MASTER_URL/CICY_MASTER_TOKEN) was retired
	// along with the team-bootstrap model.
	return runtimeMembershipSnapshotFromEnv()
}

func handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	paneID := r.URL.Query().Get("pane_id")
	J(w, buildPollData(paneID))
}
