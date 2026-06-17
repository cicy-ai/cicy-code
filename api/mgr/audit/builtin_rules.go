package audit

import (
	"bytes"
	"math"
	"regexp"
)

// BuiltinRule is one of the v1 stock detection rules shipped in the binary.
// Per docs/v1/audit-system-design.md §6.3 (10 rules covering common
// secret / PII / network-topology patterns).
//
// Each rule has:
//   - a deterministic Detect() that returns zero or more Spans;
//   - a default Severity, Category, ScanDirections, Inline flag, and Action;
//   - all of which can be overridden by policy.json's rules_override (Phase 2).
type BuiltinRule struct {
	ID             string
	Label          string
	Category       string
	Severity       Severity
	ScanDirections []string
	Inline         bool
	DefaultAction  Action

	// Pattern is the rule's regex source when it is a regex rule, surfaced so
	// the UI can SHOW and (via rules_override.pattern) edit it. Empty for rules
	// backed by a Go function (aws_secret/high_entropy/bank_card/id_card) — those
	// do validation/entropy logic that isn't a single regex.
	Pattern string

	Detect func(payload []byte) []Span
}

// BuiltinRules returns the v1 fixed rule set. Order is deterministic so the
// scanner output is reproducible across runs.
func BuiltinRules() []BuiltinRule {
	return []BuiltinRule{
		{
			ID:             "secret.jwt",
			Label:          "JSON Web Token",
			Category:       "secret",
			Severity:       SeverityMedium,
			ScanDirections: []string{DirectionOutbound},
			Inline:         false,
			DefaultAction:  ActionLog,
			Pattern:        `eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`,
			Detect:         regexDetect(`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		},
		{
			ID:             "secret.bearer_token",
			Label:          "Authorization bearer token",
			Category:       "secret",
			Severity:       SeverityMedium,
			ScanDirections: []string{DirectionOutbound},
			Inline:         false,
			DefaultAction:  ActionLog,
			Pattern:        `Bearer\s+[A-Za-z0-9._\-+/=]{20,}`,
			Detect:         regexDetect(`Bearer\s+[A-Za-z0-9._\-+/=]{20,}`),
		},
	}
}

// MaterializeBuiltins converts the compiled builtin rules into editable
// CustomRule config entries so a fresh policy.json can carry the FULL rule set.
// Once materialized (RulesManaged=true) the hardcoded layer is no longer merged
// at runtime, so every former built-in is editable / deletable via the config.
// Go-function builtins (no regex Pattern) are skipped — they can't be expressed
// as a config matcher; today every builtin has a Pattern so none are dropped.
func MaterializeBuiltins() []CustomRule {
	out := []CustomRule{}
	for _, r := range BuiltinRules() {
		if r.Pattern == "" {
			continue
		}
		out = append(out, CustomRule{
			ID:       r.ID,
			Label:    r.Label,
			Category: r.Category,
			Severity: r.Severity,
			// Default: scan BOTH directions — outbound (agent → model) AND
			// inbound (model → agent). Secrets leak either way.
			ScanDirections: []string{DirectionOutbound, DirectionInbound},
			Inline:         r.Inline,
			DefaultAction:  r.DefaultAction,
			Match:          RuleMatch{Type: "regex", Pattern: r.Pattern},
			Tests:          builtinDefaultTests(r.ID),
		})
	}
	return out
}

// DefaultPolicyWithBuiltins is the seed written to disk on first run: the
// default policy with every builtin materialized into CustomRules and
// RulesManaged set, so policy.json is the single source of truth.
func DefaultPolicyWithBuiltins() *Policy {
	p := DefaultPolicy()
	p.RulesManaged = true
	p.CustomRules = MaterializeBuiltins()
	return p
}

// safeRegexDetect is regexDetect but returns ok=false instead of panicking on
// a bad pattern — used for operator-supplied override patterns at build time.
func safeRegexDetect(pattern string) (func([]byte) []Span, bool) {
	if _, err := regexp.Compile(pattern); err != nil {
		return nil, false
	}
	return regexDetect(pattern), true
}

// regexDetect builds a Detect closure that wraps an RE2 pattern.
// Each match becomes one Span with start/end + masked preview.
func regexDetect(pattern string) func([]byte) []Span {
	re := regexp.MustCompile(pattern)
	return func(payload []byte) []Span {
		idxs := re.FindAllSubmatchIndex(payload, -1)
		if len(idxs) == 0 {
			return nil
		}
		spans := make([]Span, 0, len(idxs))
		for _, m := range idxs {
			// If a numbered capture group exists, prefer it (used by pii.phone_cn
			// to skip the surrounding non-digit boundary chars).
			start, end := m[0], m[1]
			if len(m) >= 4 && m[2] >= 0 {
				start, end = m[2], m[3]
			}
			spans = append(spans, Span{
				Start:   start,
				End:     end,
				Preview: maskPreview(string(payload[start:end])),
			})
		}
		return spans
	}
}

// maskPreview keeps the head and tail visible, replacing the interior with
// fixed-width asterisks. Short strings are returned verbatim — a four-byte
// secret has no useful structure to hide.
func maskPreview(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:4] + "****" + s[len(s)-4:]
}

// detectIDCardCN: 18-digit ID where the 18th digit is the GB 11643-1999
// weighted checksum of the first 17.
func detectIDCardCN(payload []byte) []Span {
	re := regexp.MustCompile(`[1-9]\d{16}[\dXx]`)
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checkMap := []byte{'1', '0', 'X', '9', '8', '7', '6', '5', '4', '3', '2'}

	idxs := re.FindAllIndex(payload, -1)
	if len(idxs) == 0 {
		return nil
	}
	spans := make([]Span, 0, len(idxs))
	for _, idx := range idxs {
		s := payload[idx[0]:idx[1]]
		sum := 0
		for i := 0; i < 17; i++ {
			sum += int(s[i]-'0') * weights[i]
		}
		expected := checkMap[sum%11]
		actual := s[17]
		if actual == 'x' {
			actual = 'X'
		}
		if actual != expected {
			continue
		}
		spans = append(spans, Span{Start: idx[0], End: idx[1], Preview: maskPreview(string(s))})
	}
	return spans
}

// detectBankCard: 13-19 digit candidates that pass Luhn.
func detectBankCard(payload []byte) []Span {
	re := regexp.MustCompile(`\b\d{13,19}\b`)
	idxs := re.FindAllIndex(payload, -1)
	if len(idxs) == 0 {
		return nil
	}
	spans := make([]Span, 0, len(idxs))
	for _, idx := range idxs {
		s := payload[idx[0]:idx[1]]
		if !luhnValid(s) {
			continue
		}
		spans = append(spans, Span{Start: idx[0], End: idx[1], Preview: maskPreview(string(s))})
	}
	return spans
}

func luhnValid(digits []byte) bool {
	sum := 0
	alt := false
	for i := len(digits) - 1; i >= 0; i-- {
		c := digits[i]
		if c < '0' || c > '9' {
			return false
		}
		n := int(c - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

// detectHighEntropy: base64-ish candidates of length 20-80 with Shannon
// entropy >= 4.5 AND surrounded by a credential-context keyword. This is
// the catch-all for unknown secret formats; deliberately conservative to
// avoid flagging random hashes / IDs.
func detectHighEntropy(payload []byte) []Span {
	const minLen = 20
	const maxLen = 80
	const entropyThreshold = 4.5
	const contextWindow = 30

	contextKeywords := [][]byte{
		[]byte("token"), []byte("TOKEN"),
		[]byte("key"), []byte("KEY"),
		[]byte("secret"), []byte("SECRET"),
		[]byte("password"), []byte("PASSWORD"),
		[]byte("api_key"), []byte("API_KEY"),
		[]byte("auth"), []byte("AUTH"),
	}

	candidate := regexp.MustCompile(`[A-Za-z0-9_+/=-]{20,80}`)
	idxs := candidate.FindAllIndex(payload, -1)
	if len(idxs) == 0 {
		return nil
	}
	var spans []Span
	for _, idx := range idxs {
		s := payload[idx[0]:idx[1]]
		if len(s) < minLen || len(s) > maxLen {
			continue
		}
		if shannonEntropy(s) < entropyThreshold {
			continue
		}
		ctxStart := idx[0] - contextWindow
		if ctxStart < 0 {
			ctxStart = 0
		}
		ctxEnd := idx[1] + contextWindow
		if ctxEnd > len(payload) {
			ctxEnd = len(payload)
		}
		ctx := payload[ctxStart:ctxEnd]
		// Require either an "=" or "name:" / "name=" style assignment, OR
		// a credential keyword anywhere in the window.
		hasAssign := bytes.IndexByte(ctx, '=') >= 0 || bytes.IndexByte(ctx, ':') >= 0
		hasKeyword := false
		for _, kw := range contextKeywords {
			if bytes.Contains(ctx, kw) {
				hasKeyword = true
				break
			}
		}
		if !hasAssign && !hasKeyword {
			continue
		}
		// Both assignment AND a keyword nearby — much higher confidence than
		// assignment alone (avoids matching every base64 blob in JSON).
		if !hasKeyword {
			continue
		}
		spans = append(spans, Span{Start: idx[0], End: idx[1], Preview: maskPreview(string(s))})
	}
	return spans
}

func shannonEntropy(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	var freq [256]float64
	for _, c := range b {
		freq[c]++
	}
	n := float64(len(b))
	e := 0.0
	for _, f := range freq {
		if f == 0 {
			continue
		}
		p := f / n
		e -= p * math.Log2(p)
	}
	return e
}

// detectAWSSecret: a 40-character base64-ish string is treated as an AWS
// secret access key only when proven by context — either there is an AKID
// (AKIA/ASIA prefix) within 200 chars, OR a "*SecretAccessKey*" /
// "aws_secret*" keyword within 50 chars. Stripped of context, 40 chars of
// base64 is unidentifiable; this discipline keeps false positives near zero.
func detectAWSSecret(payload []byte) []Span {
	candidate := regexp.MustCompile(`[A-Za-z0-9/+]{40}`)
	contextKeywords := [][]byte{
		[]byte("aws_secret"),
		[]byte("AWS_SECRET"),
		[]byte("SecretAccessKey"),
		[]byte("secret_access_key"),
	}
	akidRe := regexp.MustCompile(`(AKIA|ASIA)[0-9A-Z]{16}`)
	akidIdxs := akidRe.FindAllIndex(payload, -1)

	idxs := candidate.FindAllIndex(payload, -1)
	if len(idxs) == 0 {
		return nil
	}
	var spans []Span
	for _, idx := range idxs {
		s := payload[idx[0]:idx[1]]
		nearAKID := false
		for _, a := range akidIdxs {
			d := idx[0] - a[0]
			if d < 0 {
				d = -d
			}
			if d <= 200 {
				nearAKID = true
				break
			}
		}
		nearKeyword := false
		ctxStart := idx[0] - 50
		if ctxStart < 0 {
			ctxStart = 0
		}
		ctxEnd := idx[1] + 50
		if ctxEnd > len(payload) {
			ctxEnd = len(payload)
		}
		ctx := payload[ctxStart:ctxEnd]
		for _, kw := range contextKeywords {
			if bytes.Contains(ctx, kw) {
				nearKeyword = true
				break
			}
		}
		if !nearAKID && !nearKeyword {
			continue
		}
		spans = append(spans, Span{Start: idx[0], End: idx[1], Preview: maskPreview(string(s))})
	}
	return spans
}
