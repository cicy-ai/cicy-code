// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import (
	"io"
	"net/http"
	"net/url"
)

// AuditHook is the contract between mitm and the rest of cicy-code's audit
// pipeline. Package main provides an implementation that wraps
// newAIGatewayAuditSession / newAIGatewayAuditReadCloser etc. — see
// api/mgr/mitm_audit_adapter.go.
//
// Keeping the seam at this interface lets us unit-test the MITM pump
// without depending on the on-disk current.json/reply.json writer.
type AuditHook interface {
	// StartTurn opens a new audit session for one client→upstream request.
	//
	// provider:  "anthropic" | "openai" | "google" | "unknown" — from ProviderFromHost
	// agentID:   InferIdentity result
	// target:    upstream URL (https://api.anthropic.com/v1/messages)
	// method:    HTTP method
	// headers:   request headers (already including any X-Cicy-Mitm-* injections)
	// body:      full request body (already read & buffered)
	//
	// Returns a Turn handle. If the hook is nil (audit disabled), the pump
	// short-circuits the audit calls.
	StartTurn(provider, agentID string, target *url.URL, method string, headers http.Header, body []byte) AuditTurn
}

// AuditTurn wraps one in-flight upstream call.
type AuditTurn interface {
	// WrapResponseBody returns an io.ReadCloser that, while being read,
	// incrementally parses provider-specific events and persists them to
	// reply.json. The pump must use the returned reader (not the original)
	// when copying back to the client.
	WrapResponseBody(inner io.ReadCloser, statusCode int, headers http.Header, contentLength int64) io.ReadCloser

	// Fail signals that the turn ended without a parseable response
	// (dial error, upstream RST, etc). Persists status=error to reply.json.
	Fail(err error)
}

// noopAuditHook is used when AuditHook is nil so the pump code can avoid
// nil-checks at every call site.
type noopAuditHook struct{}

func (noopAuditHook) StartTurn(string, string, *url.URL, string, http.Header, []byte) AuditTurn {
	return noopAuditTurn{}
}

type noopAuditTurn struct{}

func (noopAuditTurn) WrapResponseBody(inner io.ReadCloser, _ int, _ http.Header, _ int64) io.ReadCloser {
	return inner
}
func (noopAuditTurn) Fail(error) {}
