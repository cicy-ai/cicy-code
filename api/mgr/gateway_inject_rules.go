package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Gateway-side injection of project & workspace CLAUDE.md / AGENTS.md content
// into outgoing API request system prompts. See docs/gateway-inject-rules-files.md.

const (
	rulesFileMaxBytes      = 50 * 1024  // 50KB per file truncation cap
	rulesOverlayMaxBytes   = 100 * 1024 // 100KB total overlay cap
	rulesFileTruncatedNote = "\n... [truncated %d bytes]"
	atImportMaxDepth       = 5 // max recursion depth for @-import expansion
)

// atImportLineRE matches lines starting with @<path>, e.g. `@/home/cicy/...`
var atImportLineRE = regexp.MustCompile(`(?m)^@(\S+)`)

// rulesFile is one collected rules document (CLAUDE.md / AGENTS.md).
type rulesFile struct {
	Path      string // absolute path
	Scope     string // "project" or "workspace"
	PaneID    string // short pane id (only meaningful for workspace scope)
	Content   string // file content, possibly truncated
	Bytes     int    // original byte length on disk
	Truncated bool
}

// rulesFileCacheEntry is mtime+size-keyed cache content; cache miss re-reads.
type rulesFileCacheEntry struct {
	mtime   int64
	size    int64
	content string
}

var rulesFileCache sync.Map // key=abs path string, value=*rulesFileCacheEntry

