package main

// mitmBreakerAdapter routes mitm.BreakerHook.Check into audit.PreventiveCheck.
// Translation table (one-way; mitm doesn't depend on audit package types):
//
//   audit.ActionBlock  → mitm.BreakerActionBlock
//   audit.ActionRedact → mitm.BreakerActionRedact (with ModifiedPayload copied)
//   anything else      → mitm.BreakerActionPass

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
		if len(dec.Findings) > 0 {
			out.RuleID = string(dec.Findings[0].RuleID)
		}
	case audit.ActionRedact:
		out.Action = mitm.BreakerActionRedact
		out.ModifiedPayload = dec.ModifiedPayload
		if len(dec.Findings) > 0 {
			out.RuleID = string(dec.Findings[0].RuleID)
		}
	default:
		out.Action = mitm.BreakerActionPass
	}
	return out
}
