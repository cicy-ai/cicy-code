// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// A preinstalled roster pane is seeded once: after the operator deletes it,
// the marker must still report it as seeded so startup top-up skips it.
func TestRosterSeedMarkerSurvivesDeletion(t *testing.T) {
	withTestStore(t)

	const pane = "w-1003:main.0"
	if rosterPaneWasSeeded(pane) {
		t.Fatalf("fresh store must not report %s as seeded", pane)
	}

	markRosterPaneSeeded(pane)
	markRosterPaneSeeded(pane) // idempotent
	markRosterPaneSeeded("w-1004:main.0")

	if !rosterPaneWasSeeded(pane) || !rosterPaneWasSeeded("w-1004:main.0") {
		t.Fatalf("marked panes must be reported as seeded: %v", loadSeededRosterPanes())
	}
	if got := len(loadSeededRosterPanes()); got != 2 {
		t.Fatalf("seeded set size = %d, want 2 (no duplicates)", got)
	}
	if rosterPaneWasSeeded("w-1999:main.0") {
		t.Fatalf("never-seeded pane must not be reported as seeded")
	}
}
