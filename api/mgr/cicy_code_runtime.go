// Copyright 2026 CiCy AI
//
// Self-heal for the cicy-code version store when a NEW binary runs inside an
// OLD Docker base image.
//
// cicy-code ships via npm and floats ahead of the base image, so the running
// binary is frequently newer than the image it boots in. The image bakes
// /usr/local/bin/cicy-code-update.sh, which decides WHERE each version is
// installed. An old image's copy still installs into the legacy path
// (~/cicy-ai/runtime/cicy-code); new images install into ~/.local/cicy-code.
// The install path must not depend on image age, so at startup the (newer)
// binary makes the location canonical regardless of which updater ran:
//
//  1. Make the legacy path a symlink into ~/.local/cicy-code — so even an old
//     updater's `mkdir -p $legacy/<ver>` lands in the canonical tree (this also
//     migrates a real legacy dir the entrypoint already created THIS boot,
//     before we ran).
//  2. Rewrite the baked cicy-code-update.sh in place when it still targets the
//     legacy path, so later in-container `cicy-code-update` runs use the
//     canonical path natively.
//
// Container-only; a no-op on desktop/dev hosts and idempotent across reboots.

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const legacyUpdaterPath = "/usr/local/bin/cicy-code-update.sh"

// healLegacyCicyCodeRuntime converges the cicy-code version store onto
// ~/.local/cicy-code and patches a stale baked updater. See file header.
func healLegacyCicyCodeRuntime() {
	if !isContainerRuntime() {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	canonical := filepath.Join(home, ".local", "cicy-code")
	legacy := filepath.Join(home, "cicy-ai", "runtime", "cicy-code")

	if err := os.MkdirAll(canonical, 0o755); err != nil {
		log.Printf("[cicy-runtime] mkdir %s failed: %v", canonical, err)
		return
	}
	convergeStorePath(legacy, canonical)
	healBakedUpdater(canonical)
}

// convergeStorePath makes `legacy` resolve to `canonical`: repointing a stale
// symlink, migrating a real dir the old updater left behind, or pre-creating the
// link so a later old-updater run writes straight into the canonical tree.
func convergeStorePath(legacy, canonical string) {
	fi, err := os.Lstat(legacy)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
			log.Printf("[cicy-runtime] mkdir %s failed: %v", filepath.Dir(legacy), err)
			return
		}
		if err := os.Symlink(canonical, legacy); err != nil {
			log.Printf("[cicy-runtime] pre-symlink %s failed: %v", legacy, err)
			return
		}
		log.Printf("[cicy-runtime] pre-linked %s -> %s", legacy, canonical)
		return
	}
	if err != nil {
		log.Printf("[cicy-runtime] lstat %s failed: %v", legacy, err)
		return
	}

	// Already a symlink — ensure it points at canonical.
	if fi.Mode()&os.ModeSymlink != 0 {
		if tgt, _ := os.Readlink(legacy); tgt == canonical {
			return // nothing to do
		}
		if err := os.Remove(legacy); err != nil {
			log.Printf("[cicy-runtime] remove stale link %s failed: %v", legacy, err)
			return
		}
		if err := os.Symlink(canonical, legacy); err != nil {
			log.Printf("[cicy-runtime] relink %s failed: %v", legacy, err)
			return
		}
		log.Printf("[cicy-runtime] repointed %s -> %s", legacy, canonical)
		return
	}

	// Real directory left by an old updater — migrate its versions into the
	// canonical tree, then replace it with a symlink so future installs converge.
	if fi.IsDir() {
		migrateStoreVersions(legacy, canonical)
		backup := legacy + ".bak"
		_ = os.RemoveAll(backup)
		if err := os.Rename(legacy, backup); err != nil {
			log.Printf("[cicy-runtime] backup %s failed: %v", legacy, err)
			return
		}
		if err := os.Symlink(canonical, legacy); err != nil {
			log.Printf("[cicy-runtime] symlink %s -> %s failed: %v", legacy, canonical, err)
			return
		}
		log.Printf("[cicy-runtime] migrated legacy store -> %s (backup %s)", canonical, backup)
	}
}

// migrateStoreVersions copies each version subdir present in legacy but missing
// from canonical. `cp -a` preserves modes and the store's relative bin/ symlink;
// this runs container-only (Linux), so cp is always available.
func migrateStoreVersions(legacy, canonical string) {
	ents, err := os.ReadDir(legacy)
	if err != nil {
		return
	}
	for _, e := range ents {
		dst := filepath.Join(canonical, e.Name())
		if _, err := os.Lstat(dst); err == nil {
			continue // already migrated
		}
		src := filepath.Join(legacy, e.Name())
		if out, err := exec.Command("cp", "-a", src, canonical+string(os.PathSeparator)).CombinedOutput(); err != nil {
			log.Printf("[cicy-runtime] copy %s failed: %v (%s)", src, err, strings.TrimSpace(string(out)))
		}
	}
}

// healBakedUpdater rewrites /usr/local/bin/cicy-code-update.sh when it still
// pins the legacy store path. The baked file lives on the image layer (root
// owned, reverts on container recycle), so this re-patches each boot on old
// images — cheap and idempotent (a no-op once the line already matches).
func healBakedUpdater(canonical string) {
	b, err := os.ReadFile(legacyUpdaterPath)
	if err != nil {
		return // not baked here (dev / non-container)
	}
	s := string(b)
	const oldRT = `RT="$HOME_DIR/cicy-ai/runtime/cicy-code"`
	newRT := `RT="${CICY_CODE_STORE:-$HOME_DIR/.local/cicy-code}"`
	if !strings.Contains(s, oldRT) {
		return // already new (or unrecognized layout) — never clobber
	}
	patched := strings.Replace(s, oldRT, newRT, 1)

	// Prefer a direct write; fall back to sudo for the root-owned baked file.
	if err := os.WriteFile(legacyUpdaterPath, []byte(patched), 0o755); err == nil {
		log.Printf("[cicy-runtime] patched legacy updater: %s -> %s", legacyUpdaterPath, canonical)
		return
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		log.Printf("[cicy-runtime] legacy updater %s needs patch but no write access / sudo", legacyUpdaterPath)
		return
	}
	cmd := exec.Command("sudo", "-n", "tee", legacyUpdaterPath)
	cmd.Stdin = strings.NewReader(patched)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[cicy-runtime] sudo patch of %s failed: %v (%s)", legacyUpdaterPath, err, strings.TrimSpace(string(out)))
		return
	}
	_ = exec.Command("sudo", "-n", "chmod", "0755", legacyUpdaterPath).Run()
	log.Printf("[cicy-runtime] patched legacy updater via sudo: %s -> %s", legacyUpdaterPath, canonical)
}
