package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTranscriptTypedSetIncremental verifies the per-cid incremental-offset
// cache (#4B) preserves the exact typed-set semantics: same filtering, and an
// incremental read after an append equals a full rebuild from scratch. Also
// checks the returned map is a copy (mutating it can't corrupt the cache).
func TestTranscriptTypedSetIncremental(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cid := "conv-typedset-test"
	dir := filepath.Join(tmp, ".claude", "projects", "proj-x")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, cid+".jsonl")

	norm := func(s string) string {
		return aiGatewayNormPrompt(aiGatewaySanitizeUserQuestion(s))
	}
	append := func(lines ...string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, l := range lines {
			if _, err := f.WriteString(l + "\n"); err != nil {
				t.Fatal(err)
			}
		}
	}
	resetCache := func() {
		aiGatewayTypedSetMu.Lock()
		aiGatewayTypedSetCache = map[string]*aiGatewayTypedSetEntry{}
		aiGatewayTypedSetMu.Unlock()
	}

	resetCache()
	append(
		`{"type":"user","promptSource":"typed","message":{"content":"alpha"}}`,
		`{"type":"user","promptSource":"queued","message":{"content":"bravo"}}`,
		`{"type":"user","message":{"content":"tool-result-noise"}}`,             // no promptSource → excluded
		`{"type":"assistant","promptSource":"typed","message":{"content":"c"}}`, // not user → excluded
	)

	set := aiGatewayTranscriptTypedSet(cid)
	if len(set) != 2 || !set[norm("alpha")] || !set[norm("bravo")] {
		t.Fatalf("want {alpha,bravo}, got %v", set)
	}

	// Append → incremental path must pick up the new line and keep the old.
	append(`{"type":"user","promptSource":"typed","message":{"content":"delta"}}`)
	inc := aiGatewayTranscriptTypedSet(cid)
	if len(inc) != 3 || !inc[norm("delta")] || !inc[norm("alpha")] {
		t.Fatalf("incremental want {alpha,bravo,delta}, got %v", inc)
	}

	// Full rebuild (fresh cache) must equal the incremental result.
	resetCache()
	full := aiGatewayTranscriptTypedSet(cid)
	if len(full) != len(inc) {
		t.Fatalf("full %d != incremental %d", len(full), len(inc))
	}
	for k := range inc {
		if !full[k] {
			t.Fatalf("full result missing %q", k)
		}
	}

	// Returned map is a copy — mutating it must not corrupt the cache.
	full["injected-key"] = true
	again := aiGatewayTranscriptTypedSet(cid)
	if again["injected-key"] {
		t.Fatal("returned map is the live cache, not a copy")
	}
}

// TestTranscriptTypedSetShrinkRebuilds verifies a file that shrinks (e.g. a new
// cid would be a new file, but a same-path shrink is the conservative trigger)
// forces a full rebuild rather than a stale-offset gap.
func TestTranscriptTypedSetShrinkRebuilds(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cid := "conv-shrink-test"
	dir := filepath.Join(tmp, ".claude", "projects", "proj-y")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, cid+".jsonl")
	norm := func(s string) string { return aiGatewayNormPrompt(aiGatewaySanitizeUserQuestion(s)) }

	aiGatewayTypedSetMu.Lock()
	aiGatewayTypedSetCache = map[string]*aiGatewayTypedSetEntry{}
	aiGatewayTypedSetMu.Unlock()

	os.WriteFile(path, []byte(`{"type":"user","promptSource":"typed","message":{"content":"first"}}`+"\n"+
		`{"type":"user","promptSource":"typed","message":{"content":"second"}}`+"\n"), 0644)
	if s := aiGatewayTranscriptTypedSet(cid); len(s) != 2 {
		t.Fatalf("want 2, got %v", s)
	}

	// Truncate + rewrite smaller (size < lastOffset) → must rebuild from scratch.
	os.WriteFile(path, []byte(`{"type":"user","promptSource":"typed","message":{"content":"fresh"}}`+"\n"), 0644)
	s := aiGatewayTranscriptTypedSet(cid)
	if len(s) != 1 || !s[norm("fresh")] {
		t.Fatalf("after shrink want {fresh}, got %v", s)
	}
}
