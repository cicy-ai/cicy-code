package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var uuidV4Shape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestCicyNewConversationIDIsRandomUUID(t *testing.T) {
	a := cicyNewConversationID()
	b := cicyNewConversationID()
	if !uuidV4Shape.MatchString(a) {
		t.Fatalf("conversation id %q is not a v4 UUID", a)
	}
	if a == b {
		t.Fatalf("two generated conversation ids must differ, both were %q", a)
	}
}

func TestCicyLoadOrCreateConvIDPersistsAcrossLoads(t *testing.T) {
	ws := t.TempDir()
	first := cicyLoadOrCreateConvID(ws)
	if !uuidV4Shape.MatchString(first) {
		t.Fatalf("minted id %q is not a v4 UUID", first)
	}
	// Same workspace → same id (restart survival).
	if again := cicyLoadOrCreateConvID(ws); again != first {
		t.Fatalf("reload must return the persisted id %q, got %q", first, again)
	}
	raw, err := os.ReadFile(cicyConvIDPath(ws))
	if err != nil {
		t.Fatalf("conversation_id file must exist: %v", err)
	}
	if strings.TrimSpace(string(raw)) != first {
		t.Fatalf("persisted %q != returned %q", strings.TrimSpace(string(raw)), first)
	}
}

func TestCicyLoadOrCreateConvIDHonorsExistingFile(t *testing.T) {
	ws := t.TempDir()
	dir := cicyConvDir(ws)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conversation_id"), []byte("my-pinned-id\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := cicyLoadOrCreateConvID(ws); got != "my-pinned-id" {
		t.Fatalf("existing id must be honored, got %q", got)
	}
}
