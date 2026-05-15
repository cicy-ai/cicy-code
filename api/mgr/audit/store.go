package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store handles append-only NDJSON writes:
//   - per-agent: ~/cicy-ai/workers/<agent>/history/audit.ndjson
//   - global index by UTC day: ~/cicy-ai/audit/index/YYYY-MM-DD.ndjson
//
// Each NDJSON has its own independent hash chain. The same event therefore
// carries different (prev_hash, self_hash) pairs in the two files; events
// can be cross-referenced by ID.
type Store struct {
	auditRoot   string
	workersRoot string

	mu          sync.Mutex
	agentChains map[string]*Chain
	indexChain  *Chain
}

// NewStore initializes the directory layout and the global index chain.
// Per-agent chains are created lazily on first event for that agent.
func NewStore(auditRoot, workersRoot string) (*Store, error) {
	if err := os.MkdirAll(auditRoot, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(auditRoot, "index"), 0o700); err != nil {
		return nil, err
	}
	indexChain, err := NewChain(filepath.Join(auditRoot, "chain-state.json"))
	if err != nil {
		return nil, err
	}
	return &Store{
		auditRoot:   auditRoot,
		workersRoot: workersRoot,
		agentChains: map[string]*Chain{},
		indexChain:  indexChain,
	}, nil
}

// agentHistoryDir lives next to the gateway snapshot files at
// ~/cicy-ai/workers/<agent>/.cicy/history/, matching builtinWorkerRuntimeDir
// in api/mgr/paths.go. PayloadRef values like "current.json#turn_xxx" are
// relative to this directory.
func (s *Store) agentHistoryDir(agentID string) string {
	return filepath.Join(s.workersRoot, agentID, ".cicy", "history")
}

func (s *Store) agentAuditPath(agentID string) string {
	return filepath.Join(s.agentHistoryDir(agentID), "audit.ndjson")
}

func (s *Store) indexPathForDay(day time.Time) string {
	return filepath.Join(s.auditRoot, "index", day.UTC().Format("2006-01-02")+".ndjson")
}

func (s *Store) getAgentChain(agentID string) (*Chain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.agentChains[agentID]; ok {
		return ch, nil
	}
	dir := s.agentHistoryDir(agentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ch, err := NewChain(filepath.Join(dir, "audit-chain.state"))
	if err != nil {
		return nil, err
	}
	s.agentChains[agentID] = ch
	return ch, nil
}

// Append links the event into the per-agent chain, writes to the per-agent
// NDJSON, then independently links into the global index chain and writes
// to today's index NDJSON.
//
// Failure semantics: per-agent file is the source of truth. If the global
// index write fails after the agent file succeeds, the error is returned
// but the agent record is intact.
func (s *Store) Append(e Event) (Event, error) {
	if e.Identity.AgentID == "" {
		return e, fmt.Errorf("audit: empty agent_id")
	}

	agentChain, err := s.getAgentChain(e.Identity.AgentID)
	if err != nil {
		return e, err
	}
	linked, err := agentChain.Link(e)
	if err != nil {
		return e, err
	}
	if err := appendNDJSONLine(s.agentAuditPath(e.Identity.AgentID), linked); err != nil {
		return linked, fmt.Errorf("audit: append agent ndjson: %w", err)
	}

	// Independent index chain: re-link the same content (sans hashes).
	indexEvent := linked
	indexEvent.PrevHash = ""
	indexEvent.SelfHash = ""
	indexed, err := s.indexChain.Link(indexEvent)
	if err != nil {
		return linked, fmt.Errorf("audit: link index: %w", err)
	}
	if err := appendNDJSONLine(s.indexPathForDay(time.Now()), indexed); err != nil {
		return linked, fmt.Errorf("audit: append index ndjson: %w", err)
	}

	return linked, nil
}

func appendNDJSONLine(path string, e Event) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
