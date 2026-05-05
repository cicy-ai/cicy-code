package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

func verifyToken(token string) bool {
	if token == loadAPIToken() {
		return true
	}
	var expiresAt sql.NullTime
	err := store.QueryRow("SELECT expires_at FROM tokens WHERE token=?", token).Scan(&expiresAt)
	if err != nil {
		return false
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return false
	}
	return true
}

func getTokenPerms(token string) (perms []string, groupID *int) {
	if token == loadAPIToken() {
		return []string{"api_full", "ttyd_read", "ttyd_write", "prompt", "pane_manage", "app_manage", "agent_manage", "desktop_manage", "vnc_read", "vnc_manage", "voice_to_text"}, nil
	}
	var permsStr string
	var gid sql.NullInt64
	var expiresAt sql.NullTime
	err := store.QueryRow("SELECT perms, group_id, expires_at FROM tokens WHERE token=?", token).Scan(&permsStr, &gid, &expiresAt)
	if err != nil {
		return nil, nil
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return nil, nil
	}
	for _, p := range strings.Split(permsStr, ",") {
		if p = strings.TrimSpace(p); p != "" {
			perms = append(perms, p)
		}
	}
	if gid.Valid {
		g := int(gid.Int64)
		return perms, &g
	}
	return perms, nil
}

func handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	token := getToken(r)
	if token == "" {
		J(w, M{"valid": false})
		return
	}
	perms, groupID := getTokenPerms(token)
	if perms == nil {
		J(w, M{"valid": false})
		return
	}
	J(w, M{"valid": true, "auth_type": "token", "token": token[:minInt(8, len(token))] + "...", "perms": perms, "group_id": groupID})
}

func handleAuthVerifyToken(w http.ResponseWriter, r *http.Request) {
	var req M
	readBody(r, &req)
	token, _ := req["token"].(string)
	if token == "" {
		httpErr(w, 400, "token required")
		return
	}
	perms, groupID := getTokenPerms(token)
	if perms == nil {
		J(w, M{"valid": false})
		return
	}
	J(w, M{"valid": true, "perms": perms, "token": token, "group_id": groupID})
}

func handleAuthTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		rows, err := store.Query("SELECT id, " + store.TokenPrefix() + ", group_id, pane_id, perms, note, expires_at, created_at FROM tokens ORDER BY created_at DESC")
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		var tokens []M
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			row := M{}
			for i, c := range cols {
				if b, ok := vals[i].([]byte); ok {
					row[c] = string(b)
				} else {
					row[c] = vals[i]
				}
			}
			tokens = append(tokens, row)
		}
		if tokens == nil {
			tokens = []M{}
		}
		J(w, M{"tokens": tokens})
	case "POST":
		var req M
		readBody(r, &req)
		permsArr, _ := req["perms"].([]interface{})
		var ps []string
		for _, p := range permsArr {
			if s, ok := p.(string); ok {
				ps = append(ps, s)
			}
		}
		b := make([]byte, 32)
		rand.Read(b)
		token := hex.EncodeToString(b)
		permsStr := strings.Join(ps, ",")
		gid, _ := req["group_id"]
		paneID, _ := req["pane_id"]
		note, _ := req["note"].(string)
		expiresAt, _ := req["expires_at"].(string)
		var ea interface{}
		if expiresAt != "" {
			ea = expiresAt
		}
		res, err := store.Exec("INSERT INTO tokens (token, group_id, pane_id, perms, note, expires_at) VALUES (?,?,?,?,?,?)",
			token, gid, paneID, permsStr, note, ea)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		J(w, M{"token": token, "id": id})
	}
}

func handleAuthTokenDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		httpErr(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/auth/tokens/")
	res, err := store.Exec("DELETE FROM tokens WHERE id=?", id)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httpErr(w, 404, "Token not found")
		return
	}
	J(w, M{"success": true, "message": "Token " + id + " deleted"})
}
