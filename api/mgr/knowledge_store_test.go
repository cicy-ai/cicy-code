package main

import (
	"os"
	"path/filepath"
	"testing"
)

func knowledgeStatusOf(t *testing.T, id string) string {
	t.Helper()
	k, ok, err := getKnowledge(id)
	if err != nil || !ok {
		t.Fatalf("getKnowledge(%s): ok=%v err=%v", id, ok, err)
	}
	return k.Status
}

// Insert lands in _inbox (pending); promote moves it into a canon domain folder;
// recall (keyword + tag) finds it among canon. All file-backed.
func TestKnowledgeInsertPromoteRecall(t *testing.T) {
	withTempCicyRoot(t)

	id, err := insertKnowledge(knowledgeRow{
		ID: "deploy-runbook", Title: "Deploy runbook",
		Body: "Run dev.py --quick --preview to restart 8008.",
		Tags: "deploy ops", SourcePane: "w-10001", SourceKind: "manual",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// the proposal file physically exists in _inbox.
	if _, err := os.Stat(filepath.Join(knowledgeInboxDir(), id+".md")); err != nil {
		t.Fatalf("inbox file missing: %v", err)
	}
	if s := knowledgeStatusOf(t, id); s != "pending" {
		t.Fatalf("fresh insert status = %s, want pending", s)
	}

	pend, _ := listKnowledge(knowledgeFilter{Status: "pending"})
	if len(pend) != 1 || pend[0].ID != id {
		t.Fatalf("pending list: %+v", pend)
	}

	// promote → canon, lands under the default domain folder.
	if err := promoteKnowledge(id, "", "w-10131"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	k, _, _ := getKnowledge(id)
	if k.Status != "canon" || k.Domain != "general" {
		t.Fatalf("after promote: status=%s domain=%s", k.Status, k.Domain)
	}
	if k.VerifiedBy != normPaneID("w-10131") {
		t.Fatalf("verified_by not recorded: %q", k.VerifiedBy)
	}
	if _, err := os.Stat(filepath.Join(knowledgeRootDir(), "general", id+".md")); err != nil {
		t.Fatalf("canon file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(knowledgeInboxDir(), id+".md")); !os.IsNotExist(err) {
		t.Fatalf("inbox file should be gone after promote")
	}

	hits, _ := listKnowledge(knowledgeFilter{Status: "canon", Q: "restart"})
	if len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("recall by keyword: %+v", hits)
	}
	byTag, _ := listKnowledge(knowledgeFilter{Status: "canon", Tag: "deploy"})
	if len(byTag) != 1 {
		t.Fatalf("recall by tag: %+v", byTag)
	}
	miss, _ := listKnowledge(knowledgeFilter{Q: "nonexistent-term-xyz"})
	if len(miss) != 0 {
		t.Fatalf("non-matching recall should be empty: %+v", miss)
	}
}

func TestKnowledgeReject(t *testing.T) {
	withTempCicyRoot(t)
	id, err := insertKnowledge(knowledgeRow{ID: "k2", Title: "t", Body: "b"})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := rejectKnowledge(id, "w-10131"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if s := knowledgeStatusOf(t, id); s != "rejected" {
		t.Fatalf("status = %s, want rejected", s)
	}
	if _, err := os.Stat(filepath.Join(knowledgeArchiveDir(), id+".md")); err != nil {
		t.Fatalf("archive file missing: %v", err)
	}
}

func TestKnowledgeSupersede(t *testing.T) {
	withTempCicyRoot(t)
	oldID, _ := insertKnowledge(knowledgeRow{ID: "old", Title: "v1", Body: "old body"})
	newID, _ := insertKnowledge(knowledgeRow{ID: "new", Title: "v2", Body: "new body"})

	if err := supersedeKnowledge(oldID, newID, "w-10131"); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	k, _, _ := getKnowledge(oldID)
	if k.Status != "rejected" || k.SupersededBy != newID {
		t.Fatalf("supersede wrong: status=%s superseded_by=%s", k.Status, k.SupersededBy)
	}
}

// source defaults to manual; a unique slug is derived from the title when no id.
func TestKnowledgeSlugAndDefaults(t *testing.T) {
	withTempCicyRoot(t)
	id, _ := insertKnowledge(knowledgeRow{Title: "My First Note", Body: "b"})
	if id != "my-first-note" {
		t.Fatalf("slug = %q, want my-first-note", id)
	}
	k, _, _ := getKnowledge(id)
	if k.SourceKind != "manual" {
		t.Fatalf("default source = %q, want manual", k.SourceKind)
	}
	// a second note with the same title gets a unique slug.
	id2, _ := insertKnowledge(knowledgeRow{Title: "My First Note", Body: "b2"})
	if id2 == id {
		t.Fatalf("duplicate title should get a unique slug, got %q twice", id)
	}
}
