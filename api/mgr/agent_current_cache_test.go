package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCurrentJSON writes a current.json for agentID with the given conversation
// id and a body carrying `items` history entries, and returns its size.
func writeCurrentJSON(t *testing.T, agentID, convID string, items int) int64 {
	t.Helper()
	msgs := make([]interface{}, 0, items)
	for i := 1; i <= items; i++ {
		msgs = append(msgs, map[string]interface{}{
			"id":      i,
			"role":    "user",
			"content": fmt.Sprintf("q%d", i),
		})
	}
	snap := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: convID,
		Body:           map[string]interface{}{"messages": msgs},
	}
	path := currentSnapshotPath(agentID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

// countingReads swaps in a temp cicy root and reports how many times the file was
// actually opened, by watching atime is unreliable — instead we detect a re-parse
// by mutating the file's CONTENT behind the cache's back without changing its
// (mtime, size). A cache hit keeps serving the old parse; a miss would see the new.
func TestCurrentSnapshotCacheServesRepeatPollsWithoutReparsing(t *testing.T) {
	withTempCicyRoot(t)
	dropCurrentSnapshotCache("w-9001")

	writeCurrentJSON(t, "w-9001", "conv-a", 3)

	first, err := aiGatewayReadCurrentSnapshotCached("w-9001")
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if first.ConversationID != "conv-a" {
		t.Fatalf("conversation_id = %q, want conv-a", first.ConversationID)
	}

	// Rewrite the file with SAME size and SAME mtime but different content. A real
	// re-read would surface "conv-b"; a cache hit must still say "conv-a".
	path := currentSnapshotPath("w-9001")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCurrentJSON(t, "w-9001", "conv-b", 3) // same shape → same size
	st2, _ := os.Stat(path)
	if st2.Size() != st.Size() {
		t.Fatalf("test setup: sizes differ (%d vs %d), cannot prove the cache hit", st.Size(), st2.Size())
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}

	second, err := aiGatewayReadCurrentSnapshotCached("w-9001")
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second.ConversationID != "conv-a" {
		t.Fatalf("cache MISS: got %q — the poll re-read and re-parsed an unchanged file "+
			"(this is the 79.7%%-of-all-allocation bug)", second.ConversationID)
	}
}

func TestCurrentSnapshotCacheInvalidatesWhenFileChanges(t *testing.T) {
	withTempCicyRoot(t)
	dropCurrentSnapshotCache("w-9002")

	writeCurrentJSON(t, "w-9002", "conv-a", 3)
	if snap, err := aiGatewayReadCurrentSnapshotCached("w-9002"); err != nil || snap.ConversationID != "conv-a" {
		t.Fatalf("first read: %v / %q", err, snap.ConversationID)
	}

	// A real commit: new content, new mtime. The cache MUST NOT serve the stale
	// parse — a frozen chat view is exactly what a bad cache here would cause.
	time.Sleep(10 * time.Millisecond)
	writeCurrentJSON(t, "w-9002", "conv-b", 7)

	snap, err := aiGatewayReadCurrentSnapshotCached("w-9002")
	if err != nil {
		t.Fatal(err)
	}
	if snap.ConversationID != "conv-b" {
		t.Fatalf("STALE: conversation_id = %q, want conv-b — the cache did not notice the file changed", snap.ConversationID)
	}
	if _, maxID := agentHistoryCurrentMaxIDFrom(snap, ""); maxID != 7 {
		t.Fatalf("max history id = %d, want 7 (the new snapshot's items)", maxID)
	}
}

func TestCurrentSnapshotCacheMissingFileIsNotAnError500(t *testing.T) {
	withTempCicyRoot(t)
	dropCurrentSnapshotCache("w-9003")

	_, err := aiGatewayReadCurrentSnapshotCached("w-9003")
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want a NotExist error — a fresh agent has no current.json "+
			"and the poll must return an empty snapshot, not HTTP 500", err)
	}
}

func TestCurrentSnapshotCacheIsBounded(t *testing.T) {
	withTempCicyRoot(t)

	for i := 0; i < currentSnapshotCacheMax*3; i++ {
		id := fmt.Sprintf("w-92%02d", i)
		writeCurrentJSON(t, id, "conv", 2)
		if _, err := aiGatewayReadCurrentSnapshotCached(id); err != nil {
			t.Fatal(err)
		}
	}
	currentSnapshotCache.mu.Lock()
	n := len(currentSnapshotCache.items)
	currentSnapshotCache.mu.Unlock()
	if n > currentSnapshotCacheMax {
		t.Fatalf("cache holds %d entries, max is %d — an unbounded cache of multi-MB "+
			"snapshots is the leak this was written to prevent", n, currentSnapshotCacheMax)
	}
}

func TestDropCurrentSnapshotCacheReleasesTheAgent(t *testing.T) {
	withTempCicyRoot(t)
	writeCurrentJSON(t, "w-9004", "conv-a", 2)
	if _, err := aiGatewayReadCurrentSnapshotCached("w-9004"); err != nil {
		t.Fatal(err)
	}
	dropCurrentSnapshotCache("w-9004")

	currentSnapshotCache.mu.Lock()
	_, present := currentSnapshotCache.items["w-9004"]
	currentSnapshotCache.mu.Unlock()
	if present {
		t.Fatal("deleted pane still pinned in the snapshot cache")
	}
}

// The poll handler reads current.json ONCE. This pins the helper that made that
// possible: given an already-parsed snapshot it must agree with the read-from-disk
// path, so nobody can "simplify" it back into a second full read.
func TestMaxIDFromSnapshotMatchesTheDiskPath(t *testing.T) {
	withTempCicyRoot(t)
	writeCurrentJSON(t, "w-9005", "conv-a", 5)

	wantConv, wantMax, err := agentHistoryCurrentMaxID("w-9005", "")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := aiGatewayReadCurrentSnapshot("w-9005")
	if err != nil {
		t.Fatal(err)
	}
	gotConv, gotMax := agentHistoryCurrentMaxIDFrom(snap, "")
	if gotConv != wantConv || gotMax != wantMax {
		t.Fatalf("from-snapshot (%q,%d) != from-disk (%q,%d)", gotConv, gotMax, wantConv, wantMax)
	}
	if wantMax != 5 {
		t.Fatalf("max = %d, want 5", wantMax)
	}
	// A conversation_id that doesn't match the snapshot's resolves to itself, id 0.
	if conv, max := agentHistoryCurrentMaxIDFrom(snap, "other"); conv != "other" || max != 0 {
		t.Fatalf("mismatched conversation → (%q,%d), want (other,0)", conv, max)
	}
}
