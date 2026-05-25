package audit

// Operator CLI for the autonomous policy agent. Subcommands:
//
//   cicy-code audit autonomy run           — synchronous "act now" tick
//   cicy-code audit autonomy decisions     — list recent decisions (table)
//   cicy-code audit autonomy explain <id>  — narrate a past decision
//   cicy-code audit autonomy revert <id>   — roll back via git
//   cicy-code audit autonomy show-config   — print effective autonomy.json
//
// All commands operate on the local filesystem state (~/cicy-ai/...) and
// do NOT require the cicy-code HTTP server to be running. The autonomy
// loop itself runs only inside the server; these CLI commands shortcut
// the same code paths for one-shot use.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func runAutonomyCmd(args []string) int {
	if len(args) == 0 {
		printAutonomyUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "run":
		return runAutonomyRunCmd(args[1:])
	case "decisions", "ls":
		return runAutonomyDecisionsCmd(args[1:])
	case "explain":
		return runAutonomyExplainCmd(args[1:])
	case "revert":
		return runAutonomyRevertCmd(args[1:])
	case "show-config":
		return runAutonomyShowConfigCmd(args[1:])
	case "help", "-h", "--help":
		printAutonomyUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown autonomy subcommand: %s\n\n", args[0])
		printAutonomyUsage(os.Stderr)
		return 2
	}
}

func printAutonomyUsage(w *os.File) {
	fmt.Fprint(w, `Usage: cicy-code audit autonomy <command> [args]

Commands:
  run                       Run a single tick synchronously (manual trigger).
                            Useful for sanity checking + first-tick demo.
  decisions [--limit=N]     List recent decisions, newest first.
  explain <id>              Have the LLM narrate a past decision.
  revert <id>               Roll back a decision via git (must have git_sha).
  show-config               Print the effective autonomy.json (with env fallbacks).

Exit codes:
  0  — success
  1  — operation reported an error (look at output)
  2  — invocation error (bad args, missing files)
`)
}

func runAutonomyRunCmd(_ []string) int {
	// Bootstrap the same pipeline + autonomy config the server would.
	if err := Init(); err != nil {
		fmt.Fprintln(os.Stderr, "error: audit.Init:", err)
		return 1
	}
	cfg, err := LoadAutonomyConfig("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: load autonomy config:", err)
		return 1
	}
	if cfg.LLM.Endpoint == "" || cfg.LLM.Model == "" {
		fmt.Fprintln(os.Stderr, "error: autonomy.json missing llm.endpoint or llm.model")
		fmt.Fprintln(os.Stderr, "       set CICY_AI_GATEWAY_LLM_ENDPOINT + CICY_AI_GATEWAY_LLM_MODEL or write autonomy.json")
		return 1
	}
	autonomyCfg = cfg
	defer func() { autonomyCfg = nil }()

	dec := RunOneTickNow(context.Background(), "manual-cli")
	body, _ := json.MarshalIndent(dec, "", "  ")
	fmt.Println(string(body))
	if dec.Error != "" {
		return 1
	}
	return 0
}

func runAutonomyDecisionsCmd(args []string) int {
	limit := 20
	for _, a := range args {
		if strings.HasPrefix(a, "--limit=") {
			fmt.Sscanf(strings.TrimPrefix(a, "--limit="), "%d", &limit)
		}
	}
	all := ReadDecisions(limit)
	if len(all) == 0 {
		fmt.Println("(no decisions yet)")
		return 0
	}
	fmt.Printf("%-40s %-9s %-22s %5s %5s %5s %s\n",
		"ID", "TRIGGER", "TIMESTAMP", "EVENT", "APPL", "SKIP", "GIT")
	for _, d := range all {
		applied := 0
		skipped := 0
		for _, a := range d.Actions {
			if a.Applied {
				applied++
			} else {
				skipped++
			}
		}
		sha := d.GitSHA
		if len(sha) > 8 {
			sha = sha[:8]
		}
		fmt.Printf("%-40s %-9s %-22s %5d %5d %5d %s\n",
			d.ID, d.Trigger,
			d.Timestamp.UTC().Format(time.RFC3339),
			d.EventsConsidered, applied, skipped, sha)
		if d.Error != "" {
			fmt.Printf("   error: %s\n", d.Error)
		}
	}
	return 0
}

func runAutonomyExplainCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cicy-code audit autonomy explain <decision-id>")
		return 2
	}
	if err := Init(); err != nil {
		fmt.Fprintln(os.Stderr, "error: audit.Init:", err)
		return 1
	}
	cfg, _ := LoadAutonomyConfig("")
	autonomyCfg = cfg
	defer func() { autonomyCfg = nil }()

	result, err := ExplainDecision(context.Background(), args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	body, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(body))
	return 0
}

func runAutonomyRevertCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cicy-code audit autonomy revert <decision-id>")
		return 2
	}
	result, err := RevertDecision(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	body, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(body))
	return 0
}

func runAutonomyShowConfigCmd(_ []string) int {
	cfg, err := LoadAutonomyConfig("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	body, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(body))
	return 0
}
