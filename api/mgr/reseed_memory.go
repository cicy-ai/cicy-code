package main

// `cicy-code reseed-memory` — one-shot CLI that regenerates agents' guidance
// files (CLAUDE.md / AGENTS.md / .kiro/steering/memory.md) from the CURRENT
// memory templates (~/cicy-ai/memory/{global.md, projects/<slug>.md,
// agents/<slug>.md}). Built for the roster memory-sync flow: ONLINE agents are
// told (broadcast) to refresh their own file; OFFLINE agents cannot, so this
// command rewrites theirs directly — they pick the new content up on next boot.
//
// Safety model:
//   - The existing file is ALWAYS backed up to
//     <workspace>/.cicy/memory-backups/<name>.<utc-timestamp> before overwrite.
//   - Content below a `<!-- cicy:custom -->` marker line is carried over into
//     the regenerated file, so agents/operators can keep customisations in a
//     reseed-proof section.
//   - Trigger surface is the local shell only (no HTTP endpoint): master, the
//     orchestrator, or full agents with exec. Lite agents and external a2a
//     traffic cannot reach it.
//
// Run with the daemon up is safe: the DB is opened read-only-style under WAL
// and guidance files are only written by the daemon at pane creation.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// memoryCustomMarker splits template-managed content (above) from the agent's
// own additions (below). Everything from the marker line on survives a reseed.
const memoryCustomMarker = "<!-- cicy:custom -->"

// customMarkerOffset returns the byte offset of the first line that IS the
// custom marker (modulo surrounding whitespace), or -1. Mid-line mentions of
// the marker string do not count.
func customMarkerOffset(s string) int {
	off := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.TrimSpace(line) == memoryCustomMarker {
			return off
		}
		off += len(line)
	}
	return -1
}

type reseedTarget struct {
	paneID          string
	shortID         string
	workspace       string
	agentType       string
	projectTemplate string
	roleTemplate    string
}

func runReseedMemory(args []string) int {
	fs := flag.NewFlagSet("reseed-memory", flag.ExitOnError)
	ids := fs.String("ids", "", "comma-separated agent ids (w-10042,w-10043)")
	offline := fs.Bool("offline", false, "target every agent with no live tmux session")
	all := fs.Bool("all", false, "target every agent in agent_config (online ones too)")
	dryRun := fs.Bool("dry-run", false, "report what would change, write nothing")
	outDir := fs.String("out-dir", "", "preview mode: write regenerated files under <out-dir>/<agent>/<file> instead of in place (no backups taken)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cicy-code reseed-memory (--ids w-1,w-2 | --offline | --all) [--dry-run]

Regenerates guidance files (CLAUDE.md/AGENTS.md) from the current memory
templates. Always backs the old file up to <ws>/.cicy/memory-backups/; content
below a "%s" line is preserved.
`, memoryCustomMarker)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	modes := 0
	for _, m := range []bool{*ids != "", *offline, *all} {
		if m {
			modes++
		}
	}
	if modes != 1 {
		fs.Usage()
		return 2
	}

	initDB()
	targets, err := loadReseedTargets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reseed-memory: %v\n", err)
		return 1
	}

	// Filter per mode.
	var picked []reseedTarget
	switch {
	case *ids != "":
		want := map[string]bool{}
		for _, id := range strings.Split(*ids, ",") {
			if id = strings.TrimSpace(id); id != "" {
				want[shortPaneID(normPaneID(id))] = true
			}
		}
		for _, t := range targets {
			if want[t.shortID] {
				picked = append(picked, t)
				delete(want, t.shortID)
			}
		}
		for id := range want {
			fmt.Fprintf(os.Stderr, "reseed-memory: %s not found in agent_config — skipped\n", id)
		}
	case *all:
		picked = targets
	case *offline:
		for _, t := range targets {
			if _, err := runTmux("has-session", "-t", t.shortID); err != nil {
				picked = append(picked, t)
			}
		}
	}
	if len(picked) == 0 {
		fmt.Println("reseed-memory: no matching agents")
		return 0
	}

	failed := 0
	for _, t := range picked {
		if err := reseedOne(t, *dryRun, *outDir); err != nil {
			fmt.Fprintf(os.Stderr, "reseed-memory: %s: %v\n", t.shortID, err)
			failed++
		}
	}
	fmt.Printf("reseed-memory: %d/%d done (dry-run=%v)\n", len(picked)-failed, len(picked), *dryRun)
	if failed > 0 {
		return 1
	}
	return 0
}

func loadReseedTargets() ([]reseedTarget, error) {
	rows, err := store.Query(`SELECT pane_id, COALESCE(workspace,''), COALESCE(agent_type,''),
		COALESCE(project_template,''), COALESCE(role_template,'') FROM agent_config ORDER BY pane_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reseedTarget
	for rows.Next() {
		var t reseedTarget
		if err := rows.Scan(&t.paneID, &t.workspace, &t.agentType, &t.projectTemplate, &t.roleTemplate); err != nil {
			continue
		}
		t.shortID = shortPaneID(t.paneID)
		t.workspace = expandHome(strings.TrimSpace(t.workspace))
		out = append(out, t)
	}
	return out, rows.Err()
}

func reseedOne(t reseedTarget, dryRun bool, outDir string) error {
	if t.workspace == "" {
		return fmt.Errorf("no workspace configured")
	}
	rel := guidanceFilenameForAgentType(t.agentType)
	if rel == "" {
		return fmt.Errorf("agent_type %q has no guidance file — skipped", t.agentType)
	}
	path := filepath.Join(t.workspace, rel)

	content := composeGuidanceContent(t.workspace, t.agentType, t.paneID, t.projectTemplate, t.roleTemplate, "")

	old, readErr := os.ReadFile(path)
	if readErr == nil {
		// Carry the agent's reseed-proof section over. The marker must be a
		// line of its own — prose that merely MENTIONS the marker (e.g. a
		// charter describing this feature) must not trigger the carry-over.
		if i := customMarkerOffset(string(old)); i >= 0 {
			content = strings.TrimRight(content, "\n") + "\n\n" + string(old)[i:]
		}
		if string(old) == content {
			fmt.Printf("  %s: %s unchanged\n", t.shortID, rel)
			return nil
		}
	}
	if outDir != "" {
		// Preview: materialise the regenerated file out of place so the caller
		// can diff it against the live one. Never touches the workspace.
		dst := filepath.Join(outDir, t.shortID, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(content), 0644); err != nil {
			return err
		}
		fmt.Printf("  %s: %s differs → %s\n", t.shortID, rel, dst)
		return nil
	}
	if dryRun {
		fmt.Printf("  %s: would rewrite %s (backup first)\n", t.shortID, rel)
		return nil
	}

	if readErr == nil {
		backupDir := filepath.Join(t.workspace, ".cicy", "memory-backups")
		if err := os.MkdirAll(backupDir, 0755); err != nil {
			return fmt.Errorf("backup dir: %w", err)
		}
		backup := filepath.Join(backupDir, filepath.Base(rel)+"."+time.Now().UTC().Format("20060102-150405"))
		if err := os.WriteFile(backup, old, 0644); err != nil {
			return fmt.Errorf("backup write: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	fmt.Printf("  %s: %s reseeded\n", t.shortID, rel)
	return nil
}
