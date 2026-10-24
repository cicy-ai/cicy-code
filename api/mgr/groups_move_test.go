// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http/httptest"
	"testing"
)

func groupsOf(t *testing.T, short string) map[int64]bool {
	t.Helper()
	rows, err := store.Query("SELECT group_id FROM group_windows WHERE win_id=? OR win_id=?", short, short+":main.0")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out[id] = true
	}
	return out
}

// "移动到" must leave the agent in ONLY the target project, regardless of
// whether memberships were stored as "w-N" or "w-N:main.0" and which form the
// caller sends.
func TestMoveAgentToGroupRemovesFromOtherProjects(t *testing.T) {
	for _, tc := range []struct{ stored, sent string }{
		{"w-700:main.0", "w-700:main.0"},
		{"w-700:main.0", "w-700"},
		{"w-700", "w-700:main.0"},
		{"w-700", "w-700"},
	} {
		t.Run(tc.stored+"<-"+tc.sent, func(t *testing.T) {
			withTestStore(t)
			for _, name := range []string{"src-a", "src-b", "target"} {
				if _, err := store.Exec("INSERT INTO agent_groups (name) VALUES (?)", name); err != nil {
					t.Fatal(err)
				}
			}
			for _, g := range []int{1, 2} {
				store.Exec(store.InsertIgnore("group_windows", []string{"group_id", "win_id", "win_type", "ref_id", "width"}), g, tc.stored, "agent_ttyd", tc.stored, defaultProjectAgentWidth)
			}
			addWindowToGroup(httptest.NewRecorder(), "3", tc.sent, "agent_ttyd", tc.sent, "pane_id", false)
			got := groupsOf(t, "w-700")
			if len(got) != 1 || !got[3] {
				t.Fatalf("after move memberships = %v, want only target (3)", got)
			}
		})
	}
}
