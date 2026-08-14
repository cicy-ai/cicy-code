// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
)

func handleGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		rows, err := store.Query("SELECT id, name, description, created_at, updated_at, COALESCE(is_default, 0), COALESCE(is_pinned, 0), COALESCE(name_customized, 0) FROM agent_groups ORDER BY is_pinned DESC, is_default DESC, id")
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		var groups []M
		for rows.Next() {
			var id int
			var name, desc string
			var createdAt, updatedAt sql.NullString
			var isDefault, isPinned, nameCustomized int
			if err := rows.Scan(&id, &name, &desc, &createdAt, &updatedAt, &isDefault, &isPinned, &nameCustomized); err != nil {
				httpErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			g := M{"id": id, "name": name, "description": desc, "is_default": isDefault == 1, "is_pinned": isPinned == 1, "name_customized": nameCustomized == 1}
			if createdAt.Valid {
				g["created_at"] = createdAt.String
			}
			if updatedAt.Valid {
				g["updated_at"] = updatedAt.String
			}
			// Get pane_ids
			wrows, _ := store.Query("SELECT win_id FROM group_windows WHERE group_id=?", id)
			var pids []string
			if wrows != nil {
				for wrows.Next() {
					var wid string
					if err := wrows.Scan(&wid); err != nil {
						wrows.Close()
						httpErr(w, http.StatusInternalServerError, err.Error())
						return
					}
					pids = append(pids, wid)
				}
				wrows.Close()
			}
			if pids == nil {
				pids = []string{}
			}
			g["pane_ids"] = pids
			g["pane_count"] = len(pids)
			groups = append(groups, g)
		}
		if groups == nil {
			groups = []M{}
		}
		J(w, M{"groups": groups})
	case "POST":
		var req M
		readBody(r, &req)
		name, _ := req["name"].(string)
		desc, _ := req["description"].(string)
		res, err := store.Exec("INSERT INTO agent_groups (name, description) VALUES (?,?)", name, desc)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		J(w, M{"id": id, "name": name, "description": desc, "is_default": false, "is_pinned": false, "name_customized": true, "pane_ids": []string{}, "pane_count": 0})
	}
}

func handleGroupByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/groups/")
	parts := strings.SplitN(path, "/", 2)
	groupID := parts[0]

	// Sub-routes
	if len(parts) > 1 {
		sub := parts[1]
		switch {
		case strings.HasPrefix(sub, "windows"):
			handleGroupWindows(w, r, groupID, strings.TrimPrefix(sub, "windows"))
		case strings.HasPrefix(sub, "panes/"):
			handleGroupPanesCompat(w, r, groupID, strings.TrimPrefix(sub, "panes/"))
		case sub == "layout":
			handleGroupBatchLayout(w, r, groupID)
		default:
			httpErr(w, 404, "not found")
		}
		return
	}

	switch r.Method {
	case "GET":
		var id int
		var name, desc string
		var createdAt, updatedAt sql.NullString
		var isDefault, isPinned, nameCustomized int
		err := store.QueryRow("SELECT id, name, description, created_at, updated_at, COALESCE(is_default, 0), COALESCE(is_pinned, 0), COALESCE(name_customized, 0) FROM agent_groups WHERE id=?", groupID).Scan(&id, &name, &desc, &createdAt, &updatedAt, &isDefault, &isPinned, &nameCustomized)
		if err == sql.ErrNoRows {
			httpErr(w, 404, "Group not found")
			return
		}
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		g := M{"id": id, "name": name, "description": desc, "is_default": isDefault == 1, "is_pinned": isPinned == 1, "name_customized": nameCustomized == 1}
		if createdAt.Valid {
			g["created_at"] = createdAt.String
		}
		if updatedAt.Valid {
			g["updated_at"] = updatedAt.String
		}
		// Windows
		rows, _ := store.Query("SELECT id, win_id, win_type, ref_id, pos_x, pos_y, width, height, z_index FROM group_windows WHERE group_id=? ORDER BY z_index", groupID)
		var windows, panes []M
		if rows != nil {
			for rows.Next() {
				var id int
				var winID, winType, refID string
				var posX, posY, width, height float64
				var zIndex int
				if err := rows.Scan(&id, &winID, &winType, &refID, &posX, &posY, &width, &height, &zIndex); err != nil {
					rows.Close()
					httpErr(w, http.StatusInternalServerError, err.Error())
					return
				}
				wm := M{"id": id, "win_id": winID, "win_type": winType, "ref_id": refID, "pos_x": posX, "pos_y": posY, "width": width, "height": height, "z_index": zIndex}
				windows = append(windows, wm)
				if winType == "agent_ttyd" {
					panes = append(panes, M{"id": id, "pane_id": winID, "pos_x": posX, "pos_y": posY, "width": width, "height": height, "z_index": zIndex})
				}
			}
			rows.Close()
		}
		if windows == nil {
			windows = []M{}
		}
		if panes == nil {
			panes = []M{}
		}
		g["windows"] = windows
		g["panes"] = panes
		g["apps"] = []M{}
		J(w, g)
	case "PATCH":
		var req M
		readBody(r, &req)
		var sets []string
		var vals []interface{}
		if n, ok := req["name"].(string); ok {
			sets = append(sets, "name=?", "name_customized=1")
			vals = append(vals, n)
		}
		if d, ok := req["description"].(string); ok {
			sets = append(sets, "description=?")
			vals = append(vals, d)
		}
		if pinned, ok := req["is_pinned"].(bool); ok {
			sets = append(sets, "is_pinned=?")
			if pinned {
				vals = append(vals, 1)
			} else {
				vals = append(vals, 0)
			}
		}
		if len(sets) == 0 {
			httpErr(w, 400, "No fields to update")
			return
		}
		sets = append(sets, "updated_at=datetime('now')")
		vals = append(vals, groupID)
		res, err := store.Exec("UPDATE agent_groups SET "+strings.Join(sets, ", ")+" WHERE id=?", vals...)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			httpErr(w, http.StatusNotFound, "Group not found")
			return
		}
		J(w, M{"success": true, "group_id": groupID, "updated": req})
	case "DELETE":
		var isDefault int
		if err := store.QueryRow("SELECT COALESCE(is_default, 0) FROM agent_groups WHERE id=?", groupID).Scan(&isDefault); err == sql.ErrNoRows {
			httpErr(w, http.StatusNotFound, "Group not found")
			return
		} else if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if isDefault == 1 {
			httpErr(w, http.StatusBadRequest, "Default project cannot be deleted")
			return
		}
		res, err := store.Exec("DELETE FROM agent_groups WHERE id=?", groupID)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			httpErr(w, 404, "Group not found")
			return
		}
		J(w, M{"success": true, "group_id": groupID})
	}
}

