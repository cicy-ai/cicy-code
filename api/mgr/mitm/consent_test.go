package mitm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Compliance red line (§1.4): no consent recorded → not consented. The OS-trust
// install must never fire on a fresh machine.
func TestCATrustConsent_DefaultDenied(t *testing.T) {
	withTempHome(t)
	t.Setenv("CICY_CA_TRUST_CONSENT", "")
	if CATrustConsented() {
		t.Fatal("fresh machine must report NOT consented")
	}
}

func TestCATrustConsent_SetThenCleared(t *testing.T) {
	withTempHome(t)
	t.Setenv("CICY_CA_TRUST_CONSENT", "")

	if err := SetCATrustConsent("2026-06-07T00:00:00Z", "test"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !CATrustConsented() {
		t.Fatal("after SetCATrustConsent, must report consented")
	}
	if err := ClearCATrustConsent(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if CATrustConsented() {
		t.Fatal("after ClearCATrustConsent, must report NOT consented")
	}
	// Clearing again is a no-op, not an error.
	if err := ClearCATrustConsent(); err != nil {
		t.Fatalf("second clear should be no-op: %v", err)
	}
}

// The container escape hatch must NOT become a desktop bypass: even with the env
// set, a non-container host (no /.dockerenv) requires the in-app opt-in.
func TestCATrustConsent_EnvHatchNotADesktopBypass(t *testing.T) {
	withTempHome(t)
	t.Setenv("CICY_CA_TRUST_CONSENT", "1")

	_, dockerErr := os.Stat("/.dockerenv")
	inLinuxContainer := runtime.GOOS == "linux" && dockerErr == nil
	if got := CATrustConsented(); got != inLinuxContainer {
		t.Fatalf("env hatch: consented=%v but linux-container=%v (must honor env ONLY in a linux container)", got, inLinuxContainer)
	}
}

func withTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// ConsentPath derives from the home dir on every OS we run tests on.
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	} else {
		t.Setenv("HOME", dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cicy-ai", "mitm"), 0o755); err != nil {
		t.Fatal(err)
	}
}
