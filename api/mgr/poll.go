package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const runtimeMembershipSnapshotTTL = 15 * time.Second

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

type runtimeMembershipSnapshotCacheState struct {
	mu        sync.Mutex
	value     runtimeMembershipSnapshot
	fetchedAt time.Time
}

var runtimeMembershipSnapshotCache runtimeMembershipSnapshotCacheState

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

func mergeRuntimeMembershipSnapshots(primary runtimeMembershipSnapshot, fallback runtimeMembershipSnapshot) runtimeMembershipSnapshot {
	merged := fallback
	if strings.TrimSpace(primary.Kind) != "" {
		merged.Kind = strings.TrimSpace(primary.Kind)
	}
	if strings.TrimSpace(primary.Tag) != "" {
		merged.Tag = strings.TrimSpace(primary.Tag)
	}
	if strings.TrimSpace(primary.ExpiresAt) != "" {
		merged.ExpiresAt = strings.TrimSpace(primary.ExpiresAt)
	}
	if strings.TrimSpace(primary.RenewURL) != "" {
		merged.RenewURL = strings.TrimSpace(primary.RenewURL)
	}
	if strings.TrimSpace(primary.UpgradeURL) != "" {
		merged.UpgradeURL = strings.TrimSpace(primary.UpgradeURL)
	}
	if primary.ShowRenew != nil {
		merged.ShowRenew = primary.ShowRenew
	}
	if primary.ShowUpgrade != nil {
		merged.ShowUpgrade = primary.ShowUpgrade
	}
	if strings.TrimSpace(primary.TrialExpiresAt) != "" {
		merged.TrialExpiresAt = strings.TrimSpace(primary.TrialExpiresAt)
	}
	if strings.TrimSpace(primary.TrialExpiresEpoch) != "" {
		merged.TrialExpiresEpoch = strings.TrimSpace(primary.TrialExpiresEpoch)
	}
	if primary.IsPro != nil {
		merged.IsPro = primary.IsPro
	}
	if strings.TrimSpace(primary.SyncedAt) != "" {
		merged.SyncedAt = strings.TrimSpace(primary.SyncedAt)
	}
	return merged
}

