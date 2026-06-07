package mitm

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// elevatedChildFlag marks a re-launched, already-elevated invocation so it does
// the privileged store write WITHOUT recursing into self-elevation, and without
// writing the consent flag (the elevated child may run as root on mac/linux, a
// different $HOME than the user — the original user-context process records
// consent instead).
const elevatedChildFlag = "--_elevated-child"

// hasElevatedChildFlag reports the marker's presence and returns args with it
// removed (so flag parsing downstream never sees it).
func hasElevatedChildFlag(args []string) (bool, []string) {
	out := args[:0:0]
	found := false
	for _, a := range args {
		if a == elevatedChildFlag {
			found = true
			continue
		}
		out = append(out, a)
	}
	return found, out
}

// runElevatedSelf re-launches `cicy-code mitm <verb> ...` with an OS privilege
// prompt (UAC / polkit / keychain auth) and waits, returning the child's exit
// code. This is the consent card's fallback when the running server is not
// elevated (a process cannot elevate itself in place). The OS prompt doubles as
// the compliance "second consent" (§1.4). User cancels → non-zero + cancelled.
func runElevatedSelf(verb string, passArgs []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	childArgs := append([]string{"mitm", verb}, passArgs...)
	childArgs = append(childArgs, elevatedChildFlag)

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// Start-Process -Verb RunAs raises UAC; -Wait -PassThru lets us read the
		// child's exit code. Args are passed as a PowerShell string array.
		quoted := make([]string, len(childArgs))
		for i, a := range childArgs {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", "''") + "'"
		}
		ps := fmt.Sprintf(
			"$ErrorActionPreference='Stop'; try { $p = Start-Process -FilePath '%s' -ArgumentList %s -Verb RunAs -Wait -PassThru; exit $p.ExitCode } catch { exit 1223 }",
			strings.ReplaceAll(exe, "'", "''"), strings.Join(quoted, ","))
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	case "darwin":
		// osascript shows the GUI admin-auth dialog; cancel → error -128.
		inner := shellJoin(append([]string{exe}, childArgs...))
		script := fmt.Sprintf("do shell script %s with administrator privileges", osaQuote(inner))
		cmd = exec.Command("osascript", "-e", script)
	default: // linux
		// pkexec raises the polkit prompt; cancel/deny → 126/127.
		cmd = exec.Command("pkexec", append([]string{exe}, childArgs...)...)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err == nil {
		return 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), err
	}
	return 1, err
}

// shellJoin quotes args for a POSIX shell (used inside osascript's do-shell-script).
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}

// osaQuote wraps a string as an AppleScript string literal.
func osaQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// RunCLI dispatches `cicy-code mitm <subcommand>` and returns a process
// exit code.
//
//	0 — success
//	2 — invocation error (bad args, missing files, ...)
//	other — passed through from underlying tooling (e.g. certutil)
func RunCLI(args []string) int {
	if len(args) == 0 {
		printCLIUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "install-ca":
		return runInstallCA(args[1:])
	case "uninstall-ca":
		return runUninstallCA(args[1:])
	case "show-ca":
		return runShowCA(args[1:])
	case "help", "-h", "--help":
		printCLIUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown mitm subcommand: %s\n\n", args[0])
		printCLIUsage(os.Stderr)
		return 2
	}
}

func printCLIUsage(w *os.File) {
	fmt.Fprint(w, `Usage: cicy-code mitm <command>

Commands:
  install-ca    Install the cicy-mitm root CA into the OS / NSS trust stores
  uninstall-ca  Remove a previously installed cicy-mitm CA
  show-ca       Print the path of the CA cert + key

install-ca flags:
  --scope=system|nss|both   Where to install (default: both on Linux, system on macOS)
  --cert=<path>             Override CA cert path (default ~/cicy-ai/db/mitm-ca.crt)
  --nickname=<name>         NSS DB nickname (default: cicy-mitm)
  --dry-run                 Print commands that would run without executing

`)
}

