// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Keystore — named secrets (key / value / note) kept in
// ~/cicy-ai/db/keystore.json (0600, atomic writes).
//
// Contract:
//   - Values are written only from the UI (the cicy-keystore skill has no
//     "set"), and the list endpoint NEVER returns a value — only whether one is
//     set. Agents discover keys + notes, never contents.
//   - /api/keystore/resolve hands values to the cicy-keystore skill's `exec`,
//     which injects them into a child process's environment and masks them in
//     that child's output. It is not exposed in any UI.
//
// Routes (registered in main.go, all behind authM):
//
//	GET    /api/keystore                 → {keys:[{key,note,has_value,updated_at}]}
//	POST   /api/keystore                 {key, old_key?, value?, note} create / update / rename
//	DELETE /api/keystore?key=K
//	POST   /api/keystore/resolve         {keys:[K…]} → {values:{K:V}}

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const keystoreFile = "keystore.json"

// Keys double as environment variable names for `cicy-keystore exec`.
var keystoreKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

const keystoreMaxValueBytes = 64 * 1024

var keystoreMu sync.Mutex

type keystoreEntry struct {
	Value     string `json:"value"`
	Note      string `json:"note"`
	UpdatedAt string `json:"updated_at"`
}

type keystoreItem struct {
	Key       string `json:"key"`
	Note      string `json:"note"`
	HasValue  bool   `json:"has_value"`
	UpdatedAt string `json:"updated_at"`
}

func readKeystore() (map[string]keystoreEntry, error) {
	return readAccountStore[keystoreEntry](keystoreFile)
}

func writeKeystore(entries map[string]keystoreEntry) error {
	return writeAccountStore(keystoreFile, entries)
}

// keystoreList is the only shape of the store that leaves the server for
// listing: no values, ever.
func keystoreList(entries map[string]keystoreEntry) []keystoreItem {
	items := make([]keystoreItem, 0, len(entries))
	for key, e := range entries {
		items = append(items, keystoreItem{Key: key, Note: e.Note, HasValue: e.Value != "", UpdatedAt: e.UpdatedAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items
}

func handleKeystore(w http.ResponseWriter, r *http.Request) {
	keystoreMu.Lock()
	defer keystoreMu.Unlock()
	entries, err := readKeystore()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		J(w, M{"keys": keystoreList(entries)})
	case http.MethodPost, http.MethodPut:
		var req struct {
			Key    string  `json:"key"`
			OldKey string  `json:"old_key"`
			Value  *string `json:"value"` // nil / omitted = keep the stored value
			Note   string  `json:"note"`
		}
		readBody(r, &req)
		key := strings.TrimSpace(req.Key)
		oldKey := strings.TrimSpace(req.OldKey)
		if !keystoreKeyRE.MatchString(key) {
			httpErr(w, http.StatusBadRequest, "key must match [A-Za-z_][A-Za-z0-9_]* (max 64)")
			return
		}
		entry, existed := entries[key]
		if oldKey != "" && oldKey != key {
			prev, ok := entries[oldKey]
			if !ok {
				httpErr(w, http.StatusNotFound, "key not found: "+oldKey)
				return
			}
			if existed {
				httpErr(w, http.StatusConflict, "key already exists: "+key)
				return
			}
			entry, existed = prev, true
			delete(entries, oldKey)
		}
		if req.Value != nil && *req.Value != "" {
			if len(*req.Value) > keystoreMaxValueBytes {
				httpErr(w, http.StatusBadRequest, "value too large")
				return
			}
			entry.Value = *req.Value
		}
		if !existed && entry.Value == "" {
			httpErr(w, http.StatusBadRequest, "value is required for a new key")
			return
		}
		entry.Note = strings.TrimSpace(req.Note)
		entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		entries[key] = entry
		if err := writeKeystore(entries); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "keys": keystoreList(entries)})
	case http.MethodDelete:
		key := strings.TrimSpace(r.URL.Query().Get("key"))
		if _, ok := entries[key]; !ok {
			httpErr(w, http.StatusNotFound, "key not found")
			return
		}
		delete(entries, key)
		if err := writeKeystore(entries); err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		J(w, M{"success": true, "keys": keystoreList(entries)})
	default:
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleKeystoreResolve returns the values of the requested keys for the
// cicy-keystore skill's `exec` (env injection + output masking). Unknown or
// empty keys fail the whole request so a typo never runs a command with a
// silently missing secret.
func handleKeystoreResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Keys []string `json:"keys"`
	}
	readBody(r, &req)
	if len(req.Keys) == 0 {
		httpErr(w, http.StatusBadRequest, "keys required")
		return
	}
	keystoreMu.Lock()
	entries, err := readKeystore()
	keystoreMu.Unlock()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	values := map[string]string{}
	var missing []string
	for _, k := range req.Keys {
		k = strings.TrimSpace(k)
		if e, ok := entries[k]; ok && e.Value != "" {
			values[k] = e.Value
		} else {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		httpErr(w, http.StatusNotFound, "key not set: "+strings.Join(missing, ", "))
		return
	}
	J(w, M{"values": values})
}
