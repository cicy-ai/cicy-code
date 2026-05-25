package skillcmd

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// skillDownloadClient is a dedicated http.Client for fetching skill release
// zips. It uses a custom Transport that bypasses HTTP_PROXY for GitHub
// release CDN hosts (github.com + *.githubusercontent.com).
//
// Why: some upstream proxies — notably mihomo's socks5 chain through certain
// transparent CN mirrors — repack the zip while relaying. The byte-level
// content is identical but file mtimes inside the zip get rewritten, which
// changes the sha256 and breaks our publish-time verification. Bypassing the
// proxy for these specific hosts keeps the bytes intact while leaving all
// other outbound traffic going through the user's normal proxy setup.
var skillDownloadClient = &http.Client{
	Timeout: 5 * time.Minute,
	Transport: &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			host := req.URL.Hostname()
			if host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com") {
				return nil, nil
			}
			return http.ProxyFromEnvironment(req)
		},
	},
}

// downloadAndVerify downloads url to cacheZipPath(name, version), verifies
// sha256 if provided, and returns the local path.
func downloadAndVerify(name, version, downloadURL, expectedSHA256 string) (string, error) {
	if err := ensureDir(cacheDir()); err != nil {
		return "", err
	}
	dest := cacheZipPath(name, version)

	// If cached and sha256 matches, reuse.
	if expectedSHA256 != "" {
		if got, _ := fileSHA256(dest); got == expectedSHA256 {
			return dest, nil
		}
	}

	// Some hosts return relative redirects; net/http follows them by default.
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cicy-code/skill-installer")

	resp, err := skillDownloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("download %s: HTTP %s", downloadURL, resp.Status)
	}

	tmp := dest + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	mw := io.MultiWriter(out, hasher)
	if _, err := io.Copy(mw, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("write zip: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA256 != "" && got != expectedSHA256 {
		os.Remove(tmp)
		return "", fmt.Errorf("sha256 mismatch: expected %s, got %s", expectedSHA256, got)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// fileSHA256 computes sha256 of a local file (hex). Returns "" if missing.
func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractZip unzips zipPath into destParent. The archive is expected to
// contain a single top-level directory named <name>/. We strip that prefix
// when extracting so files end up at <destParent>/<name>/...
//
// Returns the path the skill was extracted into (= destParent/<name>).
func extractZip(zipPath, name, destParent string) (string, error) {
	target := filepath.Join(destParent, name)

	// Remove any prior contents (clean install).
	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("clean prior install dir: %w", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	prefix := name + "/"
	for _, f := range zr.File {
		// guard against zip-slip
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return "", fmt.Errorf("unsafe zip entry: %s", f.Name)
		}
		if !strings.HasPrefix(f.Name, prefix) {
			// Skip entries outside the expected prefix.
			continue
		}
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" {
			continue
		}
		out := filepath.Join(target, rel)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(out, f.Mode()|0o100); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return "", err
		}
		w, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return "", err
		}
		rc, err := f.Open()
		if err != nil {
			w.Close()
			return "", err
		}
		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			w.Close()
			return "", err
		}
		rc.Close()
		w.Close()
	}
	return target, nil
}

// runNpmCI runs `npm ci --omit=dev --ignore-scripts` inside dir if a
// package-lock.json is present. Idempotent.
func runNpmCI(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err != nil {
		return nil // nothing to do
	}
	cmd := exec.Command("npm", "ci", "--omit=dev", "--ignore-scripts")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// makeSymlink creates a symlink target → src, replacing any existing entry.
// On systems where symlinks are not supported (rare on Linux/macOS), we copy
// instead (the install workflow currently assumes POSIX hosts).
func makeSymlink(src, target string) error {
	if err := ensureDir(filepath.Dir(target)); err != nil {
		return err
	}
	_ = os.Remove(target) // ignore not-exists
	return os.Symlink(src, target)
}

// chmodExec ensures the path is executable for owner.
func chmodExec(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	return os.Chmod(p, st.Mode()|0o100)
}
