// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// MITM-specific header names. Carried between chain hops; stripped at
// final_hop before dialing the real provider.
const (
	HeaderTraceID    = "X-Cicy-Mitm-Trace-Id"
	HeaderTrace      = "X-Cicy-Mitm-Trace"
	HeaderAgent      = "X-Cicy-Mitm-Agent"
	HeaderClientIP   = "X-Cicy-Mitm-Client-IP"
	HeaderHopCount   = "X-Cicy-Mitm-Hop-Count"
	HeaderBlockedBy  = "X-Cicy-Mitm-Blocked-By"
	HeaderNode       = "X-Cicy-Mitm-Node"
)

var (
	errLoopDetected = errors.New("mitm: loop detected — this node already in trace")
	errTooManyHops  = errors.New("mitm: too many hops")
)

// pumpHTTP drives one TLS-terminated client connection through one
// upstream request/response cycle. Phase 1 handles exactly one HTTP/1.1
// turn per TLS connection; clients reconnect for subsequent requests.
// HTTP keep-alive multiplexing is a v2 concern.
func pumpHTTP(
	ctx context.Context,
	client *tls.Conn,
	host string,
	port int,
	cfg *Config,
	dialer *Dialer,
	hook AuditHook,
	breaker BreakerHook,
	identity Identity,
) error {
	if hook == nil {
		hook = noopAuditHook{}
	}
	if breaker == nil {
		breaker = noopBreakerHook{}
	}

	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		return fmt.Errorf("mitm: read request: %w", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("mitm: read request body: %w", err)
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))

	// Chain bookkeeping — annotate headers so audit / next-hop see the trace.
	if err := annotateChainHeaders(req, &cfg.Node, identity); err != nil {
		writeSyntheticError(client, 502, err.Error(), cfg.Node.ID)
		return err
	}

	// Provider + target URL (for audit; outbound write uses req as-is)
	provider := ProviderFromHost(host)
	target := &url.URL{
		Scheme: "https",
		Host:   host,
		Path:   req.URL.Path,
	}

	// === Audit: start turn === — sees the request *with* trace headers, so
	// downstream audit / dashboards can correlate across hops.
	turn := hook.StartTurn(provider, identity.AgentID, target, req.Method, req.Header.Clone(), body)

	// === Breaker: PreventiveCheck ===
	// Audit has already recorded the outbound payload via current.json
	// (and a "mitm" envelope via SubmitMitmEvent), so a block decision
	// here still leaves a forensic trail.
	bd := breaker.Check(BreakerRequest{
		AgentID:    identity.AgentID,
		TurnID:     req.Header.Get(HeaderTraceID),
		Provider:   provider,
		Model:      "",
		Host:       host,
		Direction:  "outbound",
		PayloadRef: fmt.Sprintf("current.json#%s", req.Header.Get(HeaderTraceID)),
		Payload:    body,
	})
	switch bd.Action {
	case BreakerActionBlock:
		if err := writeAuditBlockRaw(client, bd.EventID, bd.Rules, bd.Message, cfg.Node.ID); err != nil {
			log.Printf("[mitm] write audit block: %v", err)
		}
		turn.Fail(fmt.Errorf("blocked by %s: %s", bd.RuleID, bd.Reason))
		return nil
	case BreakerActionPass:
		// fall through — forward unchanged. (redact removed: the breaker never
		// rewrites the body; audit only passes or blocks.)
	}

	// Final hop: strip the trace headers so the real provider never sees
	// them. Done after audit + breaker, before req.Write to upstream.
	if cfg.Node.FinalHop {
		stripChainHeaders(req)
	}

	// Forward upstream over a POOLED keep-alive connection (dialer.RoundTrip),
	// so the MITM↔upstream TLS handshake is reused across turns instead of paid
	// per request. Adapt the server-read request into a client request:
	//   - absolute URL with host:port so the Transport dials the right upstream;
	//   - RequestURI cleared (RoundTrip rejects a set RequestURI);
	//   - Close=false so the upstream conn returns to the idle pool.
	// The Host header is preserved from the original request.
	req = req.WithContext(ctx)
	req.URL.Scheme = "https"
	req.URL.Host = net.JoinHostPort(host, strconv.Itoa(port))
	req.RequestURI = ""
	req.Close = false
	if req.Host == "" {
		req.Host = host
	}

	resp, err := dialer.RoundTrip(req)
	if err != nil {
		turn.Fail(err)
		writeSyntheticError(client, 502, fmt.Sprintf("upstream request failed: %v", err), cfg.Node.ID)
		return err
	}

	// Wrap response body with audit reader so SSE events are parsed as they
	// stream through to the client. Closing it drains + returns the upstream
	// conn to the idle pool for reuse.
	resp.Body = turn.WrapResponseBody(resp.Body, resp.StatusCode, resp.Header.Clone(), resp.ContentLength)
	defer resp.Body.Close()

	// Tell the CLIENT to close after this turn — client side stays single-turn
	// (phase A pools the upstream side only). This is independent of upstream
	// reuse, which the Transport manages from the upstream response + drained body.
	resp.Close = true
	resp.Header.Set("Connection", "close")

	if err := resp.Write(client); err != nil {
		// Audit reader has already captured the partial body; let it Close.
		return fmt.Errorf("mitm: write response to client: %w", err)
	}
	return nil
}

