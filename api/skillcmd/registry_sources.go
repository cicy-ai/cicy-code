package skillcmd

// registry_sources.go — client-side registry source list.
//
// A user's client can talk to multiple registries at once: the public
// cicy-code registry plus any number of private (per-team) registries shared
// with them. Sources live in ~/cicy-ai/registries.json:
//
//	[
//	  { "name": "public", "url": "https://skills.cicy-ai.com" },
//	  { "name": "team-a",  "url": "http://team-a-host:8787", "token": "<TOKEN_A>" }
//	]
//
// List/install merge across all sources. On name collisions, later entries win
// (private over public; later-added private over earlier), so the file order is
// the precedence order.

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const publicSourceName = "public"

// validSourceName guards the source name, which is used both as a key in
// registries.json AND as a filesystem path component (~/cicy-ai/skills/team/
// <name>/). Allowing '.', '-', '_' covers host-derived defaults like
// "dev.example.com"; requiring a leading alphanumeric and disallowing '/'
// blocks path traversal (".."/"a/b").
var validSourceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// registrySource is one entry in registries.json.
type registrySource struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Token string `json:"token,omitempty"`
}

func registriesJSONPath() string {
	return filepath.Join(homeDir(), "cicy-ai", "registries.json")
}

// loadSources reads registries.json. Returns nil (no error) if the file is
// absent.
func loadSources() ([]registrySource, error) {
	data, err := os.ReadFile(registriesJSONPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var srcs []registrySource
	if err := json.Unmarshal(data, &srcs); err != nil {
		return nil, fmt.Errorf("parse registries.json: %w", err)
	}
	return srcs, nil
}

func saveSources(srcs []registrySource) error {
	if err := ensureDir(filepath.Dir(registriesJSONPath())); err != nil {
		return err
	}
	b, err := json.MarshalIndent(srcs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(registriesJSONPath(), b, 0o600) // may hold tokens
}

// defaultPublicSource is the always-available public registry.
func defaultPublicSource() registrySource {
	return registrySource{Name: publicSourceName, URL: DefaultRegistry}
}

// effectiveSources returns the registries the client should query, in
// precedence order (later wins on name collision).
//
//   - CICY_SKILLS_REGISTRY set → single source (legacy override), with
//     CICY_SKILLS_REGISTRY_TOKEN as its token. registries.json is ignored.
//   - otherwise → registries.json contents; if absent, just the public source.
func effectiveSources() []registrySource {
	if v := strings.TrimSpace(os.Getenv("CICY_SKILLS_REGISTRY")); v != "" {
		return []registrySource{{
			Name:  "env",
			URL:   v,
			Token: strings.TrimSpace(os.Getenv("CICY_SKILLS_REGISTRY_TOKEN")),
		}}
	}
	srcs, err := loadSources()
	if err != nil || len(srcs) == 0 {
		return []registrySource{defaultPublicSource()}
	}
	return srcs
}

// SourceInfo is the exported view of a configured registry source, for other
// packages (e.g. the mgr marketplace UI) that need to query the same set.
type SourceInfo struct {
	Name  string
	URL   string
	Token string
}

// ClientSources returns the configured registry sources in precedence order
// (later wins on name collision). Mirrors what the CLI uses.
func ClientSources() []SourceInfo {
	srcs := effectiveSources()
	out := make([]SourceInfo, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, SourceInfo{Name: s.Name, URL: s.URL, Token: s.Token})
	}
	return out
}

// AddSource adds (or upserts by name) a registry source in registries.json.
// The public source is seeded first if the file is empty. Returns whether an
// existing same-name entry was replaced. Shared by the CLI and the HTTP API.
func AddSource(name, rawURL, token string) (replaced bool, err error) {
	rawURL = strings.TrimSpace(rawURL)
	// A share link is a single URL with the token embedded as ?token=…, so the
	// subscriber pastes one thing. Extract it (an explicit token arg still wins)
	// and strip it from the stored URL — the client sends it as a Bearer header,
	// never as a query param.
	if rawURL != "" {
		if pu, perr := neturl.Parse(rawURL); perr == nil {
			if q := pu.Query(); q.Get("token") != "" {
				if token == "" {
					token = q.Get("token")
				}
				q.Del("token")
				pu.RawQuery = q.Encode()
				rawURL = pu.String()
			}
		}
	}
	url := strings.TrimRight(rawURL, "/")
	if url == "" {
		return false, fmt.Errorf("url required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = hostLabel(url)
	}
	if name == publicSourceName {
		return false, fmt.Errorf("source name %q is reserved", name)
	}
	if !validSourceName.MatchString(name) {
		return false, fmt.Errorf("invalid source name %q (use letters, digits, '.', '-', '_'; no '/')", name)
	}
	srcs, err := loadSources()
	if err != nil {
		return false, err
	}
	if len(srcs) == 0 {
		srcs = []registrySource{defaultPublicSource()}
	}
	for i := range srcs {
		if srcs[i].Name == name {
			srcs[i] = registrySource{Name: name, URL: url, Token: token}
			replaced = true
			break
		}
	}
	if !replaced {
		srcs = append(srcs, registrySource{Name: name, URL: url, Token: token})
	}
	return replaced, saveSources(srcs)
}

// RemoveSource removes a source by name or URL. Returns whether it was found.
func RemoveSource(key string) (removed bool, err error) {
	key = strings.TrimRight(strings.TrimSpace(key), "/")
	srcs, err := loadSources()
	if err != nil {
		return false, err
	}
	var kept []registrySource
	for _, s := range srcs {
		if s.Name == key || strings.TrimRight(s.URL, "/") == key {
			removed = true
			continue
		}
		kept = append(kept, s)
	}
	if !removed {
		return false, nil
	}
	return true, saveSources(kept)
}

// ── subcommands ──────────────────────────────────────────────────────────────

// cmdRegistryAdd: cicy-code skill registry add <url> [--name <n>] [--token <t>]
func cmdRegistryAdd(args []string) error {
	pos, _ := positional(args)
	if len(pos) == 0 {
		return fmt.Errorf("usage: cicy-code skill registry add <url> [--name <name>] [--token <token>]")
	}
	url := strings.TrimRight(pos[0], "/")
	name := flagValue(args, "--name")
	token := flagValue(args, "--token")

	replaced, err := AddSource(name, url, token)
	if err != nil {
		return err
	}
	if name == "" {
		name = hostLabel(url)
	}
	verb := "added"
	if replaced {
		verb = "updated"
	}
	fmt.Printf("✓ %s registry source %q → %s\n", verb, name, url)
	return nil
}

// cmdRegistryRemove: cicy-code skill registry remove <name-or-url>
func cmdRegistryRemove(args []string) error {
	pos, _ := positional(args)
	if len(pos) == 0 {
		return fmt.Errorf("usage: cicy-code skill registry remove <name-or-url>")
	}
	key := strings.TrimRight(pos[0], "/")
	removed, err := RemoveSource(key)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no registry source matching %q", key)
	}
	fmt.Printf("✓ removed registry source %q\n", key)
	return nil
}

// cmdRegistrySources: cicy-code skill registry sources [--json]
func cmdRegistrySources(args []string) error {
	jsonOut := contains(args, "--json")
	srcs := effectiveSources()
	if jsonOut {
		// redact tokens in JSON output
		type safe struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			HasToken bool   `json:"has_token"`
		}
		out := make([]safe, 0, len(srcs))
		for _, s := range srcs {
			out = append(out, safe{Name: s.Name, URL: s.URL, HasToken: s.Token != ""})
		}
		emitJSON(map[string]interface{}{"ok": true, "data": out})
		return nil
	}
	fmt.Printf("%-16s %-40s %s\n", "NAME", "URL", "TOKEN")
	fmt.Println(strings.Repeat("-", 70))
	for _, s := range srcs {
		tok := "-"
		if s.Token != "" {
			tok = "set"
		}
		fmt.Printf("%-16s %-40s %s\n", s.Name, s.URL, tok)
	}
	return nil
}

// hostLabel derives a short default source name from a URL host.
func hostLabel(url string) string {
	h := url
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, ":/"); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		return "private"
	}
	return h
}