func fetchRuntimeMembershipSnapshotFromTeamcenter() (runtimeMembershipSnapshot, error) {
	masterURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CICY_MASTER_URL")), "/")
	masterToken := strings.TrimSpace(os.Getenv("CICY_MASTER_TOKEN"))
	if masterURL == "" || masterToken == "" {
		return runtimeMembershipSnapshot{}, nil
	}
	bootstrapPath := strings.TrimSpace(os.Getenv("CICY_TEAMCENTER_BOOTSTRAP_PATH"))
	if bootstrapPath == "" {
		bootstrapPath = "/api/runtime/team/bootstrap"
	}
	if !strings.HasPrefix(bootstrapPath, "/") {
		bootstrapPath = "/" + bootstrapPath
	}

	body, err := json.Marshal(map[string]any{
		"instance_key":   strings.TrimSpace(os.Getenv("CICY_INSTANCE_KEY")),
		"instance_label": strings.TrimSpace(os.Getenv("CICY_INSTANCE_LABEL")),
		"runtime_kind":   firstNonEmpty(strings.TrimSpace(os.Getenv("CICY_RUNTIME_KIND")), "container"),
		"api_token":      strings.TrimSpace(os.Getenv("CICY_API_TOKEN")),
	})
	if err != nil {
		return runtimeMembershipSnapshot{}, err
	}

	req, err := http.NewRequest(http.MethodPost, masterURL+bootstrapPath, bytes.NewReader(body))
	if err != nil {
		return runtimeMembershipSnapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+masterToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return runtimeMembershipSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return runtimeMembershipSnapshot{}, nil
	}

	var payload struct {
		Data struct {
			MembershipKind      string `json:"membership_kind"`
			MembershipTag       string `json:"membership_tag"`
			MembershipExpiresAt string `json:"membership_expires_at"`
			RenewURL            string `json:"renew_url"`
			UpgradeURL          string `json:"upgrade_url"`
			ShowRenew           *bool  `json:"show_renew"`
			ShowUpgrade         *bool  `json:"show_upgrade"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return runtimeMembershipSnapshot{}, err
	}

	return runtimeMembershipSnapshot{
		Kind:        strings.TrimSpace(payload.Data.MembershipKind),
		Tag:         strings.TrimSpace(payload.Data.MembershipTag),
		ExpiresAt:   strings.TrimSpace(payload.Data.MembershipExpiresAt),
		RenewURL:    strings.TrimSpace(payload.Data.RenewURL),
		UpgradeURL:  strings.TrimSpace(payload.Data.UpgradeURL),
		ShowRenew:   payload.Data.ShowRenew,
		ShowUpgrade: payload.Data.ShowUpgrade,
		SyncedAt:    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func loadRuntimeMembershipSnapshot() runtimeMembershipSnapshot {
	envSnapshot := runtimeMembershipSnapshotFromEnv()
	masterURL := strings.TrimSpace(os.Getenv("CICY_MASTER_URL"))
	masterToken := strings.TrimSpace(os.Getenv("CICY_MASTER_TOKEN"))
	if masterURL == "" || masterToken == "" {
		return envSnapshot
	}

	now := time.Now().UTC()
	runtimeMembershipSnapshotCache.mu.Lock()
	cached := runtimeMembershipSnapshotCache.value
	cachedAt := runtimeMembershipSnapshotCache.fetchedAt
	runtimeMembershipSnapshotCache.mu.Unlock()

	if !cachedAt.IsZero() && now.Sub(cachedAt) < runtimeMembershipSnapshotTTL {
		return mergeRuntimeMembershipSnapshots(cached, envSnapshot)
	}

	fresh, err := fetchRuntimeMembershipSnapshotFromTeamcenter()
	if err == nil {
		runtimeMembershipSnapshotCache.mu.Lock()
		runtimeMembershipSnapshotCache.value = fresh
		runtimeMembershipSnapshotCache.fetchedAt = now
		runtimeMembershipSnapshotCache.mu.Unlock()
		return mergeRuntimeMembershipSnapshots(fresh, envSnapshot)
	}
	if !cachedAt.IsZero() {
		return mergeRuntimeMembershipSnapshots(cached, envSnapshot)
	}
	return envSnapshot
}

func handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		httpErr(w, 405, "method not allowed")
		return
	}
	paneID := r.URL.Query().Get("pane_id")
	agents, err := listAgentsByPane(paneID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	snapshot := loadRuntimeMembershipSnapshot()
	resp := M{
		"success":     true,
		"pane_id":     shortPaneID(normPaneID(paneID)),
		"agents":      agents,
		"statuses":    M{},
		"server_time": time.Now().UTC().Format(time.RFC3339),
	}
	if snapshot.TrialExpiresAt != "" {
		resp["trial_expires_at"] = snapshot.TrialExpiresAt
		if snapshot.TrialExpiresEpoch != "" {
			resp["trial_expires_at_epoch"] = snapshot.TrialExpiresEpoch
		}
	}
	if snapshot.IsPro != nil {
		resp["is_pro"] = *snapshot.IsPro
	}
	if snapshot.Kind != "" {
		resp["membership_kind"] = snapshot.Kind
	}
	if snapshot.Tag != "" {
		resp["membership_tag"] = snapshot.Tag
	}
	if snapshot.ExpiresAt != "" {
		resp["membership_expires_at"] = snapshot.ExpiresAt
	}
	if snapshot.RenewURL != "" {
		resp["renew_url"] = snapshot.RenewURL
	}
	if snapshot.UpgradeURL != "" {
		resp["upgrade_url"] = snapshot.UpgradeURL
	}
	if snapshot.ShowRenew != nil {
		resp["show_renew"] = *snapshot.ShowRenew
	}
	if snapshot.ShowUpgrade != nil {
		resp["show_upgrade"] = *snapshot.ShowUpgrade
	}
	if snapshot.SyncedAt != "" {
		resp["membership_synced_at"] = snapshot.SyncedAt
	}
	J(w, resp)
}
