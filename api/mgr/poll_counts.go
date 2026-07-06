// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"sync"
	"time"

	"ttyd-go/mgr/audit"
)

// The two GLOBAL dashboard badge counts — pending team-knowledge reviews and open
// audit alerts — used to be polled by the web on their own HTTP timers
// (/api/knowledge every 30s, /api/audit/events every 15s) even when those panels
// were closed. They now ride the existing 5s WS poll_data push instead (see
// buildPollData), so the browser makes zero extra requests for them.
//
// Folding a 30s/15s computation into a 5s poll must NOT make it run 6× more often
// server-side: buildPollData can fire on every poll_request AND on every
// broadcast. So the counts are cached and recomputed at most once per TTL,
// regardless of how often poll_data is built.
type dashboardCountsCache struct {
	mu               sync.Mutex
	computedAt       time.Time
	knowledgePending int
	auditOpenIDs     []string
}

const dashboardCountsTTL = 12 * time.Second

var dashboardCounts dashboardCountsCache

// get returns (pendingKnowledgeCount, openAuditAlertIDs), recomputing only when
// the cache is older than the TTL.
func (d *dashboardCountsCache) get() (int, []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.computedAt.IsZero() || time.Since(d.computedAt) > dashboardCountsTTL {
		d.knowledgePending = computeKnowledgePending()
		d.auditOpenIDs = computeAuditOpenAlertIDs()
		d.computedAt = time.Now()
	}
	return d.knowledgePending, d.auditOpenIDs
}

func computeKnowledgePending() int {
	rows, err := listKnowledge(knowledgeFilter{Status: knowledgeStatusPending})
	if err != nil {
		return 0
	}
	return len(rows)
}

// computeAuditOpenAlertIDs returns the IDs of "open" audit alerts: events whose
// decision was APPLIED with an alerting action (block/notify/redact), minus any
// that already carry a server-side ack (a meta_alert_ack event referencing them).
// The web subtracts its own local-dismiss set (localStorage) to get the final
// badge count — so we ship IDs, not just a number, to preserve that local filter.
func computeAuditOpenAlertIDs() []string {
	res, err := audit.Query(audit.QueryOpts{Limit: 200})
	if err != nil || res == nil {
		return nil
	}
	acked := make(map[string]bool, len(res.Events))
	for _, e := range res.Events {
		if e.Meta.Category == "meta_alert_ack" && e.Meta.AckEventID != "" {
			acked[e.Meta.AckEventID] = true
		}
	}
	out := make([]string, 0, 8)
	for _, e := range res.Events {
		if !e.Decision.Applied {
			continue
		}
		switch string(e.Decision.Action) {
		case "block", "notify", "redact":
		default:
			continue
		}
		if acked[e.ID] {
			continue
		}
		out = append(out, e.ID)
	}
	return out
}
