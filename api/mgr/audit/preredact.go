package audit

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// preRedactKeyName is the basename of the local AES-256 key file under
// audit root. Generated on first redact, mode 0600. Loss of this key
// makes existing pre-redact archives unrecoverable — intended behavior:
// retention period elapses, key/blobs both retire together.
const preRedactKeyName = ".preredact.key"

var (
	preRedactKeyOnce sync.Once
	preRedactKey     []byte
	preRedactKeyErr  error
)

// loadOrCreatePreRedactKey returns the host-local AES-256 key, generating
// 32 random bytes on first call. Process-cached so subsequent redacts
// don't hit disk for the key.
func loadOrCreatePreRedactKey(auditRoot string) ([]byte, error) {
	preRedactKeyOnce.Do(func() {
		path := filepath.Join(auditRoot, preRedactKeyName)
		if data, err := os.ReadFile(path); err == nil && len(data) == 32 {
			preRedactKey = data
			return
		}
		if err := os.MkdirAll(auditRoot, 0o700); err != nil {
			preRedactKeyErr = err
			return
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			preRedactKeyErr = err
			return
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			preRedactKeyErr = err
			return
		}
		preRedactKey = key
	})
	return preRedactKey, preRedactKeyErr
}

// EncryptPreRedact encrypts the original payload with AES-256-GCM using the
// machine-local key. Output layout: nonce (12 bytes) || ciphertext || tag.
func EncryptPreRedact(auditRoot string, plaintext []byte) ([]byte, error) {
	key, err := loadOrCreatePreRedactKey(auditRoot)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return out, nil
}

// DecryptPreRedact reverses EncryptPreRedact. Returns an error if the file
// is too short or the auth tag fails.
func DecryptPreRedact(auditRoot string, ciphertext []byte) ([]byte, error) {
	key, err := loadOrCreatePreRedactKey(auditRoot)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("audit: pre-redact ciphertext too short")
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}

// SavePreRedact persists the encrypted original payload to
//
//	~/cicy-ai/workers/<agent>/.cicy/history/pre-redact/<event_id>.enc
//
// Directory and file modes 0o700 / 0o600. Returns the meta-stampable
// reference string (e.g. "pre-redact:w-x/evt_abc.enc").
func SavePreRedact(auditRoot, workersRoot, agentID, eventID string, plaintext []byte) (string, error) {
	dir := preRedactDir(workersRoot, agentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	enc, err := EncryptPreRedact(auditRoot, plaintext)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, eventID+".enc")
	if err := os.WriteFile(path, enc, 0o600); err != nil {
		return "", err
	}
	return "pre-redact:" + agentID + "/" + eventID + ".enc", nil
}

func preRedactDir(workersRoot, agentID string) string {
	return filepath.Join(workersRoot, agentID, ".cicy", "history", "pre-redact")
}

// SaveSnapshot persists a REDACTED, plaintext snapshot of the request to
//
//	~/cicy-ai/workers/<agent>/.cicy/history/snapshots/<event_id>.json
//
// Unlike SavePreRedact (encrypted original, for block/redact), this is the
// already-masked payload — safe to read for later review / compliance without
// re-exposing the secret. Returns the meta-stampable ref
// "snapshot:<agent>/<event_id>.json".
func SaveSnapshot(workersRoot, agentID, eventID string, redacted []byte) (string, error) {
	dir := snapshotDir(workersRoot, agentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, eventID+".json")
	if err := os.WriteFile(path, redacted, 0o600); err != nil {
		return "", err
	}
	return "snapshot:" + agentID + "/" + eventID + ".json", nil
}

func snapshotDir(workersRoot, agentID string) string {
	return filepath.Join(workersRoot, agentID, ".cicy", "history", "snapshots")
}

// ReadSnapshot resolves a "snapshot:<agent>/<event_id>.json" ref to its
// redacted bytes via the process-global pipeline. Rejects any ref that could
// escape the snapshots directory.
func ReadSnapshot(ref string) ([]byte, error) {
	if globalPipeline == nil {
		return nil, fmt.Errorf("audit: pipeline not initialized")
	}
	const prefix = "snapshot:"
	if !strings.HasPrefix(ref, prefix) {
		return nil, fmt.Errorf("audit: bad snapshot ref")
	}
	rest := ref[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return nil, fmt.Errorf("audit: bad snapshot ref")
	}
	agent, file := rest[:slash], rest[slash+1:]
	if !safeRefSegment(agent) || !strings.HasSuffix(file, ".json") || !safeRefSegment(strings.TrimSuffix(file, ".json")) {
		return nil, fmt.Errorf("audit: invalid snapshot ref")
	}
	path := filepath.Join(snapshotDir(globalPipeline.store.workersRoot, agent), file)
	return os.ReadFile(path)
}

// safeRefSegment allows only [A-Za-z0-9_-] — no '/', '.', or path traversal.
func safeRefSegment(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// RedactPayload returns a copy of payload with every span replaced by
// "[REDACTED:<rule_id>]". Spans are processed in descending start order so
// later replacements don't shift earlier ones. Overlapping spans within a
// single rule's spans list are not merged — Phase 3 cut 2 leaves that for
// the scanner if it becomes a problem.
func RedactPayload(payload []byte, findings []Finding) []byte {
	// Build a flat (span, rule) list for stable processing.
	type item struct {
		start, end int
		rule       string
	}
	items := make([]item, 0, 4)
	for _, f := range findings {
		for _, s := range f.Spans {
			if s.Start < 0 || s.End > len(payload) || s.Start >= s.End {
				continue
			}
			items = append(items, item{s.Start, s.End, f.RuleID})
		}
	}
	if len(items) == 0 {
		return append([]byte{}, payload...)
	}
	// Sort by start DESC.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].start > items[j-1].start; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	out := make([]byte, len(payload))
	copy(out, payload)
	for _, it := range items {
		// Skip if a later (earlier-position) splice already swallowed this.
		if it.end > len(out) || it.start >= len(out) {
			continue
		}
		token := []byte("[REDACTED:" + it.rule + "]")
		out = append(out[:it.start], append(token, out[it.end:]...)...)
	}
	return out
}
