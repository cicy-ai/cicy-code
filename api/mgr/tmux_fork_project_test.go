// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestAddForkToProjectAddsTheNewAgentToOnlyTheRequestedProject(t *testing.T) {
	withTestStore(t)

	result, err := store.Exec("INSERT INTO agent_groups (name) VALUES (?)", "Fork target")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	projectID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("project id: %v", err)
	}

	if err := addForkToProject(projectID, "w-999:main.0"); err != nil {
		t.Fatalf("add fork to project: %v", err)
	}

	var count int
	if err := store.QueryRow(
		"SELECT COUNT(*) FROM group_windows WHERE group_id=? AND win_id=? AND win_type='agent_ttyd'",
		projectID, "w-999:main.0",
	).Scan(&count); err != nil {
		t.Fatalf("query fork membership: %v", err)
	}
	if count != 1 {
		t.Fatalf("fork project membership count = %d, want 1", count)
	}
	var width float64
	if err := store.QueryRow(
		"SELECT width FROM group_windows WHERE group_id=? AND win_id=?",
		projectID, "w-999:main.0",
	).Scan(&width); err != nil {
		t.Fatalf("query fork width: %v", err)
	}
	if width != defaultProjectAgentWidth {
		t.Fatalf("fork width = %v, want %d", width, defaultProjectAgentWidth)
	}

	var otherCount int
	if err := store.QueryRow(
		"SELECT COUNT(*) FROM group_windows WHERE group_id<>? AND win_id=?",
		projectID, "w-999:main.0",
	).Scan(&otherCount); err != nil {
		t.Fatalf("query other memberships: %v", err)
	}
	if otherCount != 0 {
		t.Fatalf("fork was added to %d unrelated projects", otherCount)
	}
}

func TestForkTargetProjectsInheritsSourceProjects(t *testing.T) {
	withTestStore(t)
	mk := func(name string) int64 {
		r, err := store.Exec("INSERT INTO agent_groups (name) VALUES (?)", name)
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		id, _ := r.LastInsertId()
		return id
	}
	a, b, other := mk("A"), mk("B"), mk("other")
	for _, g := range []int64{a, b} {
		if err := addForkToProject(g, "w-500:main.0"); err != nil {
			t.Fatal(err)
		}
	}
	if err := addForkToProject(other, "w-600:main.0"); err != nil {
		t.Fatal(err)
	}

	got := forkTargetProjects(0, "w-500")
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("no explicit project: got %v, want [%d %d] (source's projects)", got, a, b)
	}
	if got := forkTargetProjects(other, "w-500"); len(got) != 1 || got[0] != other {
		t.Fatalf("explicit project must win: got %v", got)
	}
	if got := forkTargetProjects(0, "w-777:main.0"); len(got) != 0 {
		t.Fatalf("source in no project: got %v, want none", got)
	}
}