// runInstallCA installs the CA into platform-appropriate trust stores.
func runInstallCA(args []string) int {
	isChild, args := hasElevatedChildFlag(args)
	fs := flag.NewFlagSet("install-ca", flag.ContinueOnError)
	scope := fs.String("scope", "", "system|nss|both (default system+nss on linux, system on darwin)")
	certPath := fs.String("cert", "", "CA cert path (default ~/cicy-ai/db/mitm-ca.crt)")
	nickname := fs.String("nickname", "cicy-mitm", "NSS DB nickname")
	dryRun := fs.Bool("dry-run", false, "print commands without executing")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *certPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: resolve home:", err)
			return 2
		}
		*certPath = filepath.Join(home, "cicy-ai", "db", "mitm-ca.crt")
	}
	if _, err := os.Stat(*certPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: cert not found at %s\n", *certPath)
		fmt.Fprintln(os.Stderr, "Hint: start cicy-code with MITM enabled once to auto-generate the CA.")
		return 2
	}

	if *scope == "" {
		switch runtime.GOOS {
		case "linux":
			*scope = "both"
		case "darwin":
			*scope = "system"
		default:
			*scope = "system"
		}
	}

	// The system-trust write needs OS privilege. If we're neither elevated nor
	// the elevated child, relaunch ourselves with a UAC/polkit/keychain prompt
	// (the compliance "second consent"). The elevated child does the store write;
	// THIS (user-context) process records consent so the flag lands in the user's
	// home even when the child runs as root. NSS is per-user → no elevation.
	if !*dryRun && !isChild && !isElevated() && (*scope == "system" || *scope == "both") {
		code, err := runElevatedSelf("install-ca", elevatedPassArgs(*scope, *certPath, *nickname))
		if code != 0 {
			fmt.Fprintf(os.Stderr, "error: elevation failed or cancelled (code %d): %v\n", code, err)
			return code
		}
		if *scope == "both" { // child did system; do per-user NSS here, unprivileged
			if rc := installNSS(*certPath, *nickname, false); rc != 0 {
				return rc
			}
		}
		_ = SetCATrustConsent(time.Now().Format(time.RFC3339), "cli")
		fmt.Println("OK — cicy-mitm CA installed.")
		fmt.Println("Restart any TLS clients (browser etc.) so they reload trust stores.")
		return 0
	}

	rc := 0
	switch *scope {
	case "system":
		rc |= installSystem(*certPath, *dryRun)
	case "nss":
		rc |= installNSS(*certPath, *nickname, *dryRun)
	case "both":
		rc |= installSystem(*certPath, *dryRun)
		rc |= installNSS(*certPath, *nickname, *dryRun)
	default:
		fmt.Fprintf(os.Stderr, "error: --scope must be system|nss|both, got %q\n", *scope)
		return 2
	}
	if rc != 0 {
		return rc
	}
	// Record consent in the user-context process only (not the elevated child,
	// whose $HOME may be root's). dry-run records nothing.
	if !isChild && !*dryRun {
		_ = SetCATrustConsent(time.Now().Format(time.RFC3339), "cli")
	}
	fmt.Println("OK — cicy-mitm CA installed.")
	fmt.Println("Restart any TLS clients (browser etc.) so they reload trust stores.")
	return 0
}

// elevatedPassArgs reconstructs the flags to forward to the elevated child so
// it installs the same scope/cert. NSS is per-user, so the child only needs the
// system scope.
func elevatedPassArgs(scope, certPath, nickname string) []string {
	childScope := scope
	if scope == "both" {
		childScope = "system" // NSS handled unprivileged in the parent
	}
	return []string{"--scope=" + childScope, "--cert=" + certPath, "--nickname=" + nickname}
}

func runUninstallCA(args []string) int {
	isChild, args := hasElevatedChildFlag(args)
	fs := flag.NewFlagSet("uninstall-ca", flag.ContinueOnError)
	scope := fs.String("scope", "both", "system|nss|both")
	nickname := fs.String("nickname", "cicy-mitm", "NSS DB nickname")
	dryRun := fs.Bool("dry-run", false, "print commands without executing")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if !*dryRun && !isChild && !isElevated() && (*scope == "system" || *scope == "both") {
		code, err := runElevatedSelf("uninstall-ca", []string{"--scope=" + (map[bool]string{true: "system", false: *scope}[*scope == "both"]), "--nickname=" + *nickname})
		if code != 0 {
			fmt.Fprintf(os.Stderr, "error: elevation failed or cancelled (code %d): %v\n", code, err)
			return code
		}
		if *scope == "both" {
			_ = uninstallNSS(*nickname, false)
		}
		_ = ClearCATrustConsent()
		return 0
	}

	rc := 0
	switch *scope {
	case "system":
		rc |= uninstallSystem(*dryRun)
	case "nss":
		rc |= uninstallNSS(*nickname, *dryRun)
	case "both":
		rc |= uninstallSystem(*dryRun)
		rc |= uninstallNSS(*nickname, *dryRun)
	default:
		fmt.Fprintf(os.Stderr, "error: --scope must be system|nss|both, got %q\n", *scope)
		return 2
	}
	if rc == 0 && !isChild && !*dryRun {
		_ = ClearCATrustConsent()
	}
	return rc
}

func runShowCA(_ []string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	certPath := filepath.Join(home, "cicy-ai", "db", "mitm-ca.crt")
	keyPath := filepath.Join(home, "cicy-ai", "db", "mitm-ca.key")
	fmt.Printf("cert: %s\n", certPath)
	fmt.Printf("key:  %s\n", keyPath)
	if _, err := os.Stat(certPath); err == nil {
		fmt.Println("status: present")
	} else {
		fmt.Println("status: missing (start cicy-code with MITM enabled to auto-generate)")
	}
	return 0
}

// --- platform install helpers ---

