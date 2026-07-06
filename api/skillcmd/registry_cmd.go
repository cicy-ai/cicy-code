// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package skillcmd

// registry_cmd.go — `cicy-code skill registry ...` dispatch.
//
//   serve     host a private registry (FS-backed, serves its own assets)
//   publish   pack a local skill dir and store it into the registry
//   add       (client) add a registry source to ~/cicy-ai/registries.json
//   remove    (client) remove a source
//   sources   (client) list configured sources

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const registryUsage = `cicy-code skill registry — host & manage private skill registries

Usage:
  # host side
  cicy-code skill registry serve [--dir <d>] [--port <p>] [--token <t>] [--admin-token <t>] [--public-url <url>]
  cicy-code skill registry publish <skill-dir> [--dir <d>]

  # client side (manage ~/cicy-ai/registries.json)
  cicy-code skill registry add <url> [--name <name>] [--token <token>]
  cicy-code skill registry remove <name-or-url>
  cicy-code skill registry sources [--json]

serve flags:
  --dir          data dir (default ~/cicy-registry-data)
  --port         listen port (default 8787)
  --token        read token; clients must send "Authorization: Bearer <token>"
                 (omit = open, rely on network isolation)
  --admin-token  token for POST /v1/admin/publish + DELETE yank (omit = disabled)
  --public-url   override the origin written into download_url (for reverse proxies)
`

func defaultRegistryDir() string {
	return filepath.Join(homeDir(), "cicy-registry-data")
}

// RunRegistry is invoked from skillcmd.Run when args[0] == "registry".
func RunRegistry(args []string) {
	if len(args) == 0 {
		fmt.Print(registryUsage)
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]
	var err error
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(registryUsage)
		return
	case "serve":
		err = cmdRegistryServe(rest)
	case "publish":
		err = cmdRegistryPublish(rest)
	case "add":
		err = cmdRegistryAdd(rest)
	case "remove", "rm":
		err = cmdRegistryRemove(rest)
	case "sources", "ls":
		err = cmdRegistrySources(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown registry subcommand: %s\n\n", sub)
		fmt.Fprint(os.Stderr, registryUsage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry: %v\n", err)
		os.Exit(1)
	}
}

func cmdRegistryServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dir := fs.String("dir", defaultRegistryDir(), "data dir")
	port := fs.Int("port", 8787, "listen port")
	token := fs.String("token", "", "read token")
	adminToken := fs.String("admin-token", "", "admin token")
	publicURL := fs.String("public-url", "", "override download_url origin")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store := newRegStore(*dir)
	if err := ensureDir(store.skillsRoot()); err != nil {
		return err
	}
	srv := newRegServer(store, *token, *adminToken, *publicURL)
	addr := ":" + strconv.Itoa(*port)

	fmt.Printf("cicy-code private skill registry\n")
	fmt.Printf("  dir:        %s\n", *dir)
	fmt.Printf("  listen:     http://0.0.0.0%s\n", addr)
	fmt.Printf("  read auth:  %s\n", boolWord(*token != "", "required (--token)", "open (no token)"))
	fmt.Printf("  admin:      %s\n", boolWord(*adminToken != "", "enabled", "disabled"))
	fmt.Printf("  skills:     %d\n", len(store.catalog()))
	fmt.Printf("\nshare with teammates:\n")
	fmt.Printf("  cicy-code skill registry add http://<this-host>%s --name <team> --token <token>\n\n", addr)

	return http.ListenAndServe(addr, srv.handler())
}

func cmdRegistryPublish(args []string) error {
	// Manual parse so --dir may appear before OR after the positional dir
	// (Go's flag package stops at the first non-flag argument).
	dir := defaultRegistryDir()
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dir" && i+1 < len(args):
			dir = args[i+1]
			i++
		case strings.HasPrefix(a, "--dir="):
			dir = strings.TrimPrefix(a, "--dir=")
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: cicy-code skill registry publish <skill-dir> [--dir <data-dir>]")
	}
	src, err := filepath.Abs(rest[0])
	if err != nil {
		return err
	}

	m, err := publishToStore(newRegStore(dir), src)
	if err != nil {
		return err
	}

	store := newRegStore(dir)
	fmt.Printf("✓ published %s@%s → %s\n", m.Name, m.Version, store.verDir(m.Name, m.Version))
	fmt.Printf("  size:   %d bytes\n", m.Publish.Size)
	fmt.Printf("  sha256: %s\n", m.Publish.SHA256)
	return nil
}

// publishToStore packs the skill dir at src and writes it into store. Shared
// by the CLI publish command and the in-process API (PublishToDir).
func publishToStore(store *regStore, src string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest.json: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if msg := validateRegManifest(&m); msg != "" {
		return nil, fmt.Errorf("invalid manifest: %s", msg)
	}
	zipBytes, err := packSkill(src, m.Name)
	if err != nil {
		return nil, fmt.Errorf("pack: %w", err)
	}
	// stamp publish metadata; download_url stays empty (filled per-request).
	m.Publish = &ManifestPublish{
		PublishedAt: nowRFC3339(),
		SHA256:      sha256Hex(zipBytes),
		Size:        int64(len(zipBytes)),
		Source:      ManifestSource{Type: "local"},
	}
	files := collectSkillFiles(src, &m)
	if err := store.writeSkill(&m, zipBytes, files); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	return &m, nil
}

// collectSkillFiles reads the doc files referenced by the manifest (or sane
// defaults) so they can be served via /files/:key.
func collectSkillFiles(src string, m *Manifest) map[string]string {
	paths := map[string]string{
		"skill_md": "SKILL.md",
		"help_md":  "references/help.md",
		"tools_md": "references/tools.md",
		"readme":   "README.md",
	}
	if m.Files != nil {
		if m.Files.SkillMD != "" {
			paths["skill_md"] = m.Files.SkillMD
		}
		if m.Files.HelpMD != "" {
			paths["help_md"] = m.Files.HelpMD
		}
		if m.Files.ToolsMD != "" {
			paths["tools_md"] = m.Files.ToolsMD
		}
		if m.Files.Readme != "" {
			paths["readme"] = m.Files.Readme
		}
	}
	out := map[string]string{}
	for key, rel := range paths {
		if b, err := os.ReadFile(filepath.Join(src, rel)); err == nil {
			out[key] = string(b)
		}
	}
	return out
}

func boolWord(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}
