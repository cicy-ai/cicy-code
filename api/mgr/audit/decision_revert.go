// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

// RevertDecision is the high-level API behind /api/audit/decisions/revert/<id>.
//
// Mechanism: look up the AutonomyDecision by ID, take its GitSHA (set by
// the autonomy tick after a successful policy.json mutation), and call
// `git revert --no-edit <sha>` in ~/cicy-ai/audit/. The audit pipeline's
// fsnotify watcher picks up the resulting policy.json change within
// ~200ms and reloads.
//
// Each revert is itself an event appended to decisions.ndjson with
// trigger="revert" and an Action of kind="revert" listing the original
// decision ID — so the forensic timeline always shows BOTH the original
// action and the rollback.

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RevertDecisionResult is the JSON returned by /revert.
type RevertDecisionResult struct {
	RevertedDecisionID string `json:"reverted_decision_id"`
	OriginalGitSHA     string `json:"original_git_sha"`
	NewGitSHA          string `json:"new_git_sha"`
	NewDecisionID      string `json:"new_decision_id"`
}

// RevertDecision performs a git-backed rollback. Returns an error if:
//   - The decision ID is not found
//   - The decision didn't actually apply anything (no GitSHA)
//   - git is not available
//   - git revert fails (e.g. merge conflicts)
//
// On success the new HEAD SHA + a new entry in decisions.ndjson are
// produced so the audit chain remains complete.
func RevertDecision(id string) (*RevertDecisionResult, error) {
	if id == "" {
		return nil, fmt.Errorf("empty decision id")
	}
	target, ok := findDecision(id)
	if !ok {
		return nil, fmt.Errorf("decision %s not found", id)
	}
	if target.GitSHA == "" {
		return nil, fmt.Errorf("decision %s has no git_sha — nothing to revert (no policy mutation)", id)
	}

	newSHA, err := GitRevertCommit(target.GitSHA)
	if err != nil {
		return nil, fmt.Errorf("git revert: %w", err)
	}

	revertDec := AutonomyDecision{
		ID:               "dec-" + uuid.NewString(),
		Timestamp:        time.Now().UTC(),
		Trigger:          "revert",
		EventsConsidered: 0,
		Actions: []AutonomyDecisionAction{
			{
				Kind:      "revert",
				Rationale: fmt.Sprintf("manual rollback of decision %s (git %s → %s)", target.ID, short(target.GitSHA), short(newSHA)),
				Applied:   true,
			},
		},
		PolicyHashBefore: target.PolicyHashAfter,
		// PolicyHashAfter is best-effort — the in-memory pipeline updates
		// asynchronously via fsnotify, so we leave it blank and let the
		// next interval tick observe the new hash.
		GitSHA: newSHA,
	}
	appendDecision(revertDec)

	return &RevertDecisionResult{
		RevertedDecisionID: target.ID,
		OriginalGitSHA:     target.GitSHA,
		NewGitSHA:          newSHA,
		NewDecisionID:      revertDec.ID,
	}, nil
}
