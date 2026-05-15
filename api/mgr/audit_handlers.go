package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"ttyd-go/mgr/audit"
)

// Routes registered in main.go:
//   GET /api/audit/events                — list events with filters
//   GET /api/audit/events/{id}           — single event detail
//   GET /api/audit/stats                 — aggregations
//   GET /api/audit/agents                — agents that have any events

func handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	opts := parseQueryOpts(r)
	result, err := audit.Query(opts)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, result)
}

func handleAuditEventByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/audit/events/"))
	id = strings.TrimRight(id, "/")
	if id == "" {
		httpErr(w, http.StatusBadRequest, "missing_id")
		return
	}
	event, err := audit.GetEventByID(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if event == nil {
		httpErr(w, http.StatusNotFound, "not_found")
		return
	}
	J(w, event)
}

func handleAuditStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	opts := parseQueryOpts(r)
	stats, err := audit.ComputeStats(opts)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, stats)
}

func handleAuditAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	agents, err := audit.Agents()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	J(w, M{"agents": agents})
}

func parseQueryOpts(r *http.Request) audit.QueryOpts {
	q := r.URL.Query()
	opts := audit.QueryOpts{
		AgentID:   strings.TrimSpace(q.Get("agent_id")),
		Direction: strings.TrimSpace(q.Get("direction")),
	}
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.From = t
		}
	}
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			opts.To = t
		}
	}
	if v := q.Get("severity"); v != "" {
		opts.Severities = audit.SeveritiesFromCSV(v)
	}
	if v := q.Get("rule_id"); v != "" {
		for _, r := range strings.Split(v, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				opts.RuleIDs = append(opts.RuleIDs, r)
			}
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Offset = n
		}
	}
	return opts
}
