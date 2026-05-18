package bundle

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type LinkSpec struct {
	Name   string
	Source string
}

type CommandGroup struct {
	Name     string
	Commands []string
}

var (
	BinaryLinks = []LinkSpec{
		{Name: "cicy-skills", Source: "dist/cicy-skills"},
		{Name: "cicy-skillsd", Source: "dist/cicy-skillsd"},
		{Name: "cicy-hosttools", Source: "dist/cicy-hosttools"},
		{Name: "stt", Source: "dist/stt"},
		{Name: "tts", Source: "dist/tts"},
	}
	HosttoolAliases = []string{
		"agent-chrome",
		"agent-code-server",
		"agent-desktop",
		"agent-webpage",
		"aliyun-cli",
		"cf",
		"cf-tunnel",
		"cf-tunnel-py",
		"cf-tunnel.py",
		"cping",
		"email",
		"eng",
		"gemini-ask",
		"gemini-vision",
		"globalApiToken",
		"google",
		"gpt",
		"gpt-chat",
		"frp-client",
		"frp-server",
		"cicy-mihomo",
		"mysql-exec",
		"tg",
		"cicy-agent",
		"todo",
		"ssh-list",
	}
	ProviderLinks = []LinkSpec{}
	DeprecatedProviderLinks = []string{"gmail"}
	RetiredLocalLinks       = []string{
		"agent-page-ping",
		"aeng-page-exec",
		"ai-desktop-check",
		"check-all",
		"check-projects",
		"electron-mcp-ui-check",
		"fast-api",
		"fast-api-check",
		"grant-trial-credit",
		"ipc-ping",
		"tm",
		"webpage",
		"webpage-ping",
	}
	LegacyLinks = []LinkSpec{
		{Name: "ax-click", Source: "legacy/skills/dev/ax-click"},
		{Name: "ax-tree", Source: "legacy/skills/dev/ax-tree"},
		{Name: "tg-bot-check", Source: "legacy/skills/services/tg-bot-check.sh"},
		{Name: "vphone-ctl", Source: "legacy/skills/dev/vphone-ctl"},
		{Name: "xui", Source: "legacy/skills/dev/xui"},
		// Top-level standalone scripts (Python/Bash) — still being Go-ified per
		// migrations/SKILL_DEV_GUIDE.md. Symlink them onto PATH directly.
		{Name: "proxy_ssh", Source: "proxy_ssh"},
		{Name: "us-spot-dev", Source: "us-spot-dev"},
		{Name: "us-spot-proxy", Source: "us-spot-proxy"},
		{Name: "cicy-master", Source: "cicy-master"},
		{Name: "hk-spot-dev", Source: "hk-spot-dev"},
		{Name: "cicy-todo", Source: "cicy-todo/scripts/cicy-todo"},
	}
	CommandGroups = []CommandGroup{
		{Name: "Core", Commands: []string{"cicy-skills", "cicy-skillsd", "cicy-hosttools", "stt", "tts"}},
		{Name: "Google", Commands: []string{"google"}},
		{Name: "Migrated Host Tools", Commands: append([]string(nil), HosttoolAliases...)},
		{Name: "Legacy Host Scripts", Commands: commandNames(LegacyLinks)},
	}
)

func commandNames(items []LinkSpec) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Name)
	}
	sort.Strings(out)
	return out
}

func DefaultPrivateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", "cicy-ai", "skills")
	}
	return filepath.Join(home, "cicy-ai", "skills")
}

func DefaultGlobalBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".local", "bin")
	}
	return filepath.Join(home, ".local", "bin")
}

func DefaultSkillRootDir() string {
	return filepath.Join(DefaultPrivateDir(), "generated", "skills")
}

func DefaultAgentOutputDir() string {
	return filepath.Join(DefaultPrivateDir(), "generated", "agents")
}

func AllCommands() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, spec := range BinaryLinks {
		seen[spec.Name] = struct{}{}
		out = append(out, spec.Name)
	}
	for _, name := range HosttoolAliases {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, spec := range ProviderLinks {
		if _, ok := seen[spec.Name]; ok {
			continue
		}
		seen[spec.Name] = struct{}{}
		out = append(out, spec.Name)
	}
	for _, spec := range LegacyLinks {
		if _, ok := seen[spec.Name]; ok {
			continue
		}
		seen[spec.Name] = struct{}{}
		out = append(out, spec.Name)
	}
	sort.Strings(out)
	return out
}

