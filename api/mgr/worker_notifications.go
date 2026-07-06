// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"log"
	"strings"
)

func listActiveMasterPanesForWorker(workerPaneID string) ([]string, string, error) {
	shortPane := shortPaneID(workerPaneID)
	if shortPane == "" {
		return nil, "", nil
	}
	rows, err := store.Query(`SELECT pa.pane_id FROM pane_agents pa WHERE pa.agent_name=? AND pa.status='active'`, shortPane)
	if err != nil {
		return nil, shortPane, err
	}
	defer rows.Close()
	var masters []string
	for rows.Next() {
		var masterPane string
		if err := rows.Scan(&masterPane); err != nil && err != sql.ErrNoRows {
			continue
		}
		masters = append(masters, masterPane)
	}
	return masters, shortPane, nil
}

func notifyActiveMastersForWorker(workerPaneID string, evt ChatEvent, logLabel string) {
	masters, shortPane, err := listActiveMasterPanesForWorker(workerPaneID)
	if err != nil {
		log.Printf("[hook] list active masters failed for worker=%s: %v", shortPaneID(workerPaneID), err)
		return
	}
	if shortPane == "" {
		return
	}
	for _, masterPane := range masters {
		hub.broadcast(masterPane, evt)
		if logLabel != "" {
			log.Printf("[hook] notified master %s (chatbus): %s", masterPane, logLabel)
		}
	}
}

func notifyWorkerReplyFinished(workerPaneID string, replyStatus string) {
	shortPane := shortPaneID(workerPaneID)
	if shortPane == "" {
		return
	}
	status := aiGatewayFirstNonEmpty(replyStatus, "completed")
	masters, _, err := listActiveMasterPanesForWorker(workerPaneID)
	if err != nil {
		log.Printf("[hook] worker reply finished lookup failed worker=%s status=%s err=%v", shortPane, status, err)
		return
	}
	log.Printf("[hook] worker reply finished trigger worker=%s status=%s active_masters=%s", shortPane, status, strings.Join(masters, ","))
	log.Printf("[hook] worker reply finished push suppressed worker=%s status=%s", shortPane, status)
}

func notifyActiveMastersByTmuxSend(workerPaneID string, text string) {
	masters, shortPane, err := listActiveMasterPanesForWorker(workerPaneID)
	if err != nil {
		log.Printf("[hook] list active masters failed for tmux-send worker=%s: %v", shortPaneID(workerPaneID), err)
		return
	}
	if shortPane == "" {
		return
	}
	log.Printf("[hook] tmux-send trigger worker=%s active_masters=%s text=%q", shortPane, strings.Join(masters, ","), text)
	for _, masterPane := range masters {
		if err := sendTextToPane(masterPane, text, true); err != nil {
			log.Printf("[hook] tmux-send master %s failed for worker %s: %v", masterPane, shortPane, err)
			continue
		}
		log.Printf("[hook] tmux-sent master %s: %s", masterPane, text)
	}
}
