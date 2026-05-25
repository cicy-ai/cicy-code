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
		if err := writeSyntheticBlockRaw(client, provider, bd.RuleID, bd.Reason, cfg.Node.ID); err != nil {
			log.Printf("[mitm] write synthetic block: %v", err)
		}
		turn.Fail(fmt.Errorf("blocked by %s: %s", bd.RuleID, bd.Reason))
		return nil
	case BreakerActionRedact:
		if len(bd.ModifiedPayload) > 0 {
			body = bd.ModifiedPayload
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
		}
	case BreakerActionPass:
		// fall through
	}

	// Final hop: strip the trace headers so the real provider never sees
	// them. Done after audit + breaker, before req.Write to upstream.
	if cfg.Node.FinalHop {
		stripChainHeaders(req)
	}

	// Dial upstream
	hostPort := net.JoinHostPort(host, strconv.Itoa(port))
	upstream, err := dialer.DialTLS(ctx, hostPort)
	if err != nil {
		turn.Fail(err)
		writeSyntheticError(client, 502, fmt.Sprintf("upstream dial failed: %v", err), cfg.Node.ID)
		return err
	}
	defer upstream.Close()

	// Write request to upstream. req.Write writes origin-form path + Host
	// header + body, handling content-length / chunked correctly.
	if err := req.Write(upstream); err != nil {
		turn.Fail(err)
		return fmt.Errorf("mitm: forward request: %w", err)
	}

	// Read upstream response
	upstreamBr := bufio.NewReader(upstream)
	resp, err := http.ReadResponse(upstreamBr, req)
	if err != nil {
		turn.Fail(err)
		return fmt.Errorf("mitm: read upstream response: %w", err)
	}

	// Wrap response body with audit reader so SSE events are parsed as
	// they stream through to the client.
	resp.Body = turn.WrapResponseBody(resp.Body, resp.StatusCode, resp.Header.Clone(), resp.ContentLength)
	defer resp.Body.Close()

	// Force connection close after this response (Phase 1 single-turn).
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
