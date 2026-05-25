package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// QueryOpts are filter and pagination options for Query. Zero values match
// "anything" except for Limit which falls back to a sane default.
type QueryOpts struct {
	AgentID    string
	From       time.Time
	To         time.Time
	Severities []Severity
	RuleIDs    []string
	Direction  string // outbound | inbound | ""

	Limit  int
	Offset int
}

// QueryResult carries the matching page of events plus the full match count
// for pagination.
type QueryResult struct {
	Events []Event `json:"events"`
	Total  int     `json:"total"`
}

const (
	defaultQueryLimit = 100
	maxQueryLimit     = 1000
	defaultLookbackDays = 7
)

// Query returns events matching opts. Source selection:
//   - If AgentID is set: scan that agent's audit.ndjson only.
//   - Otherwise: scan the global index NDJSON files covering the time range.
//
// Walking-skeleton implementation: in-memory filter/sort. Phase 2 introduces
// a SQLite-FTS index to keep this fast as data grows.
func Query(opts QueryOpts) (*QueryResult, error) {
	if globalPipeline == nil {
		return &QueryResult{Events: []Event{}, Total: 0}, nil
	}
	if opts.Limit <= 0 || opts.Limit > maxQueryLimit {
		opts.Limit = defaultQueryLimit
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	if opts.To.IsZero() {
		opts.To = time.Now().UTC()
	}
	if opts.From.IsZero() {
		opts.From = opts.To.Add(-time.Duration(defaultLookbackDays) * 24 * time.Hour)
	}

	events, err := loadEvents(opts)
	if err != nil {
		return nil, err
	}

	filtered := events[:0]
	for _, e := range events {
		if !matchesFilters(e, opts) {
			continue
		}
		filtered = append(filtered, e)
	}

	// Newest first by RFC3339Nano string compare (stable for same prefix).
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Timestamp > filtered[j].Timestamp
	})

	total := len(filtered)
	start := opts.Offset
	if start > total {
		start = total
	}
	end := start + opts.Limit
	if end > total {
		end = total
	}
	page := filtered[start:end]
	if page == nil {
		page = []Event{}
	}
	return &QueryResult{Events: page, Total: total}, nil
}

// GetEventByID searches recent days of the global index for an event.
// Returns nil if not found within the lookback window.
func GetEventByID(id string) (*Event, error) {
	if globalPipeline == nil || id == "" {
		return nil, nil
	}
	now := time.Now().UTC()
	for i := 0; i < defaultLookbackDays+1; i++ {
		day := now.Add(-time.Duration(i) * 24 * time.Hour)
		path := globalPipeline.store.indexPathForDay(day)
		events, err := scanNDJSON(path)
		if err != nil {
			continue
		}
		for j := range events {
			if events[j].ID == id {
				return &events[j], nil
			}
		}
	}
	return nil, nil
}

// Agents returns the agent IDs that have any audit data on disk.
// Walking-skeleton implementation: list directories under workers root.
func Agents() ([]string, error) {
	if globalPipeline == nil {
		return []string{}, nil
	}
	entries, err := os.ReadDir(globalPipeline.store.workersRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := globalPipeline.store.agentAuditPath(e.Name())
		if _, err := os.Stat(path); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Stats summarizes events in the lookback window. opts limits Time range.
type Stats struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ByRule     map[string]int `json:"by_rule"`
	ByAgent    map[string]int `json:"by_agent"`
	ByAction   map[string]int `json:"by_action"`
	ByDirection map[string]int `json:"by_direction"`
}

func ComputeStats(opts QueryOpts) (*Stats, error) {
	// Force a larger limit for stats so all events in the window are counted.
	opts.Limit = maxQueryLimit
	opts.Offset = 0
	result, err := Query(opts)
	if err != nil {
		return nil, err
	}
	s := &Stats{
		Total:       result.Total,
		BySeverity:  map[string]int{},
		ByRule:      map[string]int{},
		ByAgent:     map[string]int{},
		ByAction:    map[string]int{},
		ByDirection: map[string]int{},
	}
	for _, e := range result.Events {
		if e.Identity.AgentID != "" {
			s.ByAgent[e.Identity.AgentID]++
		}
		if e.Decision.Action != "" {
			s.ByAction[string(e.Decision.Action)]++
		}
		if e.Subject.Direction != "" {
			s.ByDirection[e.Subject.Direction]++
		}
		for _, f := range e.Findings {
			if f.Severity != "" {
				s.BySeverity[string(f.Severity)]++
			}
			if f.RuleID != "" {
				s.ByRule[f.RuleID]++
			}
		}
	}
	return s, nil
}

// loadEvents returns the raw event set before filtering/sorting/paging.
func loadEvents(opts QueryOpts) ([]Event, error) {
	if opts.AgentID != "" {
		return scanNDJSON(globalPipeline.store.agentAuditPath(opts.AgentID))
	}
	// Walk index files in the [From, To] window (UTC day granularity).
	out := []Event{}
	startDay := opts.From.UTC().Truncate(24 * time.Hour)
	endDay := opts.To.UTC().Truncate(24 * time.Hour)
	for day := startDay; !day.After(endDay); day = day.Add(24 * time.Hour) {
		path := globalPipeline.store.indexPathForDay(day)
		events, err := scanNDJSON(path)
		if err != nil {
			continue
		}
		out = append(out, events...)
	}
	return out, nil
}

func matchesFilters(e Event, opts QueryOpts) bool {
	if opts.Direction != "" && e.Subject.Direction != opts.Direction {
		return false
	}
	if e.Timestamp != "" {
		t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err == nil {
			if t.Before(opts.From) || t.After(opts.To) {
				return false
			}
		}
	}
	if len(opts.Severities) > 0 {
		if !hasMatchingSeverity(e, opts.Severities) {
			return false
		}
	}
	if len(opts.RuleIDs) > 0 {
		if !hasMatchingRuleID(e, opts.RuleIDs) {
			return false
		}
	}
	return true
}

func hasMatchingSeverity(e Event, want []Severity) bool {
	for _, f := range e.Findings {
		for _, s := range want {
			if f.Severity == s {
				return true
			}
		}
	}
	return false
}

func hasMatchingRuleID(e Event, want []string) bool {
	for _, f := range e.Findings {
		for _, r := range want {
			if f.RuleID == r {
				return true
			}
		}
	}
	return false
}

func scanNDJSON(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Event{}, nil
		}
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}
	defer f.Close()

	out := []Event{}
	sc := bufio.NewScanner(f)
	// Audit events with large payload metadata can exceed the default 64KB.
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		// Skip seal markers if any (Phase 5 lifecycle adds these).
		if bytes.HasPrefix(line, []byte(`{"_seal":`)) {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			// Malformed line: skip but don't fail the whole query.
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("audit: scan %s: %w", path, err)
	}
	return out, nil
}

// SeveritiesFromCSV parses "high,medium" into []Severity, dropping unknowns.
func SeveritiesFromCSV(s string) []Severity {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	out := make([]Severity, 0, 4)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		switch Severity(p) {
		case SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
			out = append(out, Severity(p))
		}
	}
	return out
}
