package main

// Adapter that lets the mitm sub-package (api/mgr/mitm) feed its captured
// plaintext into the existing ai_gateway_audit pipeline without itself
// depending on package main.
//
// See docs/v1/mitm-system-design.md §3.1 for the reuse rationale.

import (
	"io"
	"log"
	"net/http"
	"net/url"

	"ttyd-go/mgr/mitm"
)

// mitmAuditAdapter implements mitm.AuditHook by delegating to
// newAIGatewayAuditSession + newAIGatewayAuditReadCloser.
type mitmAuditAdapter struct{}

func (mitmAuditAdapter) StartTurn(provider, agentID string, target *url.URL, method string, headers http.Header, body []byte) mitm.AuditTurn {
	session := newAIGatewayAuditSession(provider, agentID, target, target.Path, method, headers, body)
	// Route audit events through SubmitMitmEvent (SourceChannel="mitm")
	// instead of the default SubmitGateway* path. Must be set before
	// writeStartSnapshots so the outbound submit picks it up.
	session.setSourceChannel("mitm")
	if err := session.writeStartSnapshots(); err != nil {
		log.Printf("[mitm] writeStartSnapshots: %v", err)
	}
	return &mitmAuditTurn{session: session}
}

type mitmAuditTurn struct {
	session *aiGatewayAuditSession
}

func (t *mitmAuditTurn) WrapResponseBody(inner io.ReadCloser, statusCode int, headers http.Header, contentLength int64) io.ReadCloser {
	return newAIGatewayAuditReadCloser(inner, t.session, statusCode, headers, contentLength)
}

func (t *mitmAuditTurn) Fail(err error) {
	t.session.completeFromError(err)
}
