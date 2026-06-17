package mitm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Audit-block response — UNIFIED with the gateway's writeGatewayBlocked so cicy /
// 网关 CLI / MITM all surface an identical terminal "已拦截" turn:
//
//	HTTP 403 Forbidden
//	X-Cicy-Audit-Blocked: <eventID>     (clients key off this header, not the code)
//	X-Cicy-Audit-Rules:   <rule,rule>
//	X-Cicy-Mitm-Blocked-By: <nodeID>    (extra: which chain node blocked)
//	X-Cicy-Mitm-Block-Rule: <rule>      (extra: first rule, chain tooling)
//	{"error":"blocked_by_audit","action":"block","event_id":…,"rules":[…],"message":…}
//
// 403 is non-retryable, so third-party SDKs (claude code / codex) fail cleanly
// instead of the retry storm a 429 rate-limit shape would have triggered.
const (
	auditBlockedHeader = "X-Cicy-Audit-Blocked"
	auditRulesHeader   = "X-Cicy-Audit-Rules"
)

func buildAuditBlockBody(eventID string, rules []string, message string) []byte {
	if rules == nil {
		rules = []string{}
	}
	b, _ := json.Marshal(map[string]interface{}{
		"error":    "blocked_by_audit",
		"action":   "block",
		"event_id": eventID,
		"rules":    rules,
		"message":  message,
	})
	return b
}

// WriteAuditBlock writes the unified 403 audit-block via an http.ResponseWriter.
func WriteAuditBlock(w http.ResponseWriter, eventID string, rules []string, message, nodeID string) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set(auditBlockedHeader, eventID)
	h.Set(auditRulesHeader, strings.Join(rules, ","))
	if len(rules) > 0 {
		h.Set("X-Cicy-Mitm-Block-Rule", rules[0])
	}
	if nodeID != "" {
		h.Set(HeaderBlockedBy, nodeID)
	}
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(buildAuditBlockBody(eventID, rules, message))
}

// writeAuditBlockRaw is like WriteAuditBlock but writes directly to the TLS conn
// (the pump has no http.ResponseWriter) using a minimal HTTP/1.1 wire format.
func writeAuditBlockRaw(conn writeOnly, eventID string, rules []string, message, nodeID string) error {
	body := buildAuditBlockBody(eventID, rules, message)
	var buf bytes.Buffer
	buf.WriteString("HTTP/1.1 403 Forbidden\r\n")
	buf.WriteString("Content-Type: application/json; charset=utf-8\r\n")
	buf.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	buf.WriteString(auditBlockedHeader + ": " + eventID + "\r\n")
	buf.WriteString(auditRulesHeader + ": " + strings.Join(rules, ",") + "\r\n")
	if len(rules) > 0 {
		buf.WriteString("X-Cicy-Mitm-Block-Rule: " + rules[0] + "\r\n")
	}
	if nodeID != "" {
		buf.WriteString(HeaderBlockedBy + ": " + nodeID + "\r\n")
	}
	buf.WriteString("Connection: close\r\n\r\n")
	buf.Write(body)
	_, err := conn.Write(buf.Bytes())
	return err
}

// writeOnly is the smallest interface that satisfies a *tls.Conn for
// purposes of writing a synthetic response.
type writeOnly interface {
	Write(p []byte) (int, error)
}
