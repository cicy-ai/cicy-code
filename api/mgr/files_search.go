// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Native files: filename + content search endpoints.
//
// /api/fs/search  — fuzzy filename match, prefers `fd`, falls back to `find`
// /api/fs/grep    — full-text via ripgrep (`rg --json`); 503 if rg missing
//
// Both honor agent_id + workspace whitelist; results are workspace-relative.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)


// --- search --------------------------------------------------------------

type fsSearchMatch struct {
	Path  string  `json:"path"`
	Score float64 `json:"score"`
}

type fsSearchResponse struct {
	Matches   []fsSearchMatch `json:"matches"`
	ElapsedMs int64           `json:"elapsed_ms"`
	Backend   string          `json:"backend"`
	Truncated bool            `json:"truncated,omitempty"`
}

func handleFsSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	q := r.URL.Query()
	pattern := strings.TrimSpace(q.Get("q"))
	if pattern == "" {
		J(w, fsSearchResponse{Matches: []fsSearchMatch{}, Backend: "noop"})
		return
	}
	limit := 100
	if v, _ := strconv.Atoi(q.Get("limit")); v > 0 && v <= 500 {
		limit = v
	}
	abs, workspace, err := fsResolve(r, q.Get("dir"))
	if err != nil {
		fsErr(w, err)
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
	defer cancel()

	var (
		paths   []string
		backend string
	)
	if _, lookErr := exec.LookPath("fd"); lookErr == nil {
		paths, _ = runFd(ctx, abs, pattern, limit*4)
		backend = "fd"
	} else if _, lookErr := exec.LookPath("fdfind"); lookErr == nil {
		paths, _ = runFdfind(ctx, abs, pattern, limit*4)
		backend = "fd"
	} else {
		paths, _ = runFind(ctx, abs, pattern, limit*4)
		backend = "find"
	}

	matches := scoreAndSort(paths, pattern, workspace, abs)
	truncated := false
	if len(matches) > limit {
		matches = matches[:limit]
		truncated = true
	}
	if matches == nil {
		matches = []fsSearchMatch{}
	}
	J(w, fsSearchResponse{
		Matches:   matches,
		ElapsedMs: time.Since(start).Milliseconds(),
		Backend:   backend,
		Truncated: truncated,
	})
}

func runFd(ctx context.Context, root, pattern string, max int) ([]string, error) {
	args := []string{"--type", "f", "--hidden", "--no-ignore-vcs",
		"--exclude", ".git", "--exclude", "node_modules",
		"--max-results", strconv.Itoa(max),
		pattern, root}
	cmd := exec.CommandContext(ctx, "fd", args...)
	out, err := cmd.Output()
	if err != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, err
	}
	return splitLines(out), nil
}

func runFdfind(ctx context.Context, root, pattern string, max int) ([]string, error) {
	args := []string{"--type", "f", "--hidden", "--no-ignore-vcs",
		"--exclude", ".git", "--exclude", "node_modules",
		"--max-results", strconv.Itoa(max),
		pattern, root}
	cmd := exec.CommandContext(ctx, "fdfind", args...)
	out, err := cmd.Output()
	if err != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, err
	}
	return splitLines(out), nil
}

// runFind is the last-resort fallback. `find` does substring (-iname) and
// can't do fuzzy, but it's always available.
func runFind(ctx context.Context, root, pattern string, max int) ([]string, error) {
	glob := "*" + escapeFindGlob(pattern) + "*"
	args := []string{root, "-type", "f",
		"-not", "-path", "*/.git/*",
		"-not", "-path", "*/node_modules/*",
		"-iname", glob}
	cmd := exec.CommandContext(ctx, "find", args...)
	out, err := cmd.Output()
	if err != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, err
	}
	lines := splitLines(out)
	if len(lines) > max {
		lines = lines[:max]
	}
	return lines, nil
}

