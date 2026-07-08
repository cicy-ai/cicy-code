package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// evalTempDir returns t.TempDir() with symlinks resolved. On macOS t.TempDir()
// is /var/folders/… which is a symlink to /private/var/…; resolveSafePath
// EvalSymlinks the workspace root and would otherwise flag every path as escaping
// (path_symlink_escape). Linux /tmp is not a symlink so this is a no-op there.
func evalTempDir(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("evalsymlinks tempdir: %v", err)
	}
	return real
}

func TestResolveSafePath_EmptyReturnsWorkspace(t *testing.T) {
	ws := evalTempDir(t)
	for _, in := range []string{"", ".", "./"} {
		got, err := resolveSafePath(ws, in)
		if err != nil {
			t.Fatalf("resolveSafePath(%q) err=%v", in, err)
		}
		if got != ws {
			t.Fatalf("resolveSafePath(%q) = %q, want %q", in, got, ws)
		}
	}
}

func TestResolveSafePath_AbsoluteOutsideRejected(t *testing.T) {
	ws := evalTempDir(t)
	// Absolute paths outside the workspace must still be rejected.
	cases := []string{"/etc/passwd", "/"}
	for _, in := range cases {
		if _, err := resolveSafePath(ws, in); err == nil {
			t.Fatalf("expected error for absolute path %q", in)
		}
	}
}

func TestResolveSafePath_AbsoluteInsideAccepted(t *testing.T) {
	ws := evalTempDir(t)
	if err := os.MkdirAll(filepath.Join(ws, "src"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	abs := filepath.Join(ws, "src", "f.txt")
	got, err := resolveSafePath(ws, abs)
	if err != nil {
		t.Fatalf("absolute path inside workspace rejected: %v", err)
	}
	if got != abs {
		t.Fatalf("got %q, want %q", got, abs)
	}
}

func TestResolveSafePath_RejectsParentEscape(t *testing.T) {
	ws := evalTempDir(t)
	cases := []string{
		"..",
		"../",
		"../etc",
		"a/../../etc",
		"./a/../../b",
	}
	for _, in := range cases {
		if _, err := resolveSafePath(ws, in); err == nil {
			t.Fatalf("expected error for escape %q", in)
		}
	}
}

func TestResolveSafePath_AcceptsInside(t *testing.T) {
	ws := evalTempDir(t)
	if err := os.MkdirAll(filepath.Join(ws, "src", "sub"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "sub", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	cases := map[string]string{
		"src":              filepath.Join(ws, "src"),
		"src/sub":          filepath.Join(ws, "src", "sub"),
		"src/sub/f.txt":    filepath.Join(ws, "src", "sub", "f.txt"),
		"./src/sub/f.txt":  filepath.Join(ws, "src", "sub", "f.txt"),
		"src/./sub/f.txt":  filepath.Join(ws, "src", "sub", "f.txt"),
		"src/a/../sub":     filepath.Join(ws, "src", "sub"),
	}
	for in, want := range cases {
		got, err := resolveSafePath(ws, in)
		if err != nil {
			t.Fatalf("resolveSafePath(%q) err=%v", in, err)
		}
		// EvalSymlinks may canonicalize; compare via filepath.Clean.
		if filepath.Clean(got) != filepath.Clean(want) {
			t.Fatalf("resolveSafePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveSafePath_NewFileUnderExistingParent(t *testing.T) {
	ws := evalTempDir(t)
	if err := os.MkdirAll(filepath.Join(ws, "src"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got, err := resolveSafePath(ws, "src/new-file.txt")
	if err != nil {
		t.Fatalf("resolveSafePath new file err=%v", err)
	}
	if filepath.Clean(got) != filepath.Clean(filepath.Join(ws, "src", "new-file.txt")) {
		t.Fatalf("got %q", got)
	}
}

func TestResolveSafePath_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	ws := evalTempDir(t)
	link := filepath.Join(ws, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := resolveSafePath(ws, "escape/secret.txt"); err == nil {
		t.Fatalf("expected symlink escape rejection")
	}
}

func TestResolveSafePath_AllowsInternalSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	ws := evalTempDir(t)
	target := filepath.Join(ws, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	link := filepath.Join(ws, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got, err := resolveSafePath(ws, "alias/f.txt")
	if err != nil {
		t.Fatalf("internal symlink rejected: %v", err)
	}
	if !strings.HasPrefix(got, ws) {
		t.Fatalf("expected resolved path inside %q, got %q", ws, got)
	}
}

// Deleting a symlink (even one pointing at a directory) must drop the link
// itself, never follow it to the target. resolveSafeLeaf returns the link node
// (unlike resolveSafePath, which EvalSymlinks the leaf to its target).
func TestResolveSafeLeaf_DoesNotFollowLeafSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	ws := evalTempDir(t)
	target := filepath.Join(ws, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	link := filepath.Join(ws, "linkdir")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := resolveSafeLeaf(ws, "linkdir")
	if err != nil {
		t.Fatalf("resolveSafeLeaf: %v", err)
	}
	if got != link {
		t.Fatalf("want link path %q, got %q (it followed the symlink)", link, got)
	}

	// Removing the resolved leaf drops the link and leaves the target intact.
	if err := os.Remove(got); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link still present after delete")
	}
	if _, err := os.Stat(filepath.Join(target, "f.txt")); err != nil {
		t.Fatalf("target directory was destroyed by deleting the link: %v", err)
	}
}

func TestIsProtectedWritePath(t *testing.T) {
	ws := evalTempDir(t)
	cases := map[string]bool{
		filepath.Join(ws, ".git", "HEAD"):                  true,
		filepath.Join(ws, "node_modules", "x", "y.js"):     true,
		filepath.Join(ws, "src", "App.tsx.cicy-tmp"):       true,
		filepath.Join(ws, "src", "App.tsx.cicy-tmp-aabbcc"): true,
		filepath.Join(ws, "src", "App.tsx"):                false,
		filepath.Join(ws, "go.mod"):                        false,
		filepath.Join(ws, "deep", ".git", "x"):             true,
	}
	for abs, want := range cases {
		if got := isProtectedWritePath(ws, abs); got != want {
			t.Fatalf("isProtectedWritePath(%q) = %v, want %v", abs, got, want)
		}
	}
}

func TestNormalizeAgentID(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"  ":              "",
		"w-1001":         "w-1001:main.0",
		"w-1001:main.0":  "w-1001:main.0",
		"w-1001:work.1":  "w-1001:work.1",
		" w-1001 ":       "w-1001:main.0",
	}
	for in, want := range cases {
		if got := normalizeAgentID(in); got != want {
			t.Fatalf("normalizeAgentID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkspaceRel(t *testing.T) {
	ws := evalTempDir(t)
	cases := map[string]string{
		ws:                                "",
		filepath.Join(ws, "a"):            "a",
		filepath.Join(ws, "a", "b", "c"):  "a/b/c",
	}
	for abs, want := range cases {
		if got := workspaceRel(ws, abs); got != want {
			t.Fatalf("workspaceRel(%q) = %q, want %q", abs, got, want)
		}
	}
}
