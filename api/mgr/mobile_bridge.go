// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	mobileBridgeURLVar      = "CICY_MOBILE_BRIDGE_URL"
	mobileBridgeTokenVar    = "CICY_MOBILE_BRIDGE_TOKEN"
	mobileBridgeMaxResponse = 1 << 20
)

func mobileBridgeConfig() (*url.URL, string, error) {
	rawURL := strings.TrimSpace(os.Getenv(mobileBridgeURLVar))
	token := strings.TrimSpace(os.Getenv(mobileBridgeTokenVar))
	if rawURL == "" || token == "" {
		return nil, "", fmt.Errorf("mobile bridge is not configured")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.Port() == "" || u.User != nil {
		return nil, "", fmt.Errorf("mobile bridge URL must be an HTTP loopback origin with an explicit port")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil, "", fmt.Errorf("mobile bridge URL must use a loopback host")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, "", fmt.Errorf("mobile bridge URL must not contain a query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, token, nil
}

func mobileBridgeConfigured() bool {
	_, _, err := mobileBridgeConfig()
	return err == nil
}

func mobileBridgeError(code, message string) string {
	b, _ := json.Marshal(M{"ok": false, "code": code, "error": message})
	return string(b)
}

func mobileBridgeCall(parent context.Context, input map[string]interface{}) string {
	if parent == nil {
		parent = context.Background()
	}
	base, token, err := mobileBridgeConfig()
	if err != nil {
		return mobileBridgeError("bridge_unavailable", err.Error())
	}
	action, _ := input["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "tree", "click", "input", "scroll", "back", "home", "launch":
	default:
		return mobileBridgeError("invalid_action", "action must be tree, click, input, scroll, back, home, or launch")
	}

	method := http.MethodPost
	endpoint := "/v1/accessibility/action"
	var body io.Reader
	if action == "tree" {
		method = http.MethodGet
		endpoint = "/v1/accessibility/tree"
	} else {
		payload, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return mobileBridgeError("invalid_input", marshalErr.Error())
		}
		body = bytes.NewReader(payload)
	}

	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + endpoint
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return mobileBridgeError("request_failed", err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return mobileBridgeError("timeout", ctx.Err().Error())
		}
		return mobileBridgeError("request_failed", err.Error())
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, mobileBridgeMaxResponse+1))
	if readErr != nil {
		return mobileBridgeError("response_failed", readErr.Error())
	}
	if len(data) > mobileBridgeMaxResponse {
		return mobileBridgeError("response_too_large", "mobile bridge response exceeds 1 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return mobileBridgeError("bridge_http_error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, message))
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return `{"ok":true}`
	}
	return string(data)
}
