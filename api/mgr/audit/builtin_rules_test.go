package audit

import (
	"strings"
	"testing"
)

// findRule returns the BuiltinRule with the given id, or fails the test.
func findRule(t *testing.T, id string) BuiltinRule {
	t.Helper()
	for _, r := range BuiltinRules() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("builtin rule %q not found", id)
	return BuiltinRule{}
}

func assertMatch(t *testing.T, ruleID string, payload string, wantMatch bool) {
	t.Helper()
	rule := findRule(t, ruleID)
	spans := rule.Detect([]byte(payload))
	got := len(spans) > 0
	if got != wantMatch {
		t.Errorf("[%s] match=%v want=%v\n  payload: %q\n  spans: %+v",
			ruleID, got, wantMatch, payload, spans)
	}
}

func TestBuiltin_PrivateKey(t *testing.T) {
	assertMatch(t, "secret.private_key", "-----BEGIN RSA PRIVATE KEY-----", true)
	assertMatch(t, "secret.private_key", "-----BEGIN OPENSSH PRIVATE KEY-----", true)
	assertMatch(t, "secret.private_key", "...intro...\n-----BEGIN EC PRIVATE KEY-----\nMIIE", true)
	assertMatch(t, "secret.private_key", "-----BEGIN PUBLIC KEY-----", false)
	assertMatch(t, "secret.private_key", "BEGIN PRIVATE KEY", false)
}

func TestBuiltin_AWSAkid(t *testing.T) {
	assertMatch(t, "secret.aws_akid", "AKIAIOSFODNN7EXAMPLE", true)
	assertMatch(t, "secret.aws_akid", "key=ASIAQUARJKBDF7JMHGIK,end", true)
	assertMatch(t, "secret.aws_akid", "AKIAIOSFODNN7EXAMPL", false) // 19 chars
	assertMatch(t, "secret.aws_akid", "AKIA", false)
	assertMatch(t, "secret.aws_akid", "AKIAabcdef", false) // lowercase invalid
}

func TestBuiltin_AWSSecret(t *testing.T) {
	// AWS docs' canonical example secret is exactly 40 chars (with two slashes).
	const secret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	if len(secret) != 40 {
		t.Fatalf("test fixture must be 40 chars, got %d", len(secret))
	}

	// 40-char base64 with nearby AKID — context #1: AKID proximity.
	assertMatch(t, "secret.aws_secret", "AKIAIOSFODNN7EXAMPLE\nsecret="+secret, true)

	// 40-char base64 with keyword context, no AKID — context #2: keyword.
	assertMatch(t, "secret.aws_secret",
		`{"aws_secret_access_key":"`+secret+`"}`, true)

	// 40-char base64 with NO AKID and NO keyword: must NOT match (false-positive guard).
	assertMatch(t, "secret.aws_secret",
		"prefix-some-random-40-char-base64-here-xx-"+strings.Repeat("a", 40),
		false)
}

func TestBuiltin_JWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	assertMatch(t, "secret.jwt", jwt, true)
	assertMatch(t, "secret.jwt", "auth: "+jwt, true)
	assertMatch(t, "secret.jwt", "eyJ.eyJ.short", false)
	assertMatch(t, "secret.jwt", "not a jwt at all", false)
}

func TestBuiltin_BearerToken(t *testing.T) {
	assertMatch(t, "secret.bearer_token",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123",
		true)
	assertMatch(t, "secret.bearer_token", "Bearer abc", false) // too short
}

func TestBuiltin_HighEntropy(t *testing.T) {
	// 40 base64-ish chars w/ token=keyword nearby: high entropy + context
	hi := "token=aBcDeFgHiJkLmNoPqRsTuVwXyZ1234567890_PQR"
	assertMatch(t, "secret.high_entropy", hi, true)
	// Plain English text, no high entropy
	assertMatch(t, "secret.high_entropy", "hello world this is a normal sentence", false)
	// Long but low-entropy run
	assertMatch(t, "secret.high_entropy", "aaaaaaaaaaaaaaaaaaaaaaaaaa = token", false)
	// 40 chars high entropy WITHOUT keyword context
	assertMatch(t, "secret.high_entropy", strings.Repeat("aB1cD2eF3gH4iJ5kL6mN7oP8", 2), false)
}

func TestBuiltin_IDCardCN(t *testing.T) {
	// 110105194912310020 + 17 weights: see GB 11643-1999; check digit 'X'.
	assertMatch(t, "pii.id_card_cn", "id=11010519491231002X", true)
	assertMatch(t, "pii.id_card_cn", "id=11010519491231002x", true) // lower x OK
	// Bad checksum (last digit replaced)
	assertMatch(t, "pii.id_card_cn", "id=110105194912310029", false)
	// Wrong length
	assertMatch(t, "pii.id_card_cn", "id=1101051949123100", false)
}

