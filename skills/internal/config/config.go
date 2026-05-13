package config

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Listen           string   `json:"listen"`
	AuthToken        string   `json:"auth_token"`
	SkillRoots       []string `json:"skill_roots"`
	AgentProfilesDir string   `json:"agent_profiles_dir"`
	GeneratedDir     string   `json:"generated_dir"`
	DefaultNode      string   `json:"default_node"`
	Nodes            []Node   `json:"nodes"`
}

type Node struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Token   string `json:"token,omitempty"`
}

type PublicConfig struct {
	Listen           string       `json:"listen"`
	AuthEnabled      bool         `json:"auth_enabled"`
	SkillRoots       []string     `json:"skill_roots"`
	AgentProfilesDir string       `json:"agent_profiles_dir"`
	GeneratedDir     string       `json:"generated_dir"`
	DefaultNode      string       `json:"default_node"`
	Nodes            []PublicNode `json:"nodes"`
}

type PublicNode struct {
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	HasToken   bool   `json:"has_token"`
	IsDefault  bool   `json:"is_default"`
	IsLoopback bool   `json:"is_loopback"`
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/cicy-ai/skills/config.json"
	}
	return filepath.Join(home, "cicy-ai", "skills", "config.json")
}

func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return Config{
		Listen:           "127.0.0.1:7811",
		AuthToken:        "",
		SkillRoots:       []string{filepath.Join(home, "projects", "cicy-skills", "legacy", "skills")},
		AgentProfilesDir: filepath.Join(home, ".codex", "skills"),
		GeneratedDir:     "",
		DefaultNode:      "local",
		Nodes: []Node{
			{
				Name:    "local",
				BaseURL: "http://127.0.0.1:7811",
			},
		},
	}
}

func ExpandPath(path string) string {
	if path == "" {
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func Normalize(cfg Config) Config {
	if cfg.Listen == "" {
		cfg.Listen = Default().Listen
	}
	cfg.AuthToken = strings.TrimSpace(cfg.AuthToken)
	if len(cfg.SkillRoots) == 0 {
		cfg.SkillRoots = Default().SkillRoots
	}
	if cfg.AgentProfilesDir == "" {
		cfg.AgentProfilesDir = Default().AgentProfilesDir
	}
	if cfg.GeneratedDir == "" {
		cfg.GeneratedDir = Default().GeneratedDir
	}
	if cfg.DefaultNode == "" {
		cfg.DefaultNode = Default().DefaultNode
	}
	for i := range cfg.SkillRoots {
		cfg.SkillRoots[i] = ExpandPath(cfg.SkillRoots[i])
	}
	cfg.AgentProfilesDir = ExpandPath(cfg.AgentProfilesDir)
	cfg.GeneratedDir = ExpandPath(cfg.GeneratedDir)
	for i := range cfg.Nodes {
		cfg.Nodes[i].Name = strings.TrimSpace(cfg.Nodes[i].Name)
		cfg.Nodes[i].BaseURL = strings.TrimRight(strings.TrimSpace(cfg.Nodes[i].BaseURL), "/")
		cfg.Nodes[i].Token = strings.TrimSpace(cfg.Nodes[i].Token)
	}
	if len(cfg.Nodes) == 0 {
		cfg.Nodes = []Node{
			{
				Name:    "local",
				BaseURL: "http://" + cfg.Listen,
				Token:   cfg.AuthToken,
			},
		}
	}
	if cfg.DefaultNode == "" && len(cfg.Nodes) > 0 {
		cfg.DefaultNode = cfg.Nodes[0].Name
	}
	for i := range cfg.Nodes {
		if cfg.Nodes[i].Name == "local" && cfg.Nodes[i].BaseURL == "" {
			cfg.Nodes[i].BaseURL = "http://" + cfg.Listen
		}
		if cfg.Nodes[i].Name == "local" && cfg.Nodes[i].Token == "" {
			cfg.Nodes[i].Token = cfg.AuthToken
		}
	}
	return cfg
}

func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultPath()
	}
	path = ExpandPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Normalize(Default()), nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return Normalize(cfg), nil
}

func WriteDefault(path string, overwrite bool) error {
	if path == "" {
		path = DefaultPath()
	}
	path = ExpandPath(path)
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return os.ErrExist
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cfg := Default()
	token, err := GenerateToken()
	if err != nil {
		return err
	}
	cfg.AuthToken = token
	if len(cfg.Nodes) > 0 && cfg.Nodes[0].Name == "local" {
		cfg.Nodes[0].Token = token
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func Save(path string, cfg Config) error {
	if path == "" {
		path = DefaultPath()
	}
	path = ExpandPath(path)
	cfg = Normalize(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func GenerateToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "cskills_" + hex.EncodeToString(buf), nil
}

func MatchToken(expected, provided string) bool {
	expected = strings.TrimSpace(expected)
	provided = strings.TrimSpace(provided)
	if expected == "" {
		return true
	}
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func (cfg Config) Public() PublicConfig {
	out := PublicConfig{
		Listen:           cfg.Listen,
		AuthEnabled:      cfg.AuthToken != "",
		SkillRoots:       append([]string(nil), cfg.SkillRoots...),
		AgentProfilesDir: cfg.AgentProfilesDir,
		GeneratedDir:     cfg.GeneratedDir,
		DefaultNode:      cfg.DefaultNode,
		Nodes:            make([]PublicNode, 0, len(cfg.Nodes)),
	}
	for _, node := range cfg.Nodes {
		out.Nodes = append(out.Nodes, PublicNode{
			Name:       node.Name,
			BaseURL:    node.BaseURL,
			HasToken:   node.Token != "",
			IsDefault:  node.Name == cfg.DefaultNode,
			IsLoopback: strings.Contains(node.BaseURL, "127.0.0.1") || strings.Contains(node.BaseURL, "localhost"),
		})
	}
	return out
}

func (cfg Config) ResolveNode(name string) (Node, error) {
	target := strings.TrimSpace(name)
	if target == "" {
		target = cfg.DefaultNode
	}
	for _, node := range cfg.Nodes {
		if node.Name == target {
			if node.Token == "" && node.Name == "local" {
				node.Token = cfg.AuthToken
			}
			if node.BaseURL == "" {
				node.BaseURL = "http://" + cfg.Listen
			}
			return node, nil
		}
	}
	return Node{}, fmt.Errorf("node not found: %s", target)
}
