// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// mitmBreakerAdapter routes mitm.BreakerHook.Check into audit.PreventiveCheck.
// Translation table (one-way; mitm doesn't depend on audit package types):
//
//   audit.ActionBlock  → mitm.BreakerActionBlock
//   anything else      → mitm.BreakerActionPass
//
// redact removed 2026-06-16: 审计绝不改写用户数据,只 pass(log)或 block。

import (
	"ttyd-go/mgr/audit"
	"ttyd-go/mgr/mitm"
)

type mitmBreakerAdapter struct{}

func (mitmBreakerAdapter) Check(req mitm.BreakerRequest) mitm.BreakerDecision {
	env := audit.Envelope{
		AgentID:    req.AgentID,
		TurnID:     req.TurnID,
		Provider:   req.Provider,
		Model:      req.Model,
		Direction:  req.Direction,
		Payload:    req.Payload,
		PayloadRef: req.PayloadRef,
	}
	env.SourceChannel = audit.SourceMitm
	env.Inline = true

	dec := audit.PreventiveCheck(env)

	out := mitm.BreakerDecision{Reason: dec.Reason}
	switch dec.Action {
	case audit.ActionBlock:
		out.Action = mitm.BreakerActionBlock
		ruleIDs := make([]string, 0, len(dec.Findings))
		for _, f := range dec.Findings {
			ruleIDs = append(ruleIDs, string(f.RuleID))
		}
		if len(ruleIDs) > 0 {
			out.RuleID = ruleIDs[0]
		}
		// UNIFIED block contract: same fields the gateway's writeGatewayBlocked
		// emits, so MITM returns an identical 403 + X-Cicy-Audit-* + body.message.
		out.EventID = dec.EventID
		out.Rules = ruleIDs
		out.Message = auditBlockMessage(ruleIDs, dec.EventID)
	default:
		out.Action = mitm.BreakerActionPass
	}
	return out
}
