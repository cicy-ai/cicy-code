// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"
)

func TestResolveProviderTargetPathWithFinalEndpointURL(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
		suffix   string
		want     string
	}{
		{
			name:     "opencode zen final chat completions endpoint",
			basePath: "/zen/v1/chat/completions",
			suffix:   "/v1/chat/completions",
			want:     "/zen/v1/chat/completions",
		},
		{
			name:     "base prefix still appends endpoint",
			basePath: "/zen/v1",
			suffix:   "/chat/completions",
			want:     "/zen/v1/chat/completions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveOpenClawProviderTargetPath(tt.basePath, tt.suffix); got != tt.want {
				t.Fatalf("target path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpstreamAgentIdentityHeaderOnlyForCicyGateway(t *testing.T) {
	t.Run("cicy gateway receives identity", func(t *testing.T) {
		header := make(http.Header)
		setUpstreamAgentIdentityHeader(header, "gateway.cicy-ai.com", "w-10242")
		if got := header.Get("X_AGENT_ID"); got != "w-10242" {
			t.Fatalf("X_AGENT_ID = %q, want w-10242", got)
		}
	})
	t.Run("opencode zen does not receive identity", func(t *testing.T) {
		header := http.Header{"X_AGENT_ID": []string{"stale"}}
		setUpstreamAgentIdentityHeader(header, "opencode.ai", "w-10242")
		if got := header.Get("X_AGENT_ID"); got != "" {
			t.Fatalf("X_AGENT_ID leaked to OpenCode Zen: %q", got)
		}
	})
}
