// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Cloud delivery and terminal rendering are deliberately separate. A Cloud
// message is ACKed after it is durable in cicy_cloud_inbox. Dispatch observes
// structured turn markers only; it must never capture or parse terminal text.
var cicyCloudInboxDispatchMu sync.Mutex

type cicyCloudInboxItem struct {
	MessageID    string
	AccountID    int64
	TargetPaneID string
	Text         string
	PeerChatID   string
	ContextToken string
	Status       string
	AttemptCount int
}

func isCiCyCloudTransport(tr botTransport) bool {
	_, ok := tr.(*cicyCloudTransport)
	return ok
}

func persistCiCyCloudInbound(accID int64, msg botMsg, paneID, text string) error {
	if store == nil {
		return fmt.Errorf("database unavailable")
	}
	messageID := strings.TrimSpace(msg.AckID)
	if messageID == "" {
		return fmt.Errorf("cloud message id required")
	}
	senderInstance, senderAgent := splitCiCyCloudPeer(msg.Peer.ChatID)
	_, err := store.Exec(`INSERT OR IGNORE INTO cicy_cloud_inbox
		(message_id,account_id,target_pane_id,sender_instance_id,sender_agent_id,text,peer_chat_id,context_token,status)
		VALUES(?,?,?,?,?,?,?,?, 'received')`, messageID, accID, normPaneID(paneID), senderInstance,
		senderAgent, text, msg.Peer.ChatID, msg.Peer.ContextToken)
	return err
}

func updateCiCyCloudInbox(messageID, status, lastError string, completed bool) {
	if store == nil || strings.TrimSpace(messageID) == "" {
		return
	}
	completedAt := ""
	if completed {
		completedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if _, err := store.Exec(`UPDATE cicy_cloud_inbox SET status=?,last_error=?,updated_at=datetime('now'),
		completed_at=CASE WHEN ?<>'' THEN ? ELSE completed_at END WHERE message_id=?`,
		status, lastError, completedAt, completedAt, messageID); err != nil {
		log.Printf("[im-cloud-inbox] update failed id=%s status=%s: %v", messageID, status, err)
	}
}

func loadPendingCiCyCloudInbox(accID int64) []cicyCloudInboxItem {
	if store == nil {
		return nil
	}
	rows, err := store.Query(`SELECT message_id,account_id,target_pane_id,text,peer_chat_id,context_token,status,attempt_count
		FROM cicy_cloud_inbox WHERE account_id=? AND status='received' ORDER BY created_at LIMIT 50`, accID)
	if err != nil {
		log.Printf("[im-cloud-inbox] list failed account=%d: %v", accID, err)
		return nil
	}
	defer rows.Close()
	var out []cicyCloudInboxItem
	for rows.Next() {
		var item cicyCloudInboxItem
		if err := rows.Scan(&item.MessageID, &item.AccountID, &item.TargetPaneID, &item.Text,
			&item.PeerChatID, &item.ContextToken, &item.Status, &item.AttemptCount); err == nil {
			out = append(out, item)
		}
	}
	return out
}

func restoreCiCyCloudReplyPushes(accID int64) {
	if store == nil {
		return
	}
	rows, err := store.Query(`SELECT target_pane_id,peer_chat_id,context_token FROM cicy_cloud_inbox
		WHERE account_id=? AND status IN ('dispatching','running','uncertain') ORDER BY created_at`, accID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var pane, chat, context string
		if rows.Scan(&pane, &chat, &context) == nil {
			imRegisterReplyPushForInbound(pane, accID, botPeer{ChatID: chat, ContextToken: context})
		}
	}
}

func dispatchPendingCiCyCloudInbox(accID int64) {
	cicyCloudInboxDispatchMu.Lock()
	defer cicyCloudInboxDispatchMu.Unlock()
	for _, item := range loadPendingCiCyCloudInbox(accID) {
		dispatchCiCyCloudInboxItem(item)
	}
}

func dispatchCiCyCloudInboxItem(item cicyCloudInboxItem) {
	pane := normPaneID(item.TargetPaneID)
	var active int
	if err := store.QueryRow(`SELECT COUNT(*) FROM cicy_cloud_inbox WHERE target_pane_id=?
		AND message_id<>? AND status IN ('dispatching','running','uncertain')`, pane, item.MessageID).Scan(&active); err != nil || active > 0 {
		return // one structured turn at a time per Agent; preserves reply_to ordering
	}
	peer := botPeer{ChatID: item.PeerChatID, ContextToken: item.ContextToken}
	imRegisterReplyPushForInbound(pane, item.AccountID, peer)

	if paneAgentType(pane) == "cicy" {
		ws := paneWorkspace(shortPaneID(pane))
		if ws == "" {
			updateCiCyCloudInbox(item.MessageID, "received", "agent workspace not found", false)
			return
		}
		if _, err := store.Exec(`UPDATE cicy_cloud_inbox SET status='running',attempt_count=attempt_count+1,
			last_error='',updated_at=datetime('now') WHERE message_id=? AND status='received'`, item.MessageID); err != nil {
			return
		}
		go deliverCicyMessage(shortPaneID(pane), ws, item.Text)
		return
	}

	if !imPaneSessionOnline(pane) {
		return // durable received row will be retried after the Agent is online
	}
	if _, err := store.Exec(`UPDATE cicy_cloud_inbox SET status='dispatching',attempt_count=attempt_count+1,
		last_error='',updated_at=datetime('now') WHERE message_id=? AND status='received'`, item.MessageID); err != nil {
		return
	}

	// Terminal agents use the same serialized, echo-confirmed submission path as
	// cicy-agent msg. Headless cicy agents remain on deliverCicyMessage above and
	// never pass through tmux or synthetic Enter keys.
	if err := sendTextToPane(pane, item.Text, true); err != nil {
		if sendErr, ok := err.(*tmuxSendError); ok && !sendErr.PaneUpdated {
			updateCiCyCloudInbox(item.MessageID, "received", sendErr.Message, false)
		} else {
			updateCiCyCloudInbox(item.MessageID, "uncertain", err.Error(), false)
		}
		return
	}
	updateCiCyCloudInbox(item.MessageID, "running", "", false)
}

func markCiCyCloudInboxReplied(messageID string) {
	if strings.TrimSpace(messageID) == "" || store == nil {
		return
	}
	var status string
	var accountID int64
	if err := store.QueryRow(`SELECT status,account_id FROM cicy_cloud_inbox WHERE message_id=?`, messageID).Scan(&status, &accountID); err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[im-cloud-inbox] reply lookup failed id=%s: %v", messageID, err)
		}
		return
	}
	updateCiCyCloudInbox(messageID, "replied", "", true)
	go dispatchPendingCiCyCloudInbox(accountID)
}
