package mitm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// CA trust consent (compliance §1.3/§1.4): installing the MITM root CA into the
// OS trust store modifies the system trust anchors, so it is gated behind an
// EXPLICIT, recorded user opt-in — never silent on first boot. The flag lives in
// its own file (not the operator-owned config.json, which we never clobber) so
// it can carry consent metadata and be toggled by install-ca / the desktop
// consent card without racing the config. All three platforms share this gate.
//
// Container escape hatch: a Linux image may bake CICY_CA_TRUST_CONSENT=1 to
// represent deploy-time consent (codex/kiro need trust out of the box), so the
// container keeps working while interactive desktops still require an in-app
// opt-in. Checked by CATrustConsented.

// CATrustConsent is the on-disk record written when the user opts in.
type CATrustConsent struct {
	Consent   bool   `json:"consent"`
	GrantedAt string `json:"granted_at,omitempty"` // RFC3339; stamped by caller
	Source    string `json:"source,omitempty"`     // "desktop" | "cli" | "env"
}

// ConsentPath returns ~/cicy-ai/mitm/ca-trust-consent.json.
func ConsentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cicy-ai", "mitm", "ca-trust-consent.json"), nil
}

// CATrustConsented reports whether the user has opted in to OS-trust install,
// either via the recorded flag or the container env hatch.
func CATrustConsented() bool {
	// Container escape hatch (compliance §1.5): honored ONLY inside a Linux
	// container — the deployer represents deploy-time consent there. Desktops
	// (Windows/macOS/Linux-desktop) MUST go through the in-app consent card even
	// if the env is set, so this can never become a desktop bypass. We gate on
	// the Docker marker /.dockerenv (the runtime image is Docker), not GOOS
	// alone, since Linux-desktop is also GOOS=linux.
	if os.Getenv("CICY_CA_TRUST_CONSENT") == "1" && runtime.GOOS == "linux" {
		if _, err := os.Stat("/.dockerenv"); err == nil {
			return true
		}
	}
	path, err := ConsentPath()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var c CATrustConsent
	if json.Unmarshal(b, &c) != nil {
		return false
	}
	return c.Consent
}

// SetCATrustConsent records an opt-in. grantedAt is an RFC3339 timestamp (the
// caller supplies it — time is not taken here so callers stay testable/explicit).
func SetCATrustConsent(grantedAt, source string) error {
	path, err := ConsentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(CATrustConsent{Consent: true, GrantedAt: grantedAt, Source: source}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ClearCATrustConsent revokes the opt-in (used by uninstall-ca / disable). The
// env hatch, if set, still reports consented — that is intentional (container
// policy is deploy-time, not user-revocable from inside).
func ClearCATrustConsent() error {
	path, err := ConsentPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
