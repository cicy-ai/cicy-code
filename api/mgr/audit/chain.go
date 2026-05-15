package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ChainGenesis is the prev_hash value for the very first event in a chain.
const ChainGenesis = "sha256:GENESIS"

// ChainState is persisted alongside each NDJSON file as <name>.state.
type ChainState struct {
	LastHash    string `json:"last_hash"`
	LastEventID string `json:"last_event_id"`
	Count       int64  `json:"count"`
}

// Chain manages prev_hash/self_hash linking for one append-only NDJSON file.
// Independent chains (per-agent file, global index file) each get their own
// Chain instance.
type Chain struct {
	statePath string

	mu    sync.Mutex
	state ChainState
}

// NewChain loads existing state from disk, initializing to Genesis if absent.
func NewChain(statePath string) (*Chain, error) {
	c := &Chain{statePath: statePath}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(statePath)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &c.state); err != nil {
			return nil, fmt.Errorf("audit: parse chain state %s: %w", statePath, err)
		}
		if c.state.LastHash == "" {
			c.state.LastHash = ChainGenesis
		}
	case os.IsNotExist(err):
		c.state = ChainState{LastHash: ChainGenesis}
	default:
		return nil, err
	}
	return c, nil
}

// Link sets prev_hash on e, computes self_hash, and persists the new tail
// to disk. Returns the linked event so the caller can write it out.
//
// Persistence order is: (1) compute hash, (2) write state file atomically.
// The caller then writes the linked event to its NDJSON file. If the NDJSON
// append fails after the state file is updated, the chain skips that ID on
// next boot — startup verify detects it and the operator can investigate.
func (c *Chain) Link(e Event) (Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e.PrevHash = c.state.LastHash
	h, err := ComputeSelfHash(e)
	if err != nil {
		return e, err
	}
	e.SelfHash = h

	next := ChainState{
		LastHash:    h,
		LastEventID: e.ID,
		Count:       c.state.Count + 1,
	}
	if err := writeJSONAtomic(c.statePath, next); err != nil {
		return e, err
	}
	c.state = next
	return e, nil
}

// Snapshot returns a copy of current chain state (lock-safe).
func (c *Chain) Snapshot() ChainState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
