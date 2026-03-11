package main

import (
	"database/sql"
	"net/http"
	"strings"
)

func handleGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		rows, err := db.Query("SELECT id, name, description, created_at, updated_at FROM agent_groups ORDER BY id")
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		var groups []M
		for rows.Next() {
			var id int
			var name, desc string
			var createdAt, updatedAt sql.NullTime
			rows.Scan(&id, &name, &desc, &createdAt, &updatedAt)
			g := M{"id": id, "name": name, "description": desc}
			if createdAt.Valid {
				g["created_at"] = createdAt.Time.Format("2006-01-02T15:04:05")
			}
			if updatedAt.Valid {
				g["updated_at"] = updatedAt.Time.Format("2006-01-02T15:04:05")
			}
			// Get pane_ids
			wrows, _ := db.Query("SELECT win_id FROM group_windows WHERE group_id=?", id)
			var pids []string
			if wrows != nil {
				for wrows.Next() {
					var wid string
					wrows.Scan(&wid)
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
		res, err := db.Exec("INSERT INTO agent_groups (name, description) VALUES (?,?)", name, desc)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		J(w, M{"id": id, "name": name, "description": desc, "pane_ids": []string{}, "pane_count": 0})
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
		var name, desc string
		var createdAt, updatedAt sql.NullTime
		err := db.QueryRow("SELECT name, description, created_at, updated_at FROM agent_groups WHERE id=?", groupID).Scan(&name, &desc, &createdAt, &updatedAt)
		if err != nil {
			httpErr(w, 404, "Group not found")
			return
		}
		g := M{"id": groupID, "name": name, "description": desc}
		if createdAt.Valid {
			g["created_at"] = createdAt.Time.Format("2006-01-02T15:04:05")
		}
		if updatedAt.Valid {
			g["updated_at"] = updatedAt.Time.Format("2006-01-02T15:04:05")
		}
		// Windows
		rows, _ := db.Query("SELECT id, win_id, win_type, ref_id, pos_x, pos_y, width, height, z_index FROM group_windows WHERE group_id=? ORDER BY z_index", groupID)
		var windows, panes []M
		if rows != nil {
			for rows.Next() {
				var id int
				var winID, winType, refID string
				var posX, posY, width, height float64
				var zIndex int
				rows.Scan(&id, &winID, &winType, &refID, &posX, &posY, &width, &height, &zIndex)
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
			sets = append(sets, "name=?")
			vals = append(vals, n)
		}
		if d, ok := req["description"].(string); ok {
			sets = append(sets, "description=?")
			vals = append(vals, d)
		}
		if len(sets) == 0 {
			httpErr(w, 400, "No fields to update")
			return
		}
		vals = append(vals, groupID)
		db.Exec("UPDATE agent_groups SET "+strings.Join(sets, ", ")+" WHERE id=?", vals...)
		J(w, M{"success": true, "group_id": groupID, "updated": req})
	case "DELETE":
		res, _ := db.Exec("DELETE FROM agent_groups WHERE id=?", groupID)
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
		db.Exec("INSERT IGNORE INTO group_windows (group_id, win_id, win_type, ref_id) VALUES (?,?,?,?)", groupID, winID, winType, refID)
		J(w, M{"success": true, "group_id": groupID, "win_id": winID})
	case "DELETE":
		winID := strings.TrimPrefix(sub, "/")
		db.Exec("DELETE FROM group_windows WHERE group_id=? AND win_id=?", groupID, winID)
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
			db.Exec("UPDATE group_windows SET "+strings.Join(sets, ", ")+" WHERE group_id=? AND win_id=?", vals...)
		}
		J(w, M{"success": true, "group_id": groupID, "win_id": winID})
	}
}

func handleGroupPanesCompat(w http.ResponseWriter, r *http.Request, groupID, sub string) {
	// Remove /layout suffix if present
	paneID := strings.TrimSuffix(sub, "/layout")
	switch r.Method {
	case "POST":
		db.Exec("INSERT IGNORE INTO group_windows (group_id, win_id, win_type, ref_id) VALUES (?,?,'agent_ttyd',?)", groupID, paneID, paneID)
		J(w, M{"success": true, "group_id": groupID, "pane_id": paneID})
	case "DELETE":
		db.Exec("DELETE FROM group_windows WHERE group_id=? AND win_id=?", groupID, paneID)
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
			db.Exec("UPDATE group_windows SET "+strings.Join(sets, ", ")+" WHERE group_id=? AND win_id=?", vals...)
		}
		J(w, M{"success": true, "group_id": groupID, "pane_id": paneID})
	}
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
		db.Exec("UPDATE group_windows SET pos_x=?, pos_y=?, width=?, height=?, z_index=? WHERE group_id=? AND win_id=?",
			p.PosX, p.PosY, p.Width, p.Height, p.ZIndex, groupID, p.WinID)
	}
	J(w, M{"success": true, "group_id": groupID, "updated": len(req.Panes)})
}
