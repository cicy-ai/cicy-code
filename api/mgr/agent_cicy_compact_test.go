package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// makeCicyMsgs builds n alternating user/assistant string messages (user at even
// indices), each tagged so we can assert which survived compaction.
func makeCicyMsgs(n int) []M {
	out := make([]M, 0, n)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		out = append(out, M{"role": role, "content": fmt.Sprintf("msg-%d", i)})
	}
	return out
}

func TestCicyCompactSplitPoint(t *testing.T) {
	// Under the threshold → no compaction.
	if got := cicyCompactSplitPoint(makeCicyMsgs(cicyCompactThreshold)); got != -1 {
		t.Errorf("at/under threshold should not compact, got %d", got)
	}
	// Over the threshold → keep the last cicyCompactKeepRecent verbatim.
	n := cicyCompactThreshold + 11
	if got := cicyCompactSplitPoint(makeCicyMsgs(n)); got != n-cicyCompactKeepRecent {
		t.Errorf("split point = %d, want %d", got, n-cicyCompactKeepRecent)
	}
}

// The kept tail must never START on a tool_result (its tool_use would be in the
// summarized half → orphan → provider 400). The boundary advances forward.
func TestCicyCompactSplitPointAdvancesPastToolResult(t *testing.T) {
	n := cicyCompactThreshold + 11
	msgs := makeCicyMsgs(n)
	base := n - cicyCompactKeepRecent
	// Plant a tool_result exactly at the natural boundary → split must move past it.
	msgs[base] = M{"role": "user", "content": []M{{"type": "tool_result", "tool_use_id": "x", "content": "r"}}}
	got := cicyCompactSplitPoint(msgs)
	if got != base+1 {
		t.Fatalf("split should advance past a tool_result boundary: got %d, want %d", got, base+1)
	}
}

func TestCicyCompactMessagesAssembles(t *testing.T) {
	prev := cicyCompactSummarize
	defer func() { cicyCompactSummarize = prev }()
	cicyCompactSummarize = func(ctx context.Context, shortID, convID, model, transcript string) (string, error) {
		return "SUMMARY-BODY", nil
	}

	n := cicyCompactThreshold + 25
	out, ok := cicyCompactMessages(context.Background(), "w-1", makeCicyMsgs(n), "m")
	if !ok {
		t.Fatal("expected compaction to fire")
	}
	// 1 summary message + the kept tail.
	if len(out) != cicyCompactKeepRecent+1 {
		t.Fatalf("compacted length = %d, want %d", len(out), cicyCompactKeepRecent+1)
	}
	first := out[0]
	if first["role"] != "user" {
		t.Errorf("summary message role = %v, want user", first["role"])
	}
	body, _ := first["content"].(string)
	if !strings.HasPrefix(body, cicyCompactSummaryPrefix) || !strings.Contains(body, "SUMMARY-BODY") {
		t.Errorf("summary message missing prefix/body: %q", body)
	}
	// The tail is preserved verbatim: the last original message is still last.
	if last, _ := out[len(out)-1]["content"].(string); last != fmt.Sprintf("msg-%d", n-1) {
		t.Errorf("tail not preserved: last content = %q", last)
	}
}

// A failed/empty summary must NOT mutate history — caller falls back to trim.
func TestCicyCompactMessagesFailFallsBack(t *testing.T) {
	prev := cicyCompactSummarize
	defer func() { cicyCompactSummarize = prev }()
	cicyCompactSummarize = func(ctx context.Context, shortID, convID, model, transcript string) (string, error) {
		return "", fmt.Errorf("provider down")
	}
	in := makeCicyMsgs(cicyCompactThreshold + 25)
	out, ok := cicyCompactMessages(context.Background(), "w-1", in, "m")
	if ok {
		t.Error("expected compaction to report failure")
	}
	if len(out) != len(in) {
		t.Errorf("history must be untouched on failure: len %d → %d", len(in), len(out))
	}

	// Empty (non-error) summary is also a no-op.
	cicyCompactSummarize = func(ctx context.Context, shortID, convID, model, transcript string) (string, error) { return "   ", nil }
	if _, ok := cicyCompactMessages(context.Background(), "w-1", in, "m"); ok {
		t.Error("blank summary should not compact")
	}
}

func TestCicyToolResultText(t *testing.T) {
	if got := cicyToolResultText("plain"); got != "plain" {
		t.Errorf("string passthrough = %q", got)
	}
	blocks := []interface{}{
		map[string]interface{}{"type": "text", "text": "a"},
		map[string]interface{}{"type": "text", "text": "b"},
	}
	if got := cicyToolResultText(blocks); got != "a\nb" {
		t.Errorf("block join = %q", got)
	}
}
