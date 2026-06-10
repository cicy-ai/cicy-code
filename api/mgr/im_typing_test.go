package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// fakeTypingTransport is a minimal botTransport that just counts Typing calls.
type fakeTypingTransport struct {
	typings int32
}

func (f *fakeTypingTransport) Kind() string                          { return "fake" }
func (f *fakeTypingTransport) Poll(string) ([]botMsg, string, error) { return nil, "", nil }
func (f *fakeTypingTransport) Send(botPeer, string) (string, error)  { return "", nil }
func (f *fakeTypingTransport) Edit(botPeer, string, string) error    { return errBotEditUnsupported }
func (f *fakeTypingTransport) Typing(botPeer) error                  { atomic.AddInt32(&f.typings, 1); return nil }
func (f *fakeTypingTransport) CanEdit() bool                         { return false }

func imTypingActive(accID int64, pane string) bool {
	imTypingSessions.mu.Lock()
	defer imTypingSessions.mu.Unlock()
	_, ok := imTypingSessions.m[imTypingKey(accID, pane)]
	return ok
}

// The typing indicator must stay ON for the WHOLE turn — through tool-continuation
// rounds and the client-side tool-run gaps (status working/tool_use) — and only
// cancel when the turn reaches a terminal status (completed/failed). This is the
// IM-side mirror of the worker-status busy window.
func TestIMTypingSpansTurnUntilTerminal(t *testing.T) {
	const acc = int64(990099)
	const pane = "w-typing-test"
	tr := &fakeTypingTransport{}
	peer := botPeer{ChatID: "u1"}
	defer imTypingStop(acc, pane)

	imTypingEnsure(acc, pane, tr, peer)
	if !imTypingActive(acc, pane) {
		t.Fatalf("ensure should start a typing session")
	}
	// The loop fires Typing immediately so the indicator shows at once.
	deadline := time.Now().Add(500 * time.Millisecond)
	for atomic.LoadInt32(&tr.typings) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&tr.typings) == 0 {
		t.Fatalf("expected an immediate typing send on start")
	}

	// Idempotent: a second ensure (next item / next tool round) must not restart.
	imTypingEnsure(acc, pane, tr, peer)
	if !imTypingActive(acc, pane) {
		t.Fatalf("ensure must remain a single live session")
	}

	h := &imReplyPushHook{accID: acc, paneID: pane, transport: tr, peer: peer}

	// A tool round ends (stop_reason=tool_use → status working) — the tool now runs
	// client-side. finalize fires, but typing MUST stay on.
	h.finalize(aiGatewayReplySnapshot{Status: "working"})
	if !imTypingActive(acc, pane) {
		t.Fatalf("working finalize must keep typing on (tool-run gap)")
	}
	h.finalize(aiGatewayReplySnapshot{Status: "tool_use"})
	if !imTypingActive(acc, pane) {
		t.Fatalf("tool_use finalize must keep typing on")
	}

	// Final round completes → typing cancels.
	h.finalize(aiGatewayReplySnapshot{Status: "completed"})
	if imTypingActive(acc, pane) {
		t.Fatalf("completed finalize must stop typing")
	}

	// Failure is also terminal → typing cancels.
	imTypingEnsure(acc, pane, tr, peer)
	if !imTypingActive(acc, pane) {
		t.Fatalf("ensure should restart after a completed turn")
	}
	h.finalize(aiGatewayReplySnapshot{Status: "failed"})
	if imTypingActive(acc, pane) {
		t.Fatalf("failed finalize must stop typing")
	}
}