// gatewayInjectRulesGlobalEnabled returns the env-driven master switch.
// Default ON. Set CICY_GATEWAY_INJECT_RULES=0 to disable globally.
func gatewayInjectRulesGlobalEnabled() bool {
	v := strings.TrimSpace(os.Getenv("CICY_GATEWAY_INJECT_RULES"))
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// gatewayInjectRulesPaneEnabled checks the per-pane DB flag. Returns true when
// no row exists (defensive default = on) so a missing column doesn't accidentally
// disable injection for all panes.
func gatewayInjectRulesPaneEnabled(agentID string) bool {
	full := normPaneID(strings.TrimSpace(agentID))
	if full == "" {
		return true
	}
	var enabled int
	err := store.QueryRow(
		"SELECT COALESCE(inject_rules_files, 1) FROM agent_config WHERE pane_id=?",
		full,
	).Scan(&enabled)
	if err != nil {
		return true
	}
	return enabled != 0
}

// loadRulesFileWithCache returns the (possibly truncated) content of path,
// using the (mtime, size) cache. Missing/unreadable files return ("", 0, false, false).
func loadRulesFileWithCache(path string) (content string, bytes int, truncated bool, ok bool) {
	abs := strings.TrimSpace(path)
	if abs == "" {
		return "", 0, false, false
	}
	stat, err := os.Stat(abs)
	if err != nil || stat.IsDir() {
		return "", 0, false, false
	}
	mtime := stat.ModTime().UnixNano()
	size := stat.Size()
	if cached, ok := rulesFileCache.Load(abs); ok {
		entry := cached.(*rulesFileCacheEntry)
		if entry.mtime == mtime && entry.size == size {
			// Note: cached content is the post-truncation string. Recompute the
			// "truncated" flag from raw size so callers see a consistent value.
			return entry.content, int(size), size > rulesFileMaxBytes, true
		}
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", 0, false, false
	}
	bytesRead := len(raw)
	body := string(raw)
	if bytesRead > rulesFileMaxBytes {
		body = body[:rulesFileMaxBytes] + fmt.Sprintf(rulesFileTruncatedNote, bytesRead-rulesFileMaxBytes)
		truncated = true
	}
	rulesFileCache.Store(abs, &rulesFileCacheEntry{mtime: mtime, size: size, content: body})
	return body, bytesRead, truncated, true
}

// expandAtImports recursively resolves @-import lines (e.g. `@/path/to/file`)
// within content. filePath is the source file's abs path (for relative refs).
// visited prevents cycles; depth limits recursion.
func expandAtImports(content, filePath string, depth int, visited map[string]struct{}) string {
	if depth <= 0 || !strings.Contains(content, "@") {
		return content
	}
	return atImportLineRE.ReplaceAllStringFunc(content, func(match string) string {
		refPath := strings.TrimSpace(match[1:]) // strip leading @
		var abs string
		if filepath.IsAbs(refPath) {
			abs = filepath.Clean(refPath)
		} else {
			abs = filepath.Clean(filepath.Join(filepath.Dir(filePath), refPath))
		}
		if _, seen := visited[abs]; seen {
			return "# [@-import cycle skipped: " + abs + "]"
		}
		refContent, _, _, ok := loadRulesFileWithCache(abs)
		if !ok {
			return "# [@-import not found: " + abs + "]"
		}
		newVisited := make(map[string]struct{}, len(visited)+1)
		for k := range visited {
			newVisited[k] = struct{}{}
		}
		newVisited[abs] = struct{}{}
		expanded := expandAtImports(refContent, abs, depth-1, newVisited)
		return strings.TrimRight(expanded, "\n")
	})
}

// agentInspectorCollectRulesFiles walks the project + workspace layers and
// returns the in-order, deduplicated, size-capped list of rules documents.
// Returns nil when the global / per-pane switch is off.
func agentInspectorCollectRulesFiles(agentID string) []rulesFile {
	if !gatewayInjectRulesGlobalEnabled() {
		return nil
	}
	full := normPaneID(strings.TrimSpace(agentID))
	if full == "" {
		return nil
	}
	if !gatewayInjectRulesPaneEnabled(full) {
		return nil
	}

	short := shortPaneID(full)
	workspace, _, _ := agentInspectorLoadPaneConfig(full)
	workspace = strings.TrimSpace(workspace)
	project := strings.TrimSpace(agentInspectorProjectKey(full))

	seen := map[string]struct{}{}
	out := make([]rulesFile, 0, 4)
	total := 0

	tryAdd := func(scope, paneID, path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		content, bytes, truncated, ok := loadRulesFileWithCache(abs)
		if !ok {
			return
		}
		// Expand gateway-side @-imports so referenced files are inlined.
		visited := map[string]struct{}{abs: {}}
		content = expandAtImports(content, abs, atImportMaxDepth, visited)
		// Re-apply per-file cap after expansion (referenced content may grow it).
		if len(content) > rulesFileMaxBytes {
			content = content[:rulesFileMaxBytes] + fmt.Sprintf(rulesFileTruncatedNote, len(content)-rulesFileMaxBytes)
			truncated = true
		}
		// Enforce overall overlay cap. Drop late entries (i.e. self/workspace
		// arrives after project, so when over budget we'd prefer to drop the
		// project layer). But the simple "stop adding" approach satisfies the
		// spec when callers feed project first and workspace second AND prefer
		// keeping the most specific layer. We invert: feed workspace first
		// internally? No — keep insertion order project-first for readability,
		// and rely on rulesFileMaxBytes per-file + total cap here. If the cap
		// kicks in we keep what's already in (less specific) and drop the new
		// (more specific). That's wrong for our policy. So instead: do a
		// post-pass below to drop project-layer entries if total exceeds cap.
		out = append(out, rulesFile{
			Path:      abs,
			Scope:     scope,
			PaneID:    paneID,
			Content:   content,
			Bytes:     bytes,
			Truncated: truncated,
		})
		total += len(content)
	}

	// Project layer first (less specific, drops first when over cap).
	if project != "" {
		tryAdd("project", "", filepath.Join(project, "CLAUDE.md"))
		tryAdd("project", "", filepath.Join(project, "AGENTS.md"))
	}
	// Workspace layer second (more specific, preserved when over cap).
	if workspace != "" {
		tryAdd("workspace", short, filepath.Join(workspace, "CLAUDE.md"))
		tryAdd("workspace", short, filepath.Join(workspace, "AGENTS.md"))
	}

	// Total cap enforcement: if we blew past the cap, drop project entries
	// in order until under budget. Workspace entries stay (more specific).
	if total > rulesOverlayMaxBytes {
		survivors := make([]rulesFile, 0, len(out))
		dropped := 0
		// Drop project entries first (forward pass), then if still over, drop
		// workspace entries from the front (rare).
		for _, item := range out {
			if total-dropped <= rulesOverlayMaxBytes {
				survivors = append(survivors, item)
				continue
			}
			if item.Scope == "project" {
				dropped += len(item.Content)
				continue
			}
			survivors = append(survivors, item)
		}
		// Second pass if still over (workspace alone exceeds cap)
		out = survivors
		runningTotal := 0
		for _, item := range out {
			runningTotal += len(item.Content)
		}
		if runningTotal > rulesOverlayMaxBytes {
			survivors = survivors[:0]
			budget := rulesOverlayMaxBytes
			for _, item := range out {
				if budget <= 0 {
					break
				}
				if len(item.Content) <= budget {
					survivors = append(survivors, item)
					budget -= len(item.Content)
					continue
				}
				// truncate this entry to remaining budget
				clipped := item
				clipped.Content = item.Content[:budget] +
					fmt.Sprintf(rulesFileTruncatedNote, len(item.Content)-budget)
				clipped.Truncated = true
				survivors = append(survivors, clipped)
				budget = 0
			}
			out = survivors
		}
	}
	return out
}

// agentInspectorBuildRulesFilesOverlay returns the XML-wrapped concatenation
// of all collected rules files, or empty string when nothing to inject.
func agentInspectorBuildRulesFilesOverlay(agentID string) string {
	files := agentInspectorCollectRulesFiles(agentID)
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range files {
		tag := "project-rules"
		paneAttr := ""
		if f.Scope == "workspace" {
			tag = "workspace-rules"
			if f.PaneID != "" {
				paneAttr = fmt.Sprintf(` pane=%q`, f.PaneID)
			}
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(fmt.Sprintf("<%s%s source=%q>\n", tag, paneAttr, f.Path))
		b.WriteString(strings.TrimRight(f.Content, "\n"))
		b.WriteString(fmt.Sprintf("\n</%s>", tag))
	}
	return b.String()
}

// rulesFilesOverlayDebugLog logs once per request (best-effort) the size
// breakdown of the injected overlay. Helpful when diagnosing token spend.
func rulesFilesOverlayDebugLog(agentID string, files []rulesFile, total int) {
	if len(files) == 0 {
		return
	}
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, fmt.Sprintf("%s(%dB%s)", f.Scope, len(f.Content), boolStr(f.Truncated, ",trunc")))
	}
	log.Printf("[gateway-inject] agent=%s files=%d total=%dB %s", agentID, len(files), total, strings.Join(parts, " "))
}

func boolStr(b bool, ifTrue string) string {
	if b {
		return ifTrue
	}
	return ""
}

// agentInspectorRulesFilesBundle returns a JSON-friendly summary of the
// collected rules files for inspector UI. Includes per-file source/scope/
// byte-count/truncated, plus a total.
func agentInspectorRulesFilesBundle(agentID string) M {
	files := agentInspectorCollectRulesFiles(agentID)
	items := make([]M, 0, len(files))
	total := 0
	for _, f := range files {
		items = append(items, M{
			"source":    f.Path,
			"scope":     f.Scope,
			"pane_id":   f.PaneID,
			"bytes":     f.Bytes,
			"injected":  len(f.Content),
			"truncated": f.Truncated,
			"content":   f.Content,
		})
		total += len(f.Content)
	}
	return M{
		"items":          items,
		"file_count":     len(files),
		"total_bytes":    total,
		"global_enabled": gatewayInjectRulesGlobalEnabled(),
		"pane_enabled":   gatewayInjectRulesPaneEnabled(agentID),
		"per_file_max":   rulesFileMaxBytes,
		"overlay_max":    rulesOverlayMaxBytes,
	}
}
