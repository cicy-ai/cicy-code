package main

import (
	"testing"
	"time"
)

// Pane tail as kiro-cli renders it at ~55 columns (from cicy-prod-1).
const kiroThrottledTail = `  ./Chat/ChatControllerMediaRecording.swift:177:
    let controller = VideoMessageCameraScreen(

• The request was throttled by the service (request_id:
  f085b20e-2c10-4947-be0a-6b329951a257)

▸ Credits: turn 2.97 • session 59.23 | Time: 1m 22s

────────────────────────────────────────────────────────
Trust All Tools active, confirmations are off · /quit to exit
────────────────────────────────────────────────────────
kiro_default · claude-opus-5.5 · ◔ 22% ~/cicy-ai/workers/w-1006

› ask a question or describe a task ↵
                                         /copy to clipboard
`

func TestKiroThrottleID(t *testing.T) {
	if got := kiroThrottleID(kiroThrottledTail); got != "f085b20e-2c10-4947-be0a-6b329951a257" {
		t.Fatalf("id = %q", got)
	}
	// Same error with "5 tasks remaining" and the id wrapped mid-token.
	wrapped := "• The request was throttled by the ser\nvice (request_id: 064a5cba-5057-4e13-8eb7-\nf18129fce54a)\n▸ Credits: turn 9.22\n● 5 tasks remaining · ctrl+x expand\nkiro_default · claude-opus-5.5\n› ask a question\n"
	if got := kiroThrottleID(wrapped); got != "064a5cba-5057-4e13-8eb7-f18129fce54a" {
		t.Fatalf("wrapped id = %q", got)
	}
	// The error is old: a later turn produced plenty of output.
	old := kiroThrottledTail
	for i := 0; i < 20; i++ {
		old += "Shell ls -la\nCompleted in 0.2s\n"
	}
	if got := kiroThrottleID(old); got != "" {
		t.Fatalf("stale throttle must be ignored, got %q", got)
	}
	if got := kiroThrottleID("› ask a question or describe a task\n"); got != "" {
		t.Fatalf("no throttle, got %q", got)
	}
}

func TestKiroThrottleRetrierBackoffAndStagger(t *testing.T) {
	r := newKiroThrottleRetrier()
	t0 := time.Unix(1_800_000_000, 0)

	if r.observe("w-1:main.0", "id-1", t0) {
		t.Fatal("must wait the base delay first")
	}
	if !r.observe("w-1:main.0", "id-1", t0.Add(kiroThrottleBaseDelay)) {
		t.Fatal("retry expected after base delay")
	}
	if r.observe("w-1:main.0", "id-1", t0.Add(time.Hour)) {
		t.Fatal("one retry per request id")
	}

	// Another pane throttled at the same moment waits for the stagger gap.
	if r.observe("w-2:main.0", "id-2", t0) || r.observe("w-2:main.0", "id-2", t0.Add(kiroThrottleBaseDelay+5*time.Second)) {
		t.Fatal("second pane must be staggered")
	}
	if !r.observe("w-2:main.0", "id-2", t0.Add(kiroThrottleBaseDelay+kiroThrottleStagger)) {
		t.Fatal("second pane retry expected after stagger")
	}

	// w-1 thrown back after the retry: the busy gap must not reset backoff.
	t1 := t0.Add(2 * time.Minute)
	r.observe("w-1:main.0", "", t1)
	r.observe("w-1:main.0", "id-3", t1)
	if r.observe("w-1:main.0", "id-3", t1.Add(kiroThrottleBaseDelay)) {
		t.Fatal("second consecutive throttle must back off longer")
	}
	if !r.observe("w-1:main.0", "id-3", t1.Add(kiroThrottleDelay(1))) {
		t.Fatal("retry expected after doubled delay")
	}
}

func TestKiroThrottleRetrierGivesUpAndRecovers(t *testing.T) {
	r := newKiroThrottleRetrier()
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < kiroThrottleMaxRetries; i++ {
		id := string(rune('a' + i))
		r.observe("w-1", id, now)
		now = now.Add(kiroThrottleMaxDelay)
		if !r.observe("w-1", id, now) {
			t.Fatalf("retry %d expected", i+1)
		}
	}
	r.observe("w-1", "z", now)
	if r.observe("w-1", "z", now.Add(time.Hour)) {
		t.Fatal("must give up after max retries")
	}
	// Quiet for a while → recovered → retries again.
	now = now.Add(time.Hour)
	r.observe("w-1", "", now.Add(kiroThrottleRecovered))
	later := now.Add(2 * kiroThrottleRecovered)
	r.observe("w-1", "y", later)
	if !r.observe("w-1", "y", later.Add(kiroThrottleBaseDelay)) {
		t.Fatal("retries must resume after recovery")
	}
}

func TestKiroThrottleDelayCaps(t *testing.T) {
	if kiroThrottleDelay(0) != 30*time.Second || kiroThrottleDelay(1) != time.Minute || kiroThrottleDelay(10) != kiroThrottleMaxDelay {
		t.Fatalf("delays: %v %v %v", kiroThrottleDelay(0), kiroThrottleDelay(1), kiroThrottleDelay(10))
	}
}
