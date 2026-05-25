package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// VerifyReport captures the result of verifying one NDJSON file.
type VerifyReport struct {
	Path       string
	EventCount int
	Errors     []VerifyError
}

func (r *VerifyReport) OK() bool { return len(r.Errors) == 0 }

// VerifyError types — fixed set so the operator can match on Kind.
const (
	VerifyErrJSONParse       = "json_parse"
	VerifyErrChainBreak      = "chain_break"
	VerifyErrHashMismatch    = "hash_mismatch"
	VerifyErrHashComputeFail = "hash_compute_fail"
	VerifyErrStateMismatch   = "state_mismatch"
)

// VerifyError is one observed integrity problem.
type VerifyError struct {
	LineNum int
	EventID string
	Kind    string
	Detail  string
}

// VerifyFile streams an audit NDJSON file and checks:
//
//   1. each line parses as Event
//   2. the first prev_hash equals "sha256:GENESIS"
//   3. every other prev_hash equals the previous event's self_hash
//   4. each self_hash matches the recomputed canonical hash of the line content
//
// If statePath is non-empty, the saved {last_hash, last_event_id, count} is
// cross-checked against the file's tail.
//
// Returns (report, ioErr). report is non-nil whenever the file could be opened
// and read at least partially; OS-level open/scan errors surface as ioErr.
func VerifyFile(path string, statePath string) (*VerifyReport, error) {
	report := &VerifyReport{Path: path}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)

	expectedPrev := ChainGenesis
	lastHash := ""
	lastID := ""
	lineNum := 0

	for sc.Scan() {
		lineNum++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		// Seal markers (Phase 5 lifecycle) are accepted but not chained.
		if bytes.HasPrefix(line, []byte(`{"_seal":`)) {
			continue
		}

		var e Event
		if jerr := json.Unmarshal(line, &e); jerr != nil {
			report.Errors = append(report.Errors, VerifyError{
				LineNum: lineNum,
				Kind:    VerifyErrJSONParse,
				Detail:  jerr.Error(),
			})
			continue
		}
		report.EventCount++

		if e.PrevHash != expectedPrev {
			report.Errors = append(report.Errors, VerifyError{
				LineNum: lineNum,
				EventID: e.ID,
				Kind:    VerifyErrChainBreak,
				Detail:  fmt.Sprintf("prev_hash=%s, expected=%s", e.PrevHash, expectedPrev),
			})
		}

		recomputed, herr := ComputeSelfHash(e)
		if herr != nil {
			report.Errors = append(report.Errors, VerifyError{
				LineNum: lineNum,
				EventID: e.ID,
				Kind:    VerifyErrHashComputeFail,
				Detail:  herr.Error(),
			})
			expectedPrev = e.SelfHash
			lastHash = e.SelfHash
			lastID = e.ID
			continue
		}
		if recomputed != e.SelfHash {
			report.Errors = append(report.Errors, VerifyError{
				LineNum: lineNum,
				EventID: e.ID,
				Kind:    VerifyErrHashMismatch,
				Detail:  fmt.Sprintf("recomputed=%s, recorded=%s", recomputed, e.SelfHash),
			})
		}

		expectedPrev = e.SelfHash
		lastHash = e.SelfHash
		lastID = e.ID
	}
	if err := sc.Err(); err != nil {
		return report, err
	}

	if statePath != "" {
		stateData, err := os.ReadFile(statePath)
		if err == nil {
			var state ChainState
			if jerr := json.Unmarshal(stateData, &state); jerr == nil {
				if state.LastHash != lastHash {
					report.Errors = append(report.Errors, VerifyError{
						Kind:   VerifyErrStateMismatch,
						Detail: fmt.Sprintf("state.last_hash=%s, file tail self_hash=%s", state.LastHash, lastHash),
					})
				}
				if state.LastEventID != lastID {
					report.Errors = append(report.Errors, VerifyError{
						Kind:   VerifyErrStateMismatch,
						Detail: fmt.Sprintf("state.last_event_id=%s, file tail id=%s", state.LastEventID, lastID),
					})
				}
				if state.Count != int64(report.EventCount) {
					report.Errors = append(report.Errors, VerifyError{
						Kind:   VerifyErrStateMismatch,
						Detail: fmt.Sprintf("state.count=%d, file events=%d", state.Count, report.EventCount),
					})
				}
			}
		}
	}

	return report, nil
}

