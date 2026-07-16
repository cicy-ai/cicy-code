// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// current.json holds the agent's live conversation snapshot — the whole outbound
// request body — so for a long conversation it is MEGABYTES (7.4 MB observed).
//
// The web UI polls /api/agents/current-reply/<pane> every 500 ms per open chat,
// and that handler used to os.ReadFile + json.Unmarshal the file TWICE per call
// (once directly, once inside agentHistoryCurrentMaxID) just to produce a ~15 KB
// response. Measured on a 7.4 MB current.json: 1.23 s per call — LONGER than the
// 500 ms poll interval, so the polls ran back to back, forever. In a 24-hour heap
// profile this one handler accounted for 79.7% of ALL allocation in the process
// (2.4 TB of 3.0 TB), and the GC pressure from that churn was the daemon's
// steady-state CPU burn.
//
// current.json only changes when a turn is committed. Keying the parsed snapshot
// on (mtime, size) turns an idle agent's poll into a single stat(2).
//
// READ-ONLY CONTRACT: the returned snapshot shares its Body/Headers/Prompts with
// the cached copy. Mutating it corrupts the cache and races other readers. Only
// the read-only poll path uses this; every other call site keeps going through
// aiGatewayReadCurrentSnapshot / agentInspectorLoadCurrent and gets a fresh parse.

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

type currentSnapshotCacheEntry struct {
	modTime time.Time
	size    int64
	usedAt  time.Time
	snap    *aiGatewayCurrentSnapshot
}

const (
	// Bounded so the cache can never become the leak it was written to prevent:
	// each entry pins one full parsed snapshot (megabytes).
	currentSnapshotCacheMax     = 32
	currentSnapshotCacheTTL     = 10 * time.Minute
	currentSnapshotCacheSweepAt = 16
)

var currentSnapshotCache = struct {
	mu    sync.Mutex
	items map[string]*currentSnapshotCacheEntry
}{items: map[string]*currentSnapshotCacheEntry{}}

func currentSnapshotPath(agentID string) string {
	return filepath.Join(aiGatewayHistoryDir(agentID), "current.json")
}

// aiGatewayReadCurrentSnapshotCached returns the parsed current.json for agentID,
// reusing the previous parse while the file's (mtime, size) is unchanged.
// The result is READ-ONLY — see the file header.
func aiGatewayReadCurrentSnapshotCached(agentID string) (aiGatewayCurrentSnapshot, error) {
	path := currentSnapshotPath(agentID)
	st, err := os.Stat(path)
	if err != nil {
		dropCurrentSnapshotCache(agentID)
		return aiGatewayCurrentSnapshot{}, err
	}
	now := time.Now()

	currentSnapshotCache.mu.Lock()
	if e := currentSnapshotCache.items[agentID]; e != nil && e.size == st.Size() && e.modTime.Equal(st.ModTime()) {
		e.usedAt = now
		snap := *e.snap
		currentSnapshotCache.mu.Unlock()
		return snap, nil
	}
	currentSnapshotCache.mu.Unlock()

	snap, err := aiGatewayReadCurrentSnapshot(agentID)
	if err != nil {
		return aiGatewayCurrentSnapshot{}, err
	}
	// The writer is atomic (write tmp + rename), so we never read a torn file —
	// but a rename CAN land between our stat and our read, which would stamp a
	// fresh parse with a stale (mtime, size) and pin the wrong snapshot forever.
	// Re-stat and only cache when the file is provably the one we stat'd.
	if st2, err2 := os.Stat(path); err2 != nil || st2.Size() != st.Size() || !st2.ModTime().Equal(st.ModTime()) {
		return snap, nil
	}
	stored := snap

	currentSnapshotCache.mu.Lock()
	currentSnapshotCacheEvictLocked(now)
	currentSnapshotCache.items[agentID] = &currentSnapshotCacheEntry{
		modTime: st.ModTime(),
		size:    st.Size(),
		usedAt:  now,
		snap:    &stored,
	}
	currentSnapshotCache.mu.Unlock()
	return snap, nil
}

// currentSnapshotCacheEvictLocked drops stale entries, then the least-recently
// used ones, so the cache stays bounded. Caller holds the lock.
func currentSnapshotCacheEvictLocked(now time.Time) {
	if len(currentSnapshotCache.items) < currentSnapshotCacheSweepAt {
		return
	}
	for id, e := range currentSnapshotCache.items {
		if now.Sub(e.usedAt) > currentSnapshotCacheTTL {
			delete(currentSnapshotCache.items, id)
		}
	}
	for len(currentSnapshotCache.items) >= currentSnapshotCacheMax {
		oldestID := ""
		var oldest time.Time
		for id, e := range currentSnapshotCache.items {
			if oldestID == "" || e.usedAt.Before(oldest) {
				oldestID, oldest = id, e.usedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(currentSnapshotCache.items, oldestID)
	}
}

// dropCurrentSnapshotCache releases an agent's cached snapshot — called when the
// pane is deleted so a torn-down agent stops pinning megabytes.
func dropCurrentSnapshotCache(agentID string) {
	currentSnapshotCache.mu.Lock()
	delete(currentSnapshotCache.items, agentID)
	delete(currentSnapshotCache.items, shortPaneID(agentID))
	currentSnapshotCache.mu.Unlock()
}
