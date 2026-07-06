// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// sanitizeAgentMitmProxyEnv removes *_proxy environment variables that point
// at our own per-agent MITM proxy (a loopback address with an agent-id proxy
// user, e.g. http://w-10064:x@127.0.0.1:1087). They leak in when the server is
// (re)started from an agent pane's shell; honoring them would loop the
// server's own upstream traffic through its own MITM and mis-attribute every
// gateway call to that agent. Operator-configured egress proxies (non-loopback
// or without an agent-id user) are left untouched.
func sanitizeAgentMitmProxyEnv() {
	agentUserRE := regexp.MustCompile(`^[wm]-\d+$`)
	keys := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}
	for _, key := range keys {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.User == nil {
			continue
		}
		host := u.Hostname()
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			continue
		}
		if !agentUserRE.MatchString(u.User.Username()) {
			continue
		}
		log.Printf("[startup] dropping leaked agent-MITM proxy env %s=%s (server must not proxy through its own MITM)", key, raw)
		_ = os.Unsetenv(key)
	}
}
