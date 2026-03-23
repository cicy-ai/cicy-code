package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SkillUsage struct {
	Skill string `json:"skill"`
	Ts    string `json:"ts"`
	Repo  string `json:"repo"`
}

type SkillStat struct {
	Skill string `json:"skill"`
	Count int    `json:"count"`
	Last  string `json:"last"`
}

type GStackSkill struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// GET /api/gstack/skills — list available skills from ~/.gstack/projects or gstack install dir
func handleGStackSkills(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	// Check common gstack install locations
	dirs := []string{
		filepath.Join(home, ".codex", "skills", "gstack"),
		filepath.Join(home, ".kiro", "agents", "skills", "gstack"),
	}

	skills := []GStackSkill{}
	for _, base := range dirs {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillMd := filepath.Join(base, e.Name(), "SKILL.md")
			if _, err := os.Stat(skillMd); err == nil {
				skills = append(skills, GStackSkill{Name: e.Name(), Path: skillMd})
			}
		}
		if len(skills) > 0 {
			break
		}
	}

	jsonResp(w, map[string]any{"skills": skills})
}

// GET /api/gstack/analytics — read ~/.gstack/analytics/skill-usage.jsonl
func handleGStackAnalytics(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".gstack", "analytics", "skill-usage.jsonl")

	f, err := os.Open(path)
	if err != nil {
		jsonResp(w, map[string]any{"stats": []SkillStat{}, "total": 0})
		return
	}
	defer f.Close()

	counts := map[string]*SkillStat{}
	scanner := bufio.NewScanner(f)
	total := 0
	for scanner.Scan() {
		var u SkillUsage
		if json.Unmarshal(scanner.Bytes(), &u) != nil || u.Skill == "" {
			continue
		}
		total++
		if s, ok := counts[u.Skill]; ok {
			s.Count++
			if u.Ts > s.Last {
				s.Last = u.Ts
			}
		} else {
			counts[u.Skill] = &SkillStat{Skill: u.Skill, Count: 1, Last: u.Ts}
		}
	}

	stats := make([]SkillStat, 0, len(counts))
	for _, s := range counts {
		stats = append(stats, *s)
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].Count > stats[j].Count })

	jsonResp(w, map[string]any{"stats": stats, "total": total})
}

// GET /api/gstack/designs — list design docs from ~/.gstack/projects/
func handleGStackDesigns(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".gstack", "projects")

	type DesignDoc struct {
		Name    string `json:"name"`
		Project string `json:"project"`
		ModTime string `json:"mod_time"`
	}

	docs := []DesignDoc{}
	_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") && strings.Contains(info.Name(), "design") {
			rel, _ := filepath.Rel(base, path)
			parts := strings.SplitN(rel, string(os.PathSeparator), 2)
			project := ""
			if len(parts) == 2 {
				project = parts[0]
			}
			docs = append(docs, DesignDoc{
				Name:    info.Name(),
				Project: project,
				ModTime: info.ModTime().Format(time.RFC3339),
			})
		}
		return nil
	})

	// newest first
	sort.Slice(docs, func(i, j int) bool { return docs[i].ModTime > docs[j].ModTime })
	jsonResp(w, map[string]any{"docs": docs})
}

// POST /api/gstack/run — send skill command to a tmux pane
// Body: { "pane_id": "w-10001", "skill": "review", "repo_path": "/path/to/repo" }
func handleGStackRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PaneID   string `json:"pane_id"`
		Skill    string `json:"skill"`
		RepoPath string `json:"repo_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PaneID == "" || body.Skill == "" {
		httpErr(w, 400, "pane_id and skill required")
		return
	}

	// Sanitize skill name — only allow alphanumeric and hyphens
	for _, c := range body.Skill {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			httpErr(w, 400, "invalid skill name")
			return
		}
	}

	cmd := "kiro-cli chat --skill " + body.Skill
	if body.RepoPath != "" {
		cmd = "cd " + shellQuote(body.RepoPath) + " && " + cmd
	}

	if err := tmuxSendKeys(body.PaneID, cmd); err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	jsonResp(w, map[string]any{"ok": true, "pane_id": body.PaneID, "skill": body.Skill})
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
