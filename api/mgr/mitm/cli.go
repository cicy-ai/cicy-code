package mitm

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

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
	fmt.Println("OK — cicy-mitm CA installed.")
	fmt.Println("Restart any TLS clients (browser etc.) so they reload trust stores.")
	return 0
}

func runUninstallCA(args []string) int {
	fs := flag.NewFlagSet("uninstall-ca", flag.ContinueOnError)
	scope := fs.String("scope", "both", "system|nss|both")
	nickname := fs.String("nickname", "cicy-mitm", "NSS DB nickname")
	dryRun := fs.Bool("dry-run", false, "print commands without executing")
	if err := fs.Parse(args); err != nil {
		return 2
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