// VerifyAll walks the standard cicy-code layout and verifies every per-agent
// audit.ndjson (with state cross-check) plus every per-day global index
// NDJSON (no state cross-check; the global chain-state.json tracks only the
// current tail across all days).
func VerifyAll(auditRoot, workersRoot string) ([]*VerifyReport, error) {
	var reports []*VerifyReport

	indexDir := filepath.Join(auditRoot, "index")
	if entries, err := os.ReadDir(indexDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
				continue
			}
			r, err := VerifyFile(filepath.Join(indexDir, e.Name()), "")
			if err != nil {
				continue
			}
			reports = append(reports, r)
		}
	}

	entries, err := os.ReadDir(workersRoot)
	if err != nil && !os.IsNotExist(err) {
		return reports, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		historyDir := filepath.Join(workersRoot, e.Name(), ".cicy", "history")
		ndjson := filepath.Join(historyDir, "audit.ndjson")
		if _, err := os.Stat(ndjson); err != nil {
			continue
		}
		state := filepath.Join(historyDir, "audit-chain.state")
		r, err := VerifyFile(ndjson, state)
		if err != nil {
			continue
		}
		reports = append(reports, r)
	}

	return reports, nil
}

// RunCLI dispatches `cicy-code audit <subcommand>` and returns a process exit
// code per design doc §18.E:
//
//	0 — all chains intact
//	1 — at least one chain has integrity errors
//	2 — invocation error (bad args, file not found, ...)
func RunCLI(args []string) int {
	if len(args) == 0 {
		printCLIUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "verify":
		return runVerifyCmd(args[1:])
	case "autonomy":
		return runAutonomyCmd(args[1:])
	case "help", "-h", "--help":
		printCLIUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown audit subcommand: %s\n\n", args[0])
		printCLIUsage(os.Stderr)
		return 2
	}
}

func runVerifyCmd(args []string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: resolve home:", err)
		return 2
	}
	auditRoot := filepath.Join(home, "cicy-ai", "audit")
	workersRoot := filepath.Join(home, "cicy-ai", "workers")

	var reports []*VerifyReport
	if len(args) > 0 {
		path := args[0]
		statePath := autoDetectStatePath(path)
		if len(args) >= 2 {
			statePath = args[1]
		}
		r, err := VerifyFile(path, statePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		reports = []*VerifyReport{r}
	} else {
		rs, err := VerifyAll(auditRoot, workersRoot)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		reports = rs
	}

	return printReports(os.Stdout, reports)
}

func autoDetectStatePath(ndjsonPath string) string {
	dir := filepath.Dir(ndjsonPath)
	base := filepath.Base(ndjsonPath)
	switch base {
	case "audit.ndjson":
		return filepath.Join(dir, "audit-chain.state")
	}
	return ""
}

func printReports(out io.Writer, reports []*VerifyReport) int {
	okFiles := 0
	failFiles := 0
	totalEvents := 0

	for _, r := range reports {
		totalEvents += r.EventCount
		if r.OK() {
			okFiles++
			fmt.Fprintf(out, "OK   %s  (%d events)\n", r.Path, r.EventCount)
			continue
		}
		failFiles++
		fmt.Fprintf(out, "FAIL %s  (%d events, %d errors)\n", r.Path, r.EventCount, len(r.Errors))
		for _, e := range r.Errors {
			location := fmt.Sprintf("line %d", e.LineNum)
			if e.EventID != "" {
				location = fmt.Sprintf("event %s @ line %d", e.EventID, e.LineNum)
			}
			fmt.Fprintf(out, "     %s [%s] %s\n", location, e.Kind, e.Detail)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "Summary: %d file(s), %d event(s) total, %d ok, %d failed\n",
		len(reports), totalEvents, okFiles, failFiles)

	if failFiles > 0 {
		return 1
	}
	return 0
}

func printCLIUsage(out io.Writer) {
	fmt.Fprintln(out, `Usage: cicy-code audit <subcommand> [args]

Subcommands:
  verify [PATH] [STATE]    Verify hash-chain integrity.
                           No PATH: verify every per-agent audit.ndjson under
                                    ~/cicy-ai/workers/* and every per-day
                                    global index NDJSON under ~/cicy-ai/audit/index.
                           PATH:    verify that single NDJSON file. STATE is
                                    its sibling audit-chain.state; auto-detected
                                    when PATH ends in audit.ndjson.

  autonomy <subcommand>    Autonomous policy agent operator commands:
                              run                  one tick now (synchronous)
                              decisions [--limit=N]  list recent decisions
                              explain <id>         LLM narration of a decision
                              revert  <id>         git-backed rollback
                              show-config          dump effective autonomy.json

Exit codes:
  0 — success
  1 — operation reported an error (look at output)
  2 — invocation error (bad args, file not found, ...)`)
}
