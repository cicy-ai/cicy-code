// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFeishuValidateCredentialsSyncsAppName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token"}`))
		case "/open-apis/bot/v3/info":
			if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("Authorization = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","bot":{"app_name":"当前飞书应用"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldBaseURL := feishuBaseURL
	feishuBaseURL = server.URL
	defer func() { feishuBaseURL = oldBaseURL }()

	name, reachable, err := feishuValidateCredentials("cli_test", "secret")
	if err != nil {
		t.Fatalf("feishuValidateCredentials: %v", err)
	}
	if !reachable {
		t.Fatal("reachable = false")
	}
	if name != "当前飞书应用" {
		t.Fatalf("app name = %q", name)
	}
}

func TestFeishuValidateCredentialsAllowsMissingBotInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token"}`))
			return
		}
		http.Error(w, `{"code":999,"msg":"bot unavailable"}`, http.StatusForbidden)
	}))
	defer server.Close()

	oldBaseURL := feishuBaseURL
	feishuBaseURL = server.URL
	defer func() { feishuBaseURL = oldBaseURL }()

	name, reachable, err := feishuValidateCredentials("cli_test", "secret")
	if err != nil || !reachable || name != "" {
		t.Fatalf("name=%q reachable=%v err=%v", name, reachable, err)
	}
}

func TestFeishuCreateChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token","expire":3600}`))
		case "/open-apis/im/v1/chats":
			if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("Authorization = %q", got)
			}
			if got := r.URL.Query().Get("user_id_type"); got != "open_id" {
				t.Fatalf("user_id_type = %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if got := body["uuid"]; got == nil || got == "" {
				t.Fatalf("uuid = %#v", got)
			}
			if got := body["description"]; !strings.Contains(got.(string), "cicy-code:cli_test:w-10265") {
				t.Fatalf("description = %q", got)
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"chat_id":"oc_created"}}`))
		case "/open-apis/im/v1/messages":
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"message_id":"om_created"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldBaseURL := feishuBaseURL
	feishuBaseURL = server.URL
	defer func() { feishuBaseURL = oldBaseURL }()

	chatID, err := feishuCreateChat(&imAccount{
		ID:       1,
		Platform: imPlatformFeishu,
		Secret:   "secret",
		Config:   map[string]any{"app_id": "cli_test", "last_feishu_open_id": "ou_user"},
	}, "cicy-code · w-10265", "w-10265:main.0")
	if err != nil {
		t.Fatalf("feishuCreateChat: %v", err)
	}
	if chatID != "oc_created" {
		t.Fatalf("chat id = %q", chatID)
	}
}

func TestExistingNamedChatBindingIsScopedByAppAndAgent(t *testing.T) {
	withTestStore(t)
	if _, err := store.Exec(`INSERT INTO im_chat_bindings
		(account_id, chat_id, pane_id, chat_name, binding_type)
		VALUES (12, 'oc_existing', 'w-10265:main.0', 'cicy-code · w-10265', 'group')`); err != nil {
		t.Fatal(err)
	}

	chatID, chatName, ok := imExistingNamedChatBinding(12, "w-10265", "group")
	if !ok || chatID != "oc_existing" || chatName != "cicy-code · w-10265" {
		t.Fatalf("binding ok=%v chatID=%q chatName=%q", ok, chatID, chatName)
	}
	if _, _, ok := imExistingNamedChatBinding(13, "w-10265", "group"); ok {
		t.Fatal("a different Feishu app must not reuse this binding")
	}
	if _, _, ok := imExistingNamedChatBinding(12, "w-10270", "group"); ok {
		t.Fatal("a different Agent must not reuse this binding")
	}
}

func TestFeishuDeleteChat(t *testing.T) {
	var deleted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token","expire":3600}`))
		case "/open-apis/im/v1/chats/oc_orphan":
			deleted = r.Method
			_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldBaseURL := feishuBaseURL
	feishuBaseURL = server.URL
	defer func() { feishuBaseURL = oldBaseURL }()

	err := feishuDeleteChat(&imAccount{
		ID:       1,
		Platform: imPlatformFeishu,
		Secret:   "secret",
		Config:   map[string]any{"app_id": "cli_test"},
	}, "oc_orphan")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != http.MethodDelete {
		t.Fatalf("method = %q", deleted)
	}
}

func TestFeishuOpenDirectChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token","expire":3600}`))
		case "/open-apis/im/v1/messages":
			if got := r.URL.Query().Get("receive_id_type"); got != "open_id" {
				t.Fatalf("receive_id_type = %q", got)
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"success","data":{"chat_id":"oc_direct","message_id":"om_direct"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldBaseURL := feishuBaseURL
	feishuBaseURL = server.URL
	defer func() { feishuBaseURL = oldBaseURL }()

	chatID, err := feishuOpenDirectChat(&imAccount{
		ID:       1,
		Platform: imPlatformFeishu,
		Secret:   "secret",
		Config:   map[string]any{"app_id": "cli_test", "last_feishu_open_id": "ou_user"},
	}, "cicy-code · w-10265")
	if err != nil {
		t.Fatalf("feishuOpenDirectChat: %v", err)
	}
	if chatID != "oc_direct" {
		t.Fatalf("chat id = %q", chatID)
	}
}

func TestFeishuLocalUserIDUsesUnionIDAcrossApps(t *testing.T) {
	binDir := t.TempDir()
	script := filepath.Join(binDir, "lark-cli")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$1 $2" = "auth status" ]; then
  printf '%s\n' '{"appId":"cli_other","identities":{"user":{"available":true,"openId":"ou_other"}}}'
else
  printf '%s\n' '{"ok":true,"data":{"union_id":"on_current"}}'
fi
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	id, idType := feishuLocalUserID("cli_bot")
	if id != "on_current" || idType != "union_id" {
		t.Fatalf("id=%q type=%q", id, idType)
	}
}

func TestFeishuGroupBindMissingPermissions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token","expire":3600}`))
		case "/open-apis/im/v1/chats":
			_, _ = w.Write([]byte(`{"code":99991672,"msg":"missing im:chat:create permission"}`))
		case "/open-apis/im/v1/messages":
			_, _ = w.Write([]byte(`{"code":99991672,"msg":"missing im:message.group_msg permission"}`))
		case "/open-apis/im/v1/images":
			_, _ = w.Write([]byte(`{"code":99991672,"msg":"missing im:resource permission"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldBaseURL := feishuBaseURL
	feishuBaseURL = server.URL
	defer func() { feishuBaseURL = oldBaseURL }()

	missing, err := feishuGroupBindMissingPermissions(&imAccount{
		ID:       1,
		Platform: imPlatformFeishu,
		Secret:   "secret",
		Config:   map[string]any{"app_id": "cli_test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"im:chat:create", "im:message.group_msg", "im:resource"} {
		if !slices.Contains(missing, scope) {
			t.Fatalf("missing permissions %v does not contain %s", missing, scope)
		}
	}
}