// annotateChainHeaders applies the §5.10 trace headers. Returns an error
// for loop or hop-count violations.
func annotateChainHeaders(req *http.Request, node *NodeConfig, identity Identity) error {
	if req.Header.Get(HeaderTraceID) == "" {
		req.Header.Set(HeaderTraceID, uuid.NewString())
	}
	if identity.AgentID != "" && req.Header.Get(HeaderAgent) == "" {
		req.Header.Set(HeaderAgent, identity.AgentID)
	}
	if identity.ClientIP != "" && req.Header.Get(HeaderClientIP) == "" {
		req.Header.Set(HeaderClientIP, identity.ClientIP)
	}

	trace := req.Header.Get(HeaderTrace)
	var hops []string
	if trace != "" {
		hops = strings.Split(trace, ",")
		for _, h := range hops {
			if strings.TrimSpace(h) == node.ID {
				return errLoopDetected
			}
		}
	}
	if node.MaxHops > 0 && len(hops) >= node.MaxHops {
		return errTooManyHops
	}
	hops = append(hops, node.ID)
	req.Header.Set(HeaderTrace, strings.Join(hops, ","))
	req.Header.Set(HeaderHopCount, strconv.Itoa(len(hops)))
	return nil
}

func stripChainHeaders(req *http.Request) {
	for _, h := range []string{
		HeaderTraceID, HeaderTrace, HeaderAgent,
		HeaderClientIP, HeaderHopCount, HeaderBlockedBy, HeaderNode,
	} {
		req.Header.Del(h)
	}
}

// writeSyntheticError emits a minimal HTTP/1.1 error response back to the
// client. Used for dial failures, loop detection, etc. Phase 3 will
// replace this with provider-specific error envelopes (Anthropic / OpenAI
// shapes) when a breaker rule actively rejects.
func writeSyntheticError(w io.Writer, status int, reason, nodeID string) {
	bodyMsg := fmt.Sprintf("cicy-mitm: %s", reason)
	body := []byte(bodyMsg)
	hdr := "HTTP/1.1 " + strconv.Itoa(status) + " " + http.StatusText(status) + "\r\n"
	hdr += "Content-Type: text/plain; charset=utf-8\r\n"
	hdr += "Content-Length: " + strconv.Itoa(len(body)) + "\r\n"
	if nodeID != "" {
		hdr += HeaderNode + ": " + nodeID + "\r\n"
	}
	hdr += "Connection: close\r\n\r\n"
	if _, err := w.Write([]byte(hdr)); err != nil {
		log.Printf("[mitm] writeSyntheticError: %v", err)
		return
	}
	_, _ = w.Write(body)
}
