package audit

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	machineIDOnce  sync.Once
	machineIDValue string
	machineIDErr   error
)

// MachineID returns this host's persistent machine_id, generating one if
// missing. Stored at <auditRoot>/machine_id. Computed once per process.
func MachineID(auditRoot string) (string, error) {
	machineIDOnce.Do(func() {
		machineIDValue, machineIDErr = loadOrCreateMachineID(auditRoot)
	})
	return machineIDValue, machineIDErr
}

func loadOrCreateMachineID(auditRoot string) (string, error) {
	if err := os.MkdirAll(auditRoot, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(auditRoot, "machine_id")
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := "host_" + hex.EncodeToString(b)
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}
