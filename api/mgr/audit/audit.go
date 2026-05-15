package audit

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Process-global pipeline. Init is called once at server startup;
// subsequent Submit calls are fire-and-forget no-ops if Init failed.
var (
	globalOnce     sync.Once
	globalPipeline *Pipeline
	globalErr      error
)

// Init wires up the audit pipeline using the standard cicy-code paths:
//
//	~/cicy-ai/audit/      — policy, machine_id, chain state, global index
//	~/cicy-ai/workers/    — per-agent audit.ndjson live alongside history
//
// Both directories are auto-created with mode 0700. Idempotent.
func Init() error {
	globalOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			globalErr = err
			return
		}
		auditRoot := filepath.Join(home, "cicy-ai", "audit")
		workersRoot := filepath.Join(home, "cicy-ai", "workers")
		policy, err := LoadPolicy(filepath.Join(auditRoot, "policy.json"))
		if err != nil {
			globalErr = err
			return
		}
		p, err := NewPipeline(auditRoot, workersRoot, NoopScanner{}, policy)
		if err != nil {
			globalErr = err
			return
		}
		globalPipeline = p
		log.Printf("[audit] initialized root=%s rules_version=%s", auditRoot, RulesVersion)
	})
	return globalErr
}

// Submit hands an envelope to the global pipeline. Becomes a no-op if Init
// has not succeeded — audit failure must never break the caller path.
func Submit(ctx context.Context, env Envelope) {
	if globalPipeline == nil {
		return
	}
	globalPipeline.Submit(ctx, env)
}

// Wait blocks for all in-flight async submits. Use during shutdown / tests.
func Wait() {
	if globalPipeline != nil {
		globalPipeline.Wait()
	}
}

// SubmitGatewayOutbound is a convenience wrapper for the cicy AI gateway
// outbound (request) write callback in ai_gateway_audit.go.
func SubmitGatewayOutbound(agentID, agentType, userID, sessionID, turnID, conversationID, provider, model string, payload []byte) {
	Submit(context.Background(), Envelope{
		AgentID:        agentID,
		AgentType:      agentType,
		UserID:         userID,
		SessionID:      sessionID,
		SourceChannel:  SourceGateway,
		TurnID:         turnID,
		ConversationID: conversationID,
		Provider:       provider,
		Model:          model,
		Direction:      DirectionOutbound,
		Payload:        payload,
		PayloadRef:     fmt.Sprintf("current.json#%s", turnID),
	})
}

// SubmitGatewayInbound is the inbound (reply) counterpart.
func SubmitGatewayInbound(agentID, agentType, userID, sessionID, turnID, conversationID, provider, model string, payload []byte) {
	Submit(context.Background(), Envelope{
		AgentID:        agentID,
		AgentType:      agentType,
		UserID:         userID,
		SessionID:      sessionID,
		SourceChannel:  SourceGateway,
		TurnID:         turnID,
		ConversationID: conversationID,
		Provider:       provider,
		Model:          model,
		Direction:      DirectionInbound,
		Payload:        payload,
		PayloadRef:     fmt.Sprintf("reply.json#%s", turnID),
	})
}
