// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package mitm

import (
	"bytes"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
)

type recoveryRoundTripper struct {
	calls  int
	bodies []string
}

func (r *recoveryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	body, _ := io.ReadAll(req.Body)
	r.bodies = append(r.bodies, string(body))
	if r.calls == 1 {
		return nil, &tls.RecordHeaderError{Msg: "local error: tls: bad record MAC"}
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func TestRoundTripWithTLSRecoveryEvictsPoolAndReplaysBody(t *testing.T) {
	payload := []byte(`{"model":"gpt-5","input":"hello"}`)
	req, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	rt := &recoveryRoundTripper{}
	closed := 0
	resp, err := roundTripWithTLSRecovery(req, rt, func() { closed++ })
	if err != nil {
		t.Fatalf("fresh retry failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || rt.calls != 2 || closed != 1 {
		t.Fatalf("status=%d calls=%d pool_evictions=%d", resp.StatusCode, rt.calls, closed)
	}
	if len(rt.bodies) != 2 || rt.bodies[0] != string(payload) || rt.bodies[1] != string(payload) {
		t.Fatalf("request body was not replayed exactly: %#v", rt.bodies)
	}
}

func TestRoundTripWithTLSRecoveryDoesNotRetryOtherErrors(t *testing.T) {
	if isTLSRecordIntegrityError(io.EOF) {
		t.Fatal("EOF must not be classified as TLS record corruption")
	}
	if !isTLSRecordIntegrityError(&tls.RecordHeaderError{Msg: "tls: bad record MAC"}) {
		t.Fatal("bad record MAC was not classified")
	}
}