func handleGroupWindows(w http.ResponseWriter, r *http.Request, groupID, sub string) {
	switch r.Method {
	case "POST":
		var req M
		readBody(r, &req)
		winID, _ := req["win_id"].(string)
		winType, _ := req["win_type"].(string)
		if winType == "" {
			winType = "agent_ttyd"
		}
		refID, _ := req["ref_id"].(string)
		if refID == "" {
			refID = winID
		}
		addWindowToGroup(w, groupID, winID, winType, refID, "win_id")
	case "DELETE":
		winID := strings.TrimPrefix(sub, "/")
		store.Exec("DELETE FROM group_windows WHERE group_id=? AND win_id=?", groupID, winID)
		J(w, M{"success": true, "group_id": groupID, "win_id": winID})
	case "PATCH":
		// Layout update for specific window
		winID := strings.TrimSuffix(strings.TrimPrefix(sub, "/"), "/layout")
		var req M
		readBody(r, &req)
		var sets []string
		var vals []interface{}
		for _, k := range []string{"pos_x", "pos_y", "width", "height", "z_index"} {
			if v, ok := req[k]; ok {
				sets = append(sets, k+"=?")
				vals = append(vals, v)
			}
		}
		if len(sets) > 0 {
			vals = append(vals, groupID, winID)
			store.Exec("UPDATE group_windows SET "+strings.Join(sets, ", ")+" WHERE group_id=? AND win_id=?", vals...)
		}
		J(w, M{"success": true, "group_id": groupID, "win_id": winID})
	}
}

func handleGroupPanesCompat(w http.ResponseWriter, r *http.Request, groupID, sub string) {
	// Remove /layout suffix if present
	paneID := strings.TrimSuffix(sub, "/layout")
	switch r.Method {
	case "POST":
		addWindowToGroup(w, groupID, paneID, "agent_ttyd", paneID, "pane_id")
	case "DELETE":
		store.Exec("DELETE FROM group_windows WHERE group_id=? AND win_id=?", groupID, paneID)
		J(w, M{"success": true, "group_id": groupID, "pane_id": paneID})
	case "PATCH":
		var req M
		readBody(r, &req)
		var sets []string
		var vals []interface{}
		for _, k := range []string{"pos_x", "pos_y", "width", "height", "z_index"} {
			if v, ok := req[k]; ok {
				sets = append(sets, k+"=?")
				vals = append(vals, v)
			}
		}
		if len(sets) > 0 {
			vals = append(vals, groupID, paneID)
			store.Exec("UPDATE group_windows SET "+strings.Join(sets, ", ")+" WHERE group_id=? AND win_id=?", vals...)
		}
		J(w, M{"success": true, "group_id": groupID, "pane_id": paneID})
	}
}

func addWindowToGroup(w http.ResponseWriter, groupID, winID, winType, refID, responseKey string) {
	winID = strings.TrimSpace(winID)
	if winID == "" {
		httpErr(w, http.StatusBadRequest, "Agent id is required")
		return
	}
	var existingGroupID int64
	err := store.QueryRow("SELECT group_id FROM group_windows WHERE win_id=? LIMIT 1", winID).Scan(&existingGroupID)
	if err == nil {
		if groupID == fmt.Sprint(existingGroupID) {
			J(w, M{"success": true, "already_added": true, "group_id": groupID, responseKey: winID})
			return
		}
		httpErr(w, http.StatusConflict, "Agent already belongs to another project")
		return
	}
	if err != sql.ErrNoRows {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := store.Exec("INSERT INTO group_windows (group_id, win_id, win_type, ref_id) VALUES (?,?,?,?)", groupID, winID, winType, refID); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"success": true, "group_id": groupID, responseKey: winID})
}

func handleGroupBatchLayout(w http.ResponseWriter, r *http.Request, groupID string) {
	var req struct {
		Panes []struct {
			WinID  string  `json:"win_id"`
			PosX   float64 `json:"pos_x"`
			PosY   float64 `json:"pos_y"`
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
			ZIndex int     `json:"z_index"`
		} `json:"panes"`
	}
	readBody(r, &req)
	for _, p := range req.Panes {
		store.Exec("UPDATE group_windows SET pos_x=?, pos_y=?, width=?, height=?, z_index=? WHERE group_id=? AND win_id=?",
			p.PosX, p.PosY, p.Width, p.Height, p.ZIndex, groupID, p.WinID)
	}
	J(w, M{"success": true, "group_id": groupID, "updated": len(req.Panes)})
}