func escapeFindGlob(s string) string {
	r := strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]")
	return r.Replace(s)
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// scoreAndSort assigns a simple fuzzy score: prefer matches in the basename
// over deep path matches, prefer earlier hit position, and prefer shorter
// paths. Returns paths relative to the workspace.
func scoreAndSort(paths []string, pattern, workspace, _ string) []fsSearchMatch {
	out := make([]fsSearchMatch, 0, len(paths))
	lpat := strings.ToLower(pattern)
	for _, p := range paths {
		rel, err := filepath.Rel(workspace, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		base := strings.ToLower(filepath.Base(rel))
		lrel := strings.ToLower(rel)
		score := 0.0
		if i := strings.Index(base, lpat); i >= 0 {
			score = 1.0 - float64(i)/float64(len(base)+1) - float64(len(base))/4096.0
		} else if i := strings.Index(lrel, lpat); i >= 0 {
			score = 0.4 - float64(i)/float64(len(lrel)+1) - float64(len(lrel))/4096.0
		}
		out = append(out, fsSearchMatch{Path: rel, Score: score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// --- grep ----------------------------------------------------------------

type fsGrepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Text string `json:"text"`
}

type fsGrepResponse struct {
	Matches   []fsGrepMatch `json:"matches"`
	ElapsedMs int64         `json:"elapsed_ms"`
	Truncated bool          `json:"truncated,omitempty"`
}

func handleFsGrep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if _, err := exec.LookPath("rg"); err != nil {
		httpErr(w, http.StatusServiceUnavailable, "ripgrep_not_installed")
		return
	}
	q := r.URL.Query()
	pattern := strings.TrimSpace(q.Get("q"))
	if pattern == "" {
		J(w, fsGrepResponse{Matches: []fsGrepMatch{}})
		return
	}
	limit := 200
	if v, _ := strconv.Atoi(q.Get("limit")); v > 0 && v <= 1000 {
		limit = v
	}
	caseSensitive := q.Get("case") == "1"
	useRegex := q.Get("regex") == "1"

	abs, workspace, err := fsResolve(r, q.Get("dir"))
	if err != nil {
		fsErr(w, err)
		return
	}

	args := []string{"--json", "--max-count", "10", "--no-messages",
		"--glob", "!.git", "--glob", "!node_modules"}
	if !caseSensitive {
		args = append(args, "--ignore-case")
	}
	if !useRegex {
		args = append(args, "--fixed-strings")
	}
	args = append(args, "--", pattern, abs)

	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fsErr(w, err)
		return
	}
	if err := cmd.Start(); err != nil {
		fsErr(w, err)
		return
	}
	out := make([]fsGrepMatch, 0, 64)
	truncated := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				LineNumber int `json:"line_number"`
				Lines      struct {
					Text string `json:"text"`
				} `json:"lines"`
				Submatches []struct {
					Start int `json:"start"`
					End   int `json:"end"`
				} `json:"submatches"`
			} `json:"data"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "match" {
			continue
		}
		rel, relErr := filepath.Rel(workspace, ev.Data.Path.Text)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		col := 0
		if len(ev.Data.Submatches) > 0 {
			col = ev.Data.Submatches[0].Start + 1
		}
		text := strings.TrimRight(ev.Data.Lines.Text, "\n")
		if len(text) > 400 {
			text = text[:400] + "…"
		}
		out = append(out, fsGrepMatch{
			Path: filepath.ToSlash(rel),
			Line: ev.Data.LineNumber,
			Col:  col,
			Text: text,
		})
		if len(out) >= limit {
			truncated = true
			break
		}
	}
	// Drain remaining output if we broke early so rg doesn't block on
	// a full pipe; then kill it.
	if truncated {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()

	J(w, fsGrepResponse{
		Matches:   out,
		ElapsedMs: time.Since(start).Milliseconds(),
		Truncated: truncated,
	})
}

// --- diff ----------------------------------------------------------------

type fsDiffMtimeResponse struct {
	A    string `json:"a"`
	B    string `json:"b"`
	Mode string `json:"mode"`
}

func handleFsDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpErr(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	q := r.URL.Query()
	base := q.Get("base")
	if base == "" {
		base = "head"
	}
	abs, workspace, err := fsResolve(r, q.Get("path"))
	if err != nil {
		fsErr(w, err)
		return
	}
	rel, relErr := filepath.Rel(workspace, abs)
	if relErr != nil {
		fsErr(w, errPathOutsideWorkspace)
		return
	}
	rel = filepath.ToSlash(rel)

	switch base {
	case "head", "index":
		// Try a `git show` for the base revision; on any git error return
		// {a:"",b:current} so the frontend renders an "all added" diff.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		rev := "HEAD"
		if base == "index" {
			rev = ":0"
		}
		spec := rev + ":" + rel
		if base == "index" {
			spec = ":" + rel
		}
		cmd := exec.CommandContext(ctx, "git", "-C", workspace, "show", spec)
		baseOut, baseErr := cmd.Output()
		curOut, _ := readFileLimited(abs, fsReadMaxBytes())
		if baseErr != nil {
			baseOut = nil
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fsDiffMtimeResponse{
			A:    string(baseOut),
			B:    string(curOut),
			Mode: base,
		})
	case "mtime":
		// For mtime mode the frontend supplies the buffer; we just return
		// the on-disk content as "a".
		cur, _ := readFileLimited(abs, fsReadMaxBytes())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fsDiffMtimeResponse{A: string(cur), B: "", Mode: "mtime"})
	default:
		httpErr(w, http.StatusBadRequest, "unknown_base")
	}
}

func readFileLimited(path string, max int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > max {
		return nil, errFileTooLarge
	}
	return os.ReadFile(path)
}
