// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// `cicy-code line …` — the headless entry point.
//
// This is a thin client over the daemon's /api/line/* engine, the same way
// cicy-repl is a thin client over /api/cicy/chat. The engine has to live in the
// daemon because a station runs through the LOCAL GATEWAY — that is what buys
// per-station cost accounting and the audit trail. A standalone runner would
// have neither.
//
//	cicy-code line validate <line.yaml>
//	cicy-code line run      <line.yaml> --seed '<json>' | --seed @seed.json [--json] [--yes]
//	cicy-code line approve  <run-id> --by <who> [--note <why>]
//	cicy-code line runs     [<run-id>]

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func lineServerBase() string {
	port := strings.TrimSpace(os.Getenv("CICY_API_PORT"))
	if port == "" {
		port = strings.TrimSpace(os.Getenv("PORT"))
	}
	if port == "" {
		port = "8008"
	}
	return "http://127.0.0.1:" + port
}

func runLineCLI(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printLineHelp()
		return 0
	}
	switch args[0] {
	case "validate":
		return lineCLIValidate(args[1:])
	case "run":
		return lineCLIRun(args[1:])
	case "approve":
		return lineCLIApprove(args[1:])
	case "runs":
		return lineCLIRuns(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "line: unknown subcommand %q\n\n", args[0])
		printLineHelp()
		return 2
	}
}

func printLineHelp() {
	fmt.Print(`cicy-code line — run a production line from its Line Spec

  line validate <line.yaml>
  line run      <line.yaml> --seed '<json>'|@file [--json] [--yes]
  line approve  <run-id> --by <who> [--note <why>]
  line runs     [<run-id>]

A line is a DECLARATIVE spec, not a script: stations with I/O contracts, quality
gates that loop work back, and a human approval gate the ENGINE enforces (the run
stops dead; nothing outward-facing happens until 'line approve' names a person).

Unit cost is MEASURED from the gateway's per-request accounting — every station
runs as its own ephemeral agent with its own usage ledger. It is never estimated.

  --yes   auto-approve human gates. Explicit, and RECORDED in the run as
          "AUTO-APPROVED — no human reviewed this". For CI. Never the default.

Requires a running cicy-code daemon (that is where the gateway lives).
`)
}

// linePost fires a request and streams the SSE body, returning the final run.
func linePost(path string, payload interface{}, onEvent func(M)) (*LineRun, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, lineServerBase()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// A line run is model work: it can legitimately take a long time.
	resp, err := (&http.Client{Timeout: 6 * time.Hour}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach cicy-code at %s — is the daemon running? (%v)", lineServerBase(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		var e struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Detail != "" {
			return nil, fmt.Errorf("%s", e.Detail)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var final *LineRun
	var runErr error
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev M
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			continue
		}
		if aiGatewayString(ev["type"]) == "result" {
			if msg := aiGatewayString(ev["error"]); msg != "" {
				runErr = fmt.Errorf("%s", msg)
			}
			if rawRun, ok := ev["run"]; ok {
				b, _ := json.Marshal(rawRun)
				var r LineRun
				if json.Unmarshal(b, &r) == nil {
					final = &r
				}
			}
			continue
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return final, runErr
}

func lineCLIValidate(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cicy-code line validate <line.yaml>")
		return 2
	}
	spec, err := LoadLineSpec(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	fmt.Printf("✓ %s@%s — %d stations\n", spec.ID, spec.Version, len(spec.Stations))
	for _, st := range spec.Stations {
		switch {
		case st.IsHumanGate():
			fmt.Printf("  ⛔ %-12s human approval gate (the engine STOPS here)\n", st.ID)
		case st.Gate != nil:
			target, _ := st.Gate.ReworkTarget()
			fmt.Printf("  ▸ %-12s %s → %s   gate: %s≠%s → rework → %s (max %d)\n",
				st.ID, st.In, st.Out, st.Gate.Field, st.Gate.Pass, target, st.Gate.MaxRework)
		default:
			fmt.Printf("  ▸ %-12s %s → %s\n", st.ID, st.In, st.Out)
		}
	}
	return 0
}

func lineCLIRun(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cicy-code line run <line.yaml> --seed '<json>'|@file [--json] [--yes]")
		return 2
	}
	specPath := args[0]
	var seedRaw, agentID string
	asJSON, autoApprove := false, false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--seed":
			if i+1 < len(args) {
				i++
				seedRaw = args[i]
			}
		case "--agent":
			if i+1 < len(args) {
				i++
				agentID = strings.TrimSpace(args[i])
			}
		case "--json":
			asJSON = true
		case "--yes", "--auto-approve":
			autoApprove = true
		default:
			fmt.Fprintf(os.Stderr, "line run: unknown flag %q\n", args[i])
			return 2
		}
	}
	seed, err := parseLineSeed(seedRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}
	abs, err := absLinePath(specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}

	onEvent := func(ev M) {
		if asJSON {
			return
		}
		switch aiGatewayString(ev["type"]) {
		case "station_start":
			fmt.Printf("▸ %s …\n", aiGatewayString(ev["station"]))
		case "station_done":
			fmt.Printf("  ✓ %s  $%.4f\n", aiGatewayString(ev["station"]), aiGatewayFloat(ev["cost"]))
		case "rework":
			fmt.Printf("  ↺ %s failed the gate → back to %s (attempt %d)\n",
				aiGatewayString(ev["station"]), aiGatewayString(ev["back_to"]), aiGatewayInt(ev["attempt"]))
		case "auto_approved":
			fmt.Printf("  ⚠ %s AUTO-APPROVED — no human reviewed this\n", aiGatewayString(ev["station"]))
		case "awaiting_approval":
			fmt.Printf("  ⛔ %s — awaiting human approval\n", aiGatewayString(ev["station"]))
		}
	}

	run, runErr := linePost("/api/line/run", M{
		"spec": abs, "seed": seed, "auto_approve": autoApprove, "agent": agentID,
	}, onEvent)
	return reportLineRun(run, runErr, asJSON)
}