// installSystem installs into the OS-wide trust store. Requires sudo on
// Linux + macOS. We do NOT prepend sudo ourselves — fail clearly instead,
// so the operator decides whether to re-run with sudo.
func installSystem(certPath string, dryRun bool) int {
	switch runtime.GOOS {
	case "linux":
		dst := "/usr/local/share/ca-certificates/cicy-mitm.crt"
		fmt.Printf("[system] install %s → %s\n", certPath, dst)
		if dryRun {
			fmt.Println("[dry-run] would cp + update-ca-certificates")
			return 0
		}
		if err := copyFileRoot(certPath, dst); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			fmt.Fprintln(os.Stderr, "hint: re-run with sudo, or use --scope=nss (Chrome/Firefox only)")
			return 1
		}
		out, err := exec.Command("update-ca-certificates").CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: update-ca-certificates: %v\n%s\n", err, out)
			return 1
		}
		fmt.Println(string(out))
		return 0
	case "darwin":
		fmt.Printf("[system] adding %s to System.keychain\n", certPath)
		if dryRun {
			fmt.Println("[dry-run] would security add-trusted-cert ...")
			return 0
		}
		cmd := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot",
			"-k", "/Library/Keychains/System.keychain", certPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "error: security: %v (try sudo)\n", err)
			return 1
		}
		return 0
	case "windows":
		// LocalMachine\ROOT via the CryptoAPI (shared with the server endpoint).
		fmt.Printf("[system] adding %s to LocalMachine\\Root\n", certPath)
		if dryRun {
			fmt.Println("[dry-run] would add cert to LocalMachine\\ROOT via CryptoAPI")
			return 0
		}
		certBytes, err := os.ReadFile(certPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: read cert: %v\n", err)
			return 1
		}
		if err := InstallRootCA(certBytes); err != nil {
			fmt.Fprintf(os.Stderr, "error: install to LocalMachine\\ROOT: %v\n", err)
			fmt.Fprintln(os.Stderr, "hint: run from an elevated (Administrator) console.")
			return 1
		}
		fmt.Printf("[system] installed (thumbprint %s)\n", CertThumbprint(certBytes))
		return 0
	default:
		fmt.Fprintf(os.Stderr, "system trust install not implemented on %s; use --scope=nss\n", runtime.GOOS)
		return 1
	}
}

func installNSS(certPath, nickname string, dryRun bool) int {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "[nss] only relevant on Linux Chrome/Firefox; skipping")
		return 0
	}
	if _, err := exec.LookPath("certutil"); err != nil {
		fmt.Fprintln(os.Stderr, "[nss] certutil not found — install with: apt install libnss3-tools")
		return 1
	}
	home, _ := os.UserHomeDir()
	nssDir := filepath.Join(home, ".pki", "nssdb")
	if _, err := os.Stat(nssDir); err != nil {
		fmt.Fprintf(os.Stderr, "[nss] %s missing — open Chrome once to create it, then retry\n", nssDir)
		return 1
	}
	cmd := exec.Command("certutil",
		"-d", "sql:"+nssDir,
		"-A", "-t", "C,,",
		"-n", nickname,
		"-i", certPath,
	)
	fmt.Printf("[nss] %s\n", cmd.String())
	if dryRun {
		return 0
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[nss] certutil: %v\n%s\n", err, out)
		return 1
	}
	return 0
}

func uninstallSystem(dryRun bool) int {
	switch runtime.GOOS {
	case "linux":
		dst := "/usr/local/share/ca-certificates/cicy-mitm.crt"
		fmt.Printf("[system] remove %s\n", dst)
		if dryRun {
			return 0
		}
		if err := removeFileRoot(dst); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		out, _ := exec.Command("update-ca-certificates", "--fresh").CombinedOutput()
		fmt.Println(string(out))
		return 0
	case "windows":
		fmt.Println("[system] removing cicy-mitm CA from LocalMachine\\Root")
		if dryRun {
			fmt.Println("[dry-run] would remove cert from LocalMachine\\ROOT via CryptoAPI")
			return 0
		}
		home, _ := os.UserHomeDir()
		certBytes, err := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "mitm-ca.crt"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: read cert: %v\n", err)
			return 1
		}
		if err := RemoveRootCA(certBytes); err != nil {
			fmt.Fprintf(os.Stderr, "error: remove from LocalMachine\\ROOT: %v\n", err)
			fmt.Fprintln(os.Stderr, "hint: run from an elevated (Administrator) console.")
			return 1
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "system trust uninstall not implemented on %s\n", runtime.GOOS)
		return 1
	}
}

func uninstallNSS(nickname string, dryRun bool) int {
	if runtime.GOOS != "linux" {
		return 0
	}
	home, _ := os.UserHomeDir()
	nssDir := filepath.Join(home, ".pki", "nssdb")
	cmd := exec.Command("certutil", "-d", "sql:"+nssDir, "-D", "-n", nickname)
	fmt.Printf("[nss] %s\n", cmd.String())
	if dryRun {
		return 0
	}
	out, _ := cmd.CombinedOutput()
	fmt.Println(string(out))
	return 0
}

// copyFileRoot writes src to dst. If dst's parent requires root, callers
// will get an error and a "re-run with sudo" hint at the call site.
func copyFileRoot(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func removeFileRoot(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
