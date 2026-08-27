package main

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

// 用户点 stop(UI 发 Ctrl+C)→ CLI 挂断在途请求。网关必须把这一轮记成「已停止生成」
// (completed + cancelled marker),而不是 failed +「生成失败」——否则会话视图把
// 用户主动停止渲染成失败,下一条提问还会按"覆盖失败轮"把上一条 q 抹掉。
func TestUserInterruptSealsRoundAsCancelledNotFailed(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)
	agent := "w-19403"
	base, _ := url.Parse("https://api.anthropic.com")
	hdr := http.Header{"Content-Type": []string{"application/json"}}
	req := []byte(`{"model":"m","metadata":{"session_id":"sess-stop-1"},"tools":[{"name":"Bash"}],"messages":[{"role":"user","content":"跑一下测试"}]}`)

	s := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req)
	if err := s.writeStartSnapshots(); err != nil {
		t.Fatalf("start: %v", err)
	}
	aiGatewayMarkUserInterrupt(agent + ":main.0") // handleSendKeys 的写法:完整 pane id
	s.completeFromError(errors.New("context canceled"))

	if s.reply.Status != "completed" {
		t.Fatalf("reply.Status = %q, want completed (user stop must not be a failure)", s.reply.Status)
	}
	if n := len(s.reply.Items); n == 0 || s.reply.Items[n-1]["cicy_outcome"] != "cancelled" {
		t.Fatalf("missing cancelled outcome marker item: %+v", s.reply.Items)
	}
	for _, it := range s.reply.Items {
		if txt, _ := it["text"].(string); len(txt) > 0 && (txt[:1] == "⚠") {
			t.Fatalf("failure detail leaked into a user-stopped round: %q", txt)
		}
	}
	// 只消费一次:再来一次同样的挂断(没有新的 Ctrl+C)必须仍是 failed。
	s2 := newAIGatewayAuditSession("anthropic", agent, base, "/v1/messages", "POST", hdr, req)
	_ = s2.writeStartSnapshots()
	s2.completeFromError(errors.New("context canceled"))
	if s2.reply.Status != "failed" {
		t.Fatalf("second hang-up without a stop should stay failed, got %q", s2.reply.Status)
	}
}
