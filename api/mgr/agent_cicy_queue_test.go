package main

import (
	"sync"
	"testing"
)

// Headless liveness: a cicy agent is "online" iff its server-side session is
// registered (warmCicySessions / getCicySession), NOT via tmux. getCicySession
// must register on first touch; an untouched id must read as offline.
func TestCicySessionRegisteredSignal(t *testing.T) {
	const id = "w-test-headless-9001"
	cicySessionsMu.Lock()
	delete(cicySessions, id)
	cicySessionsMu.Unlock()

	if cicySessionRegistered(id) {
		t.Fatal("unwarmed cicy id must be offline (not registered)")
	}
	getCicySession(id, t.TempDir()) // registers + loads (empty) history
	if !cicySessionRegistered(id) {
		t.Fatal("getCicySession must register the session (headless online signal)")
	}

	cicySessionsMu.Lock()
	delete(cicySessions, id)
	cicySessionsMu.Unlock()
}

// First caller owns the turn; concurrent callers during in-flight all queue;
// drainPending merges them (newline-joined) into ONE follow-up, in arrival order.
func TestCicyQueueMergesInFlightInputs(t *testing.T) {
	s := &cicySession{}

	if s.enqueueIfBusy("first") {
		t.Fatal("first caller must NOT be queued — it owns the turn")
	}
	// While owner runs, three more arrive — all must queue.
	for _, in := range []string{"a", "b", "c"} {
		if !s.enqueueIfBusy(in) {
			t.Fatalf("input %q during in-flight must be queued", in)
		}
	}
	merged, more := s.drainPending()
	if !more {
		t.Fatal("drain must report queued inputs")
	}
	if merged != "a\nb\nc" {
		t.Errorf("merged = %q, want \"a\\nb\\nc\"", merged)
	}
	// Nothing left → release.
	if _, more := s.drainPending(); more {
		t.Error("second drain should find empty queue")
	}
	// After release a fresh caller owns the next turn again.
	if s.enqueueIfBusy("next") {
		t.Error("after drain-release, next caller must own (not queue)")
	}
}

// forceRelease unwedges the session after an abnormal owner exit.
func TestCicyQueueForceRelease(t *testing.T) {
	s := &cicySession{}
	s.enqueueIfBusy("owner") // becomes busy
	if !s.enqueueIfBusy("x") {
		t.Fatal("should queue while busy")
	}
	s.forceRelease()
	if s.enqueueIfBusy("y") {
		t.Error("after forceRelease, a caller must be able to own again")
	}
}

// Exactly one of N concurrent callers wins ownership; the rest queue. No races
// (run with -race). The owner then drains all queued inputs without loss.
func TestCicyQueueSingleOwnerUnderConcurrency(t *testing.T) {
	s := &cicySession{}
	const n = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	owners := 0
	queued := 0
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if s.enqueueIfBusy("in") {
				mu.Lock()
				queued++
				mu.Unlock()
			} else {
				mu.Lock()
				owners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if owners != 1 {
		t.Fatalf("exactly one owner expected, got %d", owners)
	}
	if queued != n-1 {
		t.Fatalf("queued = %d, want %d", queued, n-1)
	}
	// Owner drains: all n-1 queued inputs survive the merge (no loss).
	merged, more := s.drainPending()
	if !more {
		t.Fatal("owner must drain the queued inputs")
	}
	got := 1
	for _, c := range merged {
		if c == '\n' {
			got++
		}
	}
	if got != n-1 {
		t.Errorf("merged carries %d inputs, want %d (%q)", got, n-1, merged)
	}
}
