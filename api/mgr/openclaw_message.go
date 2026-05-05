package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var openclawMessageIDPattern = regexp.MustCompile(`Message ID:\s*([^\s]+)`)

type openclawMessageSendResult struct {
	Success   bool   `json:"success"`
	Profile   string `json:"profile"`
	Channel   string `json:"channel"`
	Target    string `json:"target"`
	Message   string `json:"message"`
	MessageID string `json:"message_id,omitempty"`
	Output    string `json:"output,omitempty"`
	TimedOut  bool   `json:"timed_out,omitempty"`
}

func openclawProfileFromPaneID(paneID string) string {
	paneID = normPaneID(strings.TrimSpace(paneID))
	if paneID == "" {
		return ""
	}
	return shortPaneID(paneID)
}

func parseOpenClawMessageSendResult(profile, channel, target, message, output string, timedOut bool) openclawMessageSendResult {
	result := openclawMessageSendResult{
		Profile:  profile,
		Channel:  channel,
		Target:   target,
		Message:  message,
		Output:   strings.TrimSpace(output),
		TimedOut: timedOut,
	}
	if matches := openclawMessageIDPattern.FindStringSubmatch(output); len(matches) == 2 {
		result.MessageID = matches[1]
	}
	result.Success = strings.Contains(output, "Sent via")
	return result
}

func sendOpenClawMessage(profile, channel, target, message string, timeout time.Duration) (openclawMessageSendResult, error) {
	args := []string{
		"--profile", profile,
		"message", "send",
		"--channel", channel,
		"--target", target,
		"--message", message,
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "openclaw", args...)
	out, err := cmd.CombinedOutput()
	output := string(out)
	result := parseOpenClawMessageSendResult(profile, channel, target, message, output, ctx.Err() == context.DeadlineExceeded)
	if result.Success {
		return result, nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return result, context.DeadlineExceeded
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// POST /api/openclaw/message/send
// {"pane_id":"w-10001:main.0","channel":"openclaw-weixin","target":"...","message":"...","timeout_seconds":20}
func handleOpenClawMessageSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		PaneID         string `json:"pane_id"`
		Profile        string `json:"profile"`
		Channel        string `json:"channel"`
		Target         string `json:"target"`
		Message        string `json:"message"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Profile = strings.TrimSpace(req.Profile)
	if req.Profile == "" {
		req.Profile = openclawProfileFromPaneID(req.PaneID)
	}
	req.Channel = strings.TrimSpace(req.Channel)
	req.Target = strings.TrimSpace(req.Target)
	req.Message = strings.TrimSpace(req.Message)
	if req.Profile == "" {
		httpErr(w, http.StatusBadRequest, "profile or pane_id required")
		return
	}
	if req.Channel == "" || req.Target == "" || req.Message == "" {
		httpErr(w, http.StatusBadRequest, "channel, target, message required")
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 20
	}
	if req.TimeoutSeconds > 120 {
		req.TimeoutSeconds = 120
	}

	result, err := sendOpenClawMessage(req.Profile, req.Channel, req.Target, req.Message, time.Duration(req.TimeoutSeconds)*time.Second)
	log.Printf("[openclaw-send] profile=%s channel=%s target=%s success=%v timed_out=%v message_id=%s err=%v",
		req.Profile, req.Channel, req.Target, result.Success, result.TimedOut, result.MessageID, err)
	if err != nil && !result.Success {
		httpErr(w, http.StatusInternalServerError, strings.TrimSpace(result.Output))
		return
	}
	J(w, result)
}
