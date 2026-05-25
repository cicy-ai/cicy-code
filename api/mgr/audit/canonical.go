package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// canonicalize normalizes the event so JSON serialization is deterministic:
//   - SelfHash cleared (it's the output we're computing)
//   - nil slices replaced with empty slices (so json renders [] not null)
//
// Field order is fixed by the Go struct, so encoding/json produces stable
// output as long as the struct doesn't change.
func canonicalize(e Event) Event {
	e.SelfHash = ""
	if e.Findings == nil {
		e.Findings = []Finding{}
	}
	for i := range e.Findings {
		if e.Findings[i].Spans == nil {
			e.Findings[i].Spans = []Span{}
		}
	}
	return e
}

// CanonicalJSON returns the deterministic JSON encoding used for hashing.
func CanonicalJSON(e Event) ([]byte, error) {
	return json.Marshal(canonicalize(e))
}

// ComputeSelfHash returns "sha256:<hex>" over the canonical JSON encoding
// of e (with SelfHash zeroed during the computation).
func ComputeSelfHash(e Event) (string, error) {
	b, err := CanonicalJSON(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
