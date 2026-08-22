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
