package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIMPlatformsEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/im/platforms", nil)
	handleIMRoute(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	plats, _ := resp["platforms"].([]any)
	if len(plats) < 2 {
		t.Fatalf("expected >=2 platforms, got %v", resp["platforms"])
	}
	kinds := map[string]bool{}
	for _, p := range plats {
		m, _ := p.(map[string]any)
		kinds[anyString(m["kind"])] = true
	}
	if !kinds["telegram"] || !kinds["wechat"] {
		t.Fatalf("missing telegram/wechat in platforms: %v", kinds)
	}
}

func TestIMWeChatAccountCRUD(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	imWorkersDisabled = true
	t.Cleanup(func() { imWorkersDisabled = false })

	// wechat accounts can no longer be created via POST /api/im/accounts (must use the scan flow)
	recBad := httptest.NewRecorder()
	handleIMRoute(recBad, httptest.NewRequest("POST", "/api/im/accounts", strings.NewReader(`{"platform":"wechat","name":"x"}`)))
	if recBad.Code != 400 {
		t.Fatalf("expected 400 for direct wechat create, got %d body=%s", recBad.Code, recBad.Body.String())
	}

	// seed a wechat row directly (as the scan flow would after a confirmed login)
	if _, err := store.Exec("INSERT INTO im_accounts (platform, name, secret, config, enabled, state) VALUES ('wechat','我的微信','stored-in-db-dir','{}',1,'connected')"); err != nil {
		t.Fatalf("seed wechat: %v", err)
	}

	// list
	rec2 := httptest.NewRecorder()
	handleIMRoute(rec2, httptest.NewRequest("GET", "/api/im/accounts", nil))
	if rec2.Code != 200 {
		t.Fatalf("list status = %d", rec2.Code)
	}
	var listResp map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &listResp)
	list, _ := listResp["accounts"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 account, got %v", listResp["accounts"])
	}
	acc0, _ := list[0].(map[string]any)
	if anyString(acc0["platform"]) != "wechat" || anyString(acc0["state"]) != "connected" {
		t.Fatalf("unexpected account: %v", acc0)
	}

	// patch name + inbound flag
	rec3 := httptest.NewRecorder()
	handleIMRoute(rec3, httptest.NewRequest("PATCH", "/api/im/accounts/1", strings.NewReader(`{"name":"renamed","inbound_to_agent":false,"enabled":false}`)))
	if rec3.Code != 200 {
		t.Fatalf("patch status = %d body=%s", rec3.Code, rec3.Body.String())
	}
	fresh, err := imGetAccount(1)
	if err != nil || fresh == nil {
		t.Fatalf("get after patch: %v", err)
	}
	if fresh.Name != "renamed" || fresh.InboundToAgent || fresh.Enabled {
		t.Fatalf("patch not applied: %+v", fresh)
	}

	// delete
	rec4 := httptest.NewRecorder()
	handleIMRoute(rec4, httptest.NewRequest("DELETE", "/api/im/accounts/1", nil))
	if rec4.Code != 200 {
		t.Fatalf("delete status = %d body=%s", rec4.Code, rec4.Body.String())
	}
	rec5 := httptest.NewRecorder()
	handleIMRoute(rec5, httptest.NewRequest("GET", "/api/im/accounts", nil))
	var listResp2 map[string]any
	_ = json.Unmarshal(rec5.Body.Bytes(), &listResp2)
	if l, _ := listResp2["accounts"].([]any); len(l) != 0 {
		t.Fatalf("expected 0 accounts after delete, got %v", listResp2["accounts"])
	}
}