// Install symlinks every command directly from <root>/<source> into globalBinDir.
// The cicy-hosttools binary is the multiplex target for all HosttoolAliases.
func Install(root, globalBinDir string) error {
	if root == "" {
		return fmt.Errorf("project root is required")
	}
	if globalBinDir == "" {
		globalBinDir = DefaultGlobalBinDir()
	}
	if err := os.MkdirAll(globalBinDir, 0o755); err != nil {
		return err
	}

	for _, spec := range BinaryLinks {
		if err := forceSymlink(filepath.Join(root, filepath.FromSlash(spec.Source)), filepath.Join(globalBinDir, spec.Name)); err != nil {
			return err
		}
	}
	hosttoolTarget := filepath.Join(root, "dist", "cicy-hosttools")
	for _, name := range HosttoolAliases {
		if err := forceSymlink(hosttoolTarget, filepath.Join(globalBinDir, name)); err != nil {
			return err
		}
	}
	for _, spec := range ProviderLinks {
		if err := forceSymlink(filepath.Join(root, filepath.FromSlash(spec.Source)), filepath.Join(globalBinDir, spec.Name)); err != nil {
			return err
		}
	}
	for _, spec := range LegacyLinks {
		if err := forceSymlink(filepath.Join(root, filepath.FromSlash(spec.Source)), filepath.Join(globalBinDir, spec.Name)); err != nil {
			return err
		}
	}
	for _, name := range DeprecatedProviderLinks {
		if err := removeLink(filepath.Join(globalBinDir, name)); err != nil {
			return err
		}
	}
	if err := removeLinks(globalBinDir, RetiredLocalLinks); err != nil {
		return err
	}
	return nil
}

func InstallProvider(root, globalBinDir string) error {
	if root == "" {
		return fmt.Errorf("project root is required")
	}
	if globalBinDir == "" {
		globalBinDir = DefaultGlobalBinDir()
	}
	if err := os.MkdirAll(globalBinDir, 0o755); err != nil {
		return err
	}
	for _, spec := range ProviderLinks {
		if err := forceSymlink(filepath.Join(root, filepath.FromSlash(spec.Source)), filepath.Join(globalBinDir, spec.Name)); err != nil {
			return err
		}
	}
	for _, name := range DeprecatedProviderLinks {
		if err := removeLink(filepath.Join(globalBinDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func Remove(globalBinDir string) error {
	if globalBinDir == "" {
		globalBinDir = DefaultGlobalBinDir()
	}
	for _, name := range AllCommands() {
		if err := removeLink(filepath.Join(globalBinDir, name)); err != nil {
			return err
		}
	}
	for _, name := range DeprecatedProviderLinks {
		if err := removeLink(filepath.Join(globalBinDir, name)); err != nil {
			return err
		}
	}
	if err := removeLinks(globalBinDir, RetiredLocalLinks); err != nil {
		return err
	}
	return nil
}

func RemoveProvider(globalBinDir string) error {
	if globalBinDir == "" {
		globalBinDir = DefaultGlobalBinDir()
	}
	for _, spec := range ProviderLinks {
		if err := removeLink(filepath.Join(globalBinDir, spec.Name)); err != nil {
			return err
		}
	}
	for _, name := range DeprecatedProviderLinks {
		if err := removeLink(filepath.Join(globalBinDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func forceSymlink(target, link string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	_ = os.Remove(link)
	return os.Symlink(target, link)
}

func removeLink(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func removeLinks(dir string, names []string) error {
	for _, name := range names {
		if err := removeLink(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func SyncSkillTree(root, target string) error {
	source := filepath.Join(root, "legacy", "skills")
	return syncTree(source, target, map[string]bool{"bin": true, "__pycache__": true})
}

func syncTree(source, target string, skipDirs map[string]bool) error {
	if samePath(source, target) {
		return nil
	}
	if _, err := os.Stat(source); err != nil {
		if _, targetErr := os.Stat(target); targetErr == nil {
			return nil
		}
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(target, 0o755)
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(target, rel), 0o755)
		}
		if d.Name() == "link-skills.sh" {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return copySymlink(path, filepath.Join(target, rel))
		}
		return copyFile(path, filepath.Join(target, rel))
	})
}

func copyFile(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, target)
}

func copySymlink(source, target string) error {
	value, err := os.Readlink(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	_ = os.Remove(target)
	return os.Symlink(value, target)
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// EnsurePathExports makes sure binDir is in $PATH for future shell sessions.
// If binDir is already in the current $PATH, this is a no-op.
// Otherwise it appends a marker-guarded export line to ~/.profile and ~/.zshrc
// (creating them if they don't exist). Returns the files that were modified.
func EnsurePathExports(binDir string) ([]string, error) {
	if strings.TrimSpace(binDir) == "" {
		return nil, nil
	}
	if pathContains(os.Getenv("PATH"), binDir) {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	exportLine := fmt.Sprintf(`export PATH="%s:$PATH"`, binDir)
	marker := "# cicy-skills: ensure local bin on PATH"
	block := "\n" + marker + "\n" + exportLine + "\n"

	var modified []string
	for _, name := range []string{".profile", ".zshrc"} {
		p := filepath.Join(home, name)
		updated, err := appendIfMissing(p, marker, block)
		if err != nil {
			return modified, err
		}
		if updated {
			modified = append(modified, p)
		}
	}
	return modified, nil
}

func pathContains(pathEnv, dir string) bool {
	if dir == "" {
		return false
	}
	clean := filepath.Clean(dir)
	for _, p := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		if p == "" {
			continue
		}
		if filepath.Clean(p) == clean {
			return true
		}
	}
	return false
}

func appendIfMissing(path, marker, block string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if strings.Contains(string(data), marker) {
		return false, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.WriteString(block); err != nil {
		return false, err
	}
	return true, nil
}
