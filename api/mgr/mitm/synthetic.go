package mitm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// WriteSyntheticBlock writes a provider-shaped 429 response that the
// client SDK will surface as a recognizable error (rate_limit_error /
// rate_limit_exceeded). Header X-Cicy-Mitm-Blocked-By carries the node
// id so chain-aware tooling can attribute the block.
func WriteSyntheticBlock(w http.ResponseWriter, provider, ruleID, message, nodeID string) {
	w.Header().Set("Content-Type", "application/json")
	if nodeID != "" {
		w.Header().Set(HeaderBlockedBy, nodeID)
	}
	if ruleID != "" {
		w.Header().Set("X-Cicy-Mitm-Block-Rule", ruleID)
	}
	w.WriteHeader(http.StatusTooManyRequests)
	body := buildBlockBody(provider, ruleID, message)
	_, _ = w.Write(body)
}

// writeSyntheticBlockRaw is like WriteSyntheticBlock but writes directly
// to an io.Writer (the TLS conn) using a minimal HTTP/1.1 wire format.
// Used by the pump because we don't have an http.ResponseWriter.
func writeSyntheticBlockRaw(conn writeOnly, provider, ruleID, message, nodeID string) error {
	body := buildBlockBody(provider, ruleID, message)
	var buf bytes.Buffer
	buf.WriteString("HTTP/1.1 429 Too Many Requests\r\n")
	buf.WriteString("Content-Type: application/json\r\n")
	buf.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n")
	if nodeID != "" {
		buf.WriteString(HeaderBlockedBy + ": " + nodeID + "\r\n")
	}
	if ruleID != "" {
		buf.WriteString("X-Cicy-Mitm-Block-Rule: " + ruleID + "\r\n")
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

// buildBlockBody produces the JSON envelope each provider's SDK
// expects for a rate-limit-class error. Falls back to a plain envelope
// for unknown providers.
func buildBlockBody(provider, ruleID, message string) []byte {
	msg := fmt.Sprintf("cicy-mitm: blocked by rule %s — %s", nonEmpty(ruleID, "(unspecified)"), nonEmpty(message, "preventive policy hit"))
	switch provider {
	case "anthropic":
		payload := map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    "rate_limit_error",
				"message": msg,
			},
		}
		b, _ := json.Marshal(payload)
		return b
	case "openai":
		payload := map[string]interface{}{
			"error": map[string]interface{}{
				"message": msg,
				"type":    "rate_limit_exceeded",
				"code":    "cicy_mitm_blocked",
				"param":   nil,
			},
		}
		b, _ := json.Marshal(payload)
		return b
	default:
		payload := map[string]interface{}{
			"error":   "rate_limit_exceeded",
			"message": msg,
			"code":    "cicy_mitm_blocked",
		}
		b, _ := json.Marshal(payload)
		return b
	}
}

func nonEmpty(a, fallback string) string {
	if a != "" {
		return a
	}
	return fallback
}