func lineCLIApprove(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cicy-code line approve <run-id> --by <who> [--note <why>]")
		return 2
	}
	runID := args[0]
	by, note := "", ""
	asJSON := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--by":
			if i+1 < len(args) {
				i++
				by = args[i]
			}
		case "--note":
			if i+1 < len(args) {
				i++
				note = args[i]
			}
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(os.Stderr, "line approve: unknown flag %q\n", args[i])
			return 2
		}
	}
	if strings.TrimSpace(by) == "" {
		fmt.Fprintln(os.Stderr, "line approve: --by is required — the record must name who approved this")
		return 2
	}
	run, err := linePost("/api/line/approve", M{"run": runID, "by": by, "note": note}, func(ev M) {
		if !asJSON {
			if t := aiGatewayString(ev["type"]); t == "station_done" {
				fmt.Printf("  ✓ %s  $%.4f\n", aiGatewayString(ev["station"]), aiGatewayFloat(ev["cost"]))
			}
		}
	})
	return reportLineRun(run, err, asJSON)
}

func lineCLIRuns(args []string) int {
	url := lineServerBase() + "/api/line/runs"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		url += "?run=" + args[0]
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach cicy-code at %s — is the daemon running? (%v)\n", lineServerBase(), err)
		return 3
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "%s\n", strings.TrimSpace(string(body)))
		return 1
	}
	fmt.Println(string(body))
	return 0
}

func reportLineRun(run *LineRun, runErr error, asJSON bool) int {
	if asJSON {
		out := M{}
		if run != nil {
			out["run"] = run
		}
		if runErr != nil {
			out["error"] = runErr.Error()
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	}
	if runErr != nil {
		if !asJSON {
			fmt.Fprintf(os.Stderr, "✗ %v\n", runErr)
		}
		return 1
	}
	if run == nil {
		if !asJSON {
			fmt.Fprintln(os.Stderr, "✗ no run record returned")
		}
		return 1
	}
	if !asJSON {
		m := run.Metrics
		fmt.Println()
		switch run.Status {
		case "awaiting_approval":
			fmt.Printf("⛔ run %s parked at the human gate %q.\n", run.ID, run.AwaitingStation)
			fmt.Printf("   Nothing outward-facing has happened. To let it through:\n")
			fmt.Printf("     cicy-code line approve %s --by <who>\n", run.ID)
		case "done":
			fmt.Printf("✓ run %s done\n", run.ID)
		default:
			fmt.Printf("✗ run %s %s\n", run.ID, run.Status)
		}
		fmt.Printf("   unit cost  $%.4f   (measured, %d gateway requests)\n", m.UnitCostUSD, m.Requests)
		fmt.Printf("   cycle time %.1fs   yield %.0f%%  (%d rework of %d attempts)\n",
			m.CycleTimeS, m.Yield*100, m.Reworks, m.Attempts)
		if m.Bottleneck != "" {
			fmt.Printf("   bottleneck %s (costliest station)\n", m.Bottleneck)
		}
	}
	// A run parked at a gate is NOT a success — CI must not treat it as one.
	if run.Status == "awaiting_approval" {
		return 10
	}
	if run.Status != "done" {
		return 1
	}
	return 0
}

func parseLineSeed(raw string) (map[string]interface{}, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]interface{}{}, nil
	}
	if strings.HasPrefix(raw, "@") {
		b, err := os.ReadFile(strings.TrimPrefix(raw, "@"))
		if err != nil {
			return nil, fmt.Errorf("read seed file: %w", err)
		}
		raw = string(b)
	}
	var seed map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &seed); err != nil {
		return nil, fmt.Errorf("--seed must be a JSON object: %w", err)
	}
	return seed, nil
}

// absLinePath makes the spec path absolute BEFORE handing it to the daemon —
// the daemon's working directory is not the caller's, so a relative path would
// resolve somewhere else entirely (or, worse, resolve to a different file).
func absLinePath(p string) (string, error) {
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("no such spec: %s", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}
