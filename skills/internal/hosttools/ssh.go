package hosttools

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (e *Env) runSSH(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		fmt.Fprintln(e.Stdout, "Usage: ssh-list [--short]")
		fmt.Fprintln(e.Stdout, "  List all Host entries from ~/.ssh/config")
		fmt.Fprintln(e.Stdout, "  --short   Print alias names only (one per line)")
		return nil
	}
	switch args[0] {
	case "list":
		return e.runSSHList(args[1:])
	default:
		fmt.Fprintln(e.Stderr, "Usage: ssh-list list [--short]")
		return fmt.Errorf("unknown ssh subcommand: %s", args[0])
	}
}

func (e *Env) runSSHList(args []string) error {
	short := len(args) > 0 && args[0] == "--short"

	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, ".ssh", "config")

	hosts, err := parseSSHConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("read ~/.ssh/config: %w", err)
	}
	if len(hosts) == 0 {
		fmt.Fprintln(e.Stdout, "(no Host entries found in ~/.ssh/config)")
		return nil
	}
	for _, h := range hosts {
		if short {
			fmt.Fprintln(e.Stdout, h.alias)
			continue
		}
		parts := []string{h.alias}
		if h.hostname != "" && h.hostname != h.alias {
			parts = append(parts, "→ "+h.hostname)
		}
		if h.user != "" {
			parts = append(parts, "user="+h.user)
		}
		if h.port != "" && h.port != "22" {
			parts = append(parts, "port="+h.port)
		}
		if h.identityFile != "" {
			parts = append(parts, "key="+h.identityFile)
		}
		fmt.Fprintln(e.Stdout, strings.Join(parts, "  "))
	}
	return nil
}

type sshHostEntry struct {
	alias        string
	hostname     string
	user         string
	port         string
	identityFile string
}

// parseSSHConfig reads ~/.ssh/config and extracts Host entries.
// It follows Include directives one level deep.
// Wildcard-only hosts (*, ?) are skipped.
func parseSSHConfig(path string) ([]sshHostEntry, error) {
	return parseSSHConfigFile(path, true)
}

func parseSSHConfigFile(path string, followIncludes bool) ([]sshHostEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var hosts []sshHostEntry
	var cur *sshHostEntry

	save := func() {
		if cur != nil {
			hosts = append(hosts, *cur)
			cur = nil
		}
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		key := strings.ToLower(parts[0])
		val := strings.TrimSpace(parts[1])

		if key == "include" && followIncludes {
			save()
			expanded := expandTilde(val)
			// glob support
			matches, _ := filepath.Glob(expanded)
			for _, m := range matches {
				sub, _ := parseSSHConfigFile(m, false)
				hosts = append(hosts, sub...)
			}
			continue
		}

		if key == "host" {
			save()
			// skip pure-wildcard patterns
			if isWildcardOnly(val) {
				cur = nil
				continue
			}
			cur = &sshHostEntry{alias: val}
			continue
		}

		if cur == nil {
			continue
		}
		switch key {
		case "hostname":
			cur.hostname = val
		case "user":
			cur.user = val
		case "port":
			cur.port = val
		case "identityfile":
			cur.identityFile = val
		}
	}
	save()
	return hosts, scanner.Err()
}

func isWildcardOnly(s string) bool {
	for _, c := range s {
		if c != '*' && c != '?' && c != ' ' {
			return false
		}
	}
	return true
}

func expandTilde(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
