package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// writeCurrentForSidechain drops a current.json whose first user message and
// age are exactly what the sidechain classifier reads.
func writeCurrentForSidechain(t *testing.T, agentID, convID, firstUserText string, at time.Time) {
	t.Helper()
	stamp := at.UTC().Format(time.RFC3339)
	current := aiGatewayCurrentSnapshot{
		AgentID:        agentID,
		ConversationID: convID,
		Status:         "thinking",
		Timestamp:      stamp,
		StartedAt:      stamp,
		UpdatedAt:      stamp,
		Body: map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": firstUserText},
			},
		},
	}
	path := filepath.Join(aiGatewayHistoryDir(agentID), "current.json")
	if err := aiGatewayWriteJSONAtomic(path, current); err != nil {
		t.Fatalf("write current.json: %v", err)
	}
}

func sidechainWireBody(firstUserText string) map[string]interface{} {
	return map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": firstUserText},
		},
	}
}

// A subagent runs under a mainline turn that refreshed current.json moments
// ago, so a first-message mismatch against a FRESH snapshot really is a
// sidechain.
func TestSidechainRequestDetectedAgainstFreshSnapshot(t *testing.T) {
	withTempCicyRoot(t)

	const agentID, convID = "w-9201", "conv-fresh"
	writeCurrentForSidechain(t, agentID, convID, "mainline question", time.Now())

	if !aiGatewayIsSidechainRequest(agentID, convID, http.Header{}, sidechainWireBody("subagent thread prompt")) {
		t.Fatal("a mismatch against a fresh snapshot should still be treated as a sidechain")
	}
}

// Only a mainline request refreshes current.json, so a stale snapshot is a
// deadlock: every later mainline mismatches it, gets tagged sidechain, and so
// never refreshes it. w-101 stayed wedged for hours that way — reply.json
// frozen on a failed turn, model and cost stuck at that moment. Past the TTL the
// mainline has to be able to reclaim the snapshot.
func TestStaleSnapshotLetsMainlineReclaimInsteadOfLoopingAsSidechain(t *testing.T) {
	withTempCicyRoot(t)

	const agentID, convID = "w-9202", "conv-stale"
	writeCurrentForSidechain(t, agentID, convID, "question from the turn that failed",
		time.Now().Add(-aiGatewayMainlineSnapshotTTL-time.Minute))

	if aiGatewayIsSidechainRequest(agentID, convID, http.Header{}, sidechainWireBody("a new mainline question")) {
		t.Fatal("a mainline request must reclaim a snapshot that no mainline has refreshed for longer than the TTL")
	}
}

// The reclaim is a rescue for an abandoned snapshot, not a blanket disable: a
// snapshot inside the window keeps classifying normally.
func TestSnapshotJustInsideTTLStillClassifiesSidechain(t *testing.T) {
	withTempCicyRoot(t)

	const agentID, convID = "w-9203", "conv-recent"
	writeCurrentForSidechain(t, agentID, convID, "mainline question",
		time.Now().Add(-aiGatewayMainlineSnapshotTTL+time.Minute))

	if !aiGatewayIsSidechainRequest(agentID, convID, http.Header{}, sidechainWireBody("subagent thread prompt")) {
		t.Fatal("a snapshot inside the TTL should still classify a mismatching request as a sidechain")
	}
}