func TestIMTelegramCreateWithoutToken(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	imWorkersDisabled = true
	t.Cleanup(func() { imWorkersDisabled = false })

	rec := httptest.NewRecorder()
	handleIMRoute(rec, httptest.NewRequest("POST", "/api/im/accounts", strings.NewReader(`{"platform":"telegram"}`)))
	if rec.Code != 200 {
		t.Fatalf("create telegram (no token) status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	acc, _ := resp["account"].(map[string]any)
	if acc == nil || anyString(acc["platform"]) != "telegram" {
		t.Fatalf("bad account: %v", resp)
	}
	if has, _ := acc["has_secret"].(bool); has {
		t.Fatalf("expected has_secret=false for tokenless telegram account, got %v", acc)
	}
}

func TestIMBindOnePerPlatformPerAgent(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	imWorkersDisabled = true
	t.Cleanup(func() { imWorkersDisabled = false })

	if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, workspace, init_script, config, role, default_model, agent_type, allow_all_actions, reply_in_chinese) VALUES (?,?,?,?,?,?,?,?,?,?)",
		"w-20001:main.0", "Claude", "/tmp/w-20001", "", "{}", "worker", "", "claude", true, true,
	); err != nil {
		t.Fatalf("insert pane: %v", err)
	}
	if _, err := store.Exec("INSERT INTO im_accounts (platform, name, state) VALUES ('wechat','a','pending'),('wechat','b','pending')"); err != nil {
		t.Fatalf("insert accounts: %v", err)
	}

	rec := httptest.NewRecorder()
	handleIMRoute(rec, httptest.NewRequest("POST", "/api/im/accounts/1/bind", strings.NewReader(`{"pane_id":"w-20001"}`)))
	if rec.Code != 200 {
		t.Fatalf("bind 1 status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec2 := httptest.NewRecorder()
	handleIMRoute(rec2, httptest.NewRequest("POST", "/api/im/accounts/2/bind", strings.NewReader(`{"pane_id":"w-20001"}`)))
	if rec2.Code != 409 {
		t.Fatalf("expected 409 binding 2nd wechat to same pane, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	// binding to a non-existent pane → 400
	rec3 := httptest.NewRecorder()
	handleIMRoute(rec3, httptest.NewRequest("POST", "/api/im/accounts/2/bind", strings.NewReader(`{"pane_id":"w-nope"}`)))
	if rec3.Code != 400 {
		t.Fatalf("expected 400 binding to missing pane, got %d", rec3.Code)
	}
	// unbind 1, then 2 can bind
	recU := httptest.NewRecorder()
	handleIMRoute(recU, httptest.NewRequest("POST", "/api/im/accounts/1/unbind", nil))
	if recU.Code != 200 {
		t.Fatalf("unbind status = %d", recU.Code)
	}
	rec4 := httptest.NewRecorder()
	handleIMRoute(rec4, httptest.NewRequest("POST", "/api/im/accounts/2/bind", strings.NewReader(`{"pane_id":"w-20001"}`)))
	if rec4.Code != 200 {
		t.Fatalf("bind 2 after unbind status = %d body=%s", rec4.Code, rec4.Body.String())
	}

	// the reply-hook factory should now return an IM push hook for the bound
	// account once a transport exists. (No transport in tests, so it should
	// return no IM hook — just make sure it doesn't panic. Other independent
	// hooks, e.g. the memory-write hook, may be present and are ignored here.)
	var imHooks int
	for _, h := range newReplyHooksForPane("w-20001", false) {
		if _, ok := h.(*imReplyPushHook); ok {
			imHooks++
		}
	}
	if imHooks != 0 {
		t.Fatalf("expected no IM reply hooks without a live transport, got %d", imHooks)
	}
}

func TestIMChatBindings(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	imWorkersDisabled = true
	t.Cleanup(func() { imWorkersDisabled = false })

	// seed an account + two agents
	res, err := store.Exec("INSERT INTO im_accounts (platform, name, secret, config, enabled, state) VALUES ('feishu','测试飞书','sec','{\"app_id\":\"cli_x\"}',1,'connected')")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	accID, _ := res.LastInsertId()
	for _, p := range []string{"w-201:main.0", "w-202:main.0"} {
		if _, err := store.Exec("INSERT INTO agent_config (pane_id, title, agent_type, active) VALUES (?,?,?,1)", p, "t-"+p, "claude"); err != nil {
			t.Fatalf("seed agent: %v", err)
		}
	}

	// 未绑定 → 空
	if got := imChatBoundPane(accID, "oc_chat_1"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	// 两个会话各绑一个 agent
	if err := imBindChatToPane(accID, "oc_chat_1", "w-201"); err != nil {
		t.Fatalf("bind1: %v", err)
	}
	if err := imBindChatToPane(accID, "oc_chat_2", "w-202:main.0"); err != nil {
		t.Fatalf("bind2: %v", err)
	}
	if got := imChatBoundPane(accID, "oc_chat_1"); got != normPaneID("w-201") {
		t.Fatalf("chat1 pane = %q", got)
	}
	if got := imChatBoundPane(accID, "oc_chat_2"); got != normPaneID("w-202:main.0") {
		t.Fatalf("chat2 pane = %q", got)
	}
	// upsert 覆盖:chat1 换绑到 w-202
	if err := imBindChatToPane(accID, "oc_chat_1", "w-202"); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if got := imChatBoundPane(accID, "oc_chat_1"); got != normPaneID("w-202") {
		t.Fatalf("rebound pane = %q", got)
	}
	// 反查:绑到 w-202 的会话应有两个
	if got := imChatBindingsForPane("w-202:main.0"); len(got) != 2 {
		t.Fatalf("bindings for w-202 = %v", got)
	}
	// 解绑
	if err := imBindChatToPane(accID, "oc_chat_1", ""); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if got := imChatBoundPane(accID, "oc_chat_1"); got != "" {
		t.Fatalf("after unbind pane = %q", got)
	}
}