func TestBuiltin_BankCard(t *testing.T) {
	assertMatch(t, "pii.bank_card", "card 4242424242424242 end", true)        // valid Visa test
	assertMatch(t, "pii.bank_card", "4111 1111 1111 1111", false)              // spaces break regex
	assertMatch(t, "pii.bank_card", "card 4242424242424241 end", false)        // bad Luhn
	assertMatch(t, "pii.bank_card", "card 12345 end", false)                   // too short
}

func TestBuiltin_PhoneCN(t *testing.T) {
	assertMatch(t, "pii.phone_cn", "我的手机是13800138000请回电", true)
	assertMatch(t, "pii.phone_cn", "联系15912345678", true)
	assertMatch(t, "pii.phone_cn", "phone:12300000000", false) // 12 prefix
	assertMatch(t, "pii.phone_cn", "id 1380013800", false)     // 10 digits
}

func TestBuiltin_PrivateIP(t *testing.T) {
	assertMatch(t, "network.private_ip", "internal=10.0.0.1", true)
	assertMatch(t, "network.private_ip", "host 172.16.0.5", true)
	assertMatch(t, "network.private_ip", "lan 192.168.1.1", true)
	assertMatch(t, "network.private_ip", "172.31.255.255", true)
	assertMatch(t, "network.private_ip", "ext 8.8.8.8", false)
	assertMatch(t, "network.private_ip", "172.15.0.1", false) // out of range
	assertMatch(t, "network.private_ip", "172.32.0.1", false) // out of range
	assertMatch(t, "network.private_ip", "193.168.1.1", false)
}

// TestBuiltinScanner_DirectionGating: pii rules scan both directions, but
// secret/network rules only scan outbound. Verify the scanner respects this.
func TestBuiltinScanner_DirectionGating(t *testing.T) {
	s := NewBuiltinScanner()
	payload := []byte("AKIAIOSFODNN7EXAMPLE and phone 13800138000")

	out := s.Scan(payload, DirectionOutbound, nil)
	gotOutbound := map[string]bool{}
	for _, f := range out {
		gotOutbound[f.RuleID] = true
	}
	if !gotOutbound["secret.aws_akid"] {
		t.Error("outbound: expected secret.aws_akid finding")
	}
	if !gotOutbound["pii.phone_cn"] {
		t.Error("outbound: expected pii.phone_cn finding")
	}

	in := s.Scan(payload, DirectionInbound, nil)
	gotInbound := map[string]bool{}
	for _, f := range in {
		gotInbound[f.RuleID] = true
	}
	if gotInbound["secret.aws_akid"] {
		t.Error("inbound: secret.aws_akid should be gated out (outbound-only rule)")
	}
	if !gotInbound["pii.phone_cn"] {
		t.Error("inbound: pii.phone_cn should still match (both-direction rule)")
	}
}

// TestBuiltinScanner_NoFindingsOnEmpty: empty payload yields zero findings,
// not nil, not panic.
func TestBuiltinScanner_NoFindingsOnEmpty(t *testing.T) {
	s := NewBuiltinScanner()
	out := s.Scan([]byte{}, DirectionOutbound, nil)
	if len(out) != 0 {
		t.Errorf("empty payload: got %d findings, want 0", len(out))
	}
}

// TestBuiltinScanner_MaskPreviewShape: ensures the preview never leaks the
// secret in full — head4 + **** + tail4 for anything > 8 chars.
func TestBuiltinScanner_MaskPreviewShape(t *testing.T) {
	s := NewBuiltinScanner()
	akid := "AKIAIOSFODNN7EXAMPLE"
	out := s.Scan([]byte("k="+akid), DirectionOutbound, nil)
	var f *Finding
	for i := range out {
		if out[i].RuleID == "secret.aws_akid" {
			f = &out[i]
			break
		}
	}
	if f == nil {
		t.Fatal("expected secret.aws_akid finding")
	}
	if len(f.Spans) == 0 {
		t.Fatal("expected at least one span")
	}
	preview := f.Spans[0].Preview
	if !strings.HasPrefix(preview, "AKIA") {
		t.Errorf("preview should start with AKIA, got %q", preview)
	}
	if !strings.Contains(preview, "****") {
		t.Errorf("preview should mask middle with ****, got %q", preview)
	}
	if strings.Contains(preview, "IOSF") || strings.Contains(preview, "EXAM") {
		t.Errorf("preview leaks middle bytes: %q", preview)
	}
}
