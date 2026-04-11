package main

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

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
	resp := M{
		"success":     true,
		"pane_id":     shortPaneID(normPaneID(paneID)),
		"agents":      agents,
		"statuses":    M{},
		"server_time": time.Now().UTC().Format(time.RFC3339),
	}
	if expiresEpoch, expiresAt := resolveTrialExpiresAtFromEnv(); expiresAt != "" {
		resp["trial_expires_at"] = expiresAt
		if expiresEpoch != "" {
			resp["trial_expires_at_epoch"] = expiresEpoch
		}
	}
	if isPro, exists := resolveIsProFromEnv(); exists {
		resp["is_pro"] = isPro
	}
	J(w, resp)
}
