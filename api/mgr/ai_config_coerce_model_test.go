package main

import "testing"

// A model belonging to a DIFFERENT provider (left selected in the UI after a
// provider switch) must be coerced to the active provider's default, not
// forwarded as-is (which made DeepSeek 400 on opencodeZen's north-mini-code-free).
func TestCoerceModelForeignFallsBackToDefault(t *testing.T) {
	deepseek := &providerConfig{
		DefaultModel: "deepseek-v4-pro",
		Models:       []string{"deepseek-v4-pro", "deepseek-v4-flash"},
	}
	if got := deepseek.coerceModel("north-mini-code-free"); got != "deepseek-v4-pro" {
		t.Fatalf("foreign model should coerce to default, got %q", got)
	}
}

// A model the provider DOES serve passes through untouched.
func TestCoerceModelValidUntouched(t *testing.T) {
	deepseek := &providerConfig{
		DefaultModel: "deepseek-v4-pro",
		Models:       []string{"deepseek-v4-pro", "deepseek-v4-flash"},
	}
	if got := deepseek.coerceModel("deepseek-v4-flash"); got != "deepseek-v4-flash" {
		t.Fatalf("valid model must be untouched, got %q", got)
	}
}

// Mapping is honored: a mapped-to-valid model resolves via the mapping.
func TestCoerceModelHonorsMappingToValid(t *testing.T) {
	p := &providerConfig{
		DefaultModel: "deepseek-v4-pro",
		Models:       []string{"deepseek-v4-pro"},
		ModelMapping: map[string]string{"claude-sonnet*": "deepseek-v4-pro"},
	}
	if got := p.coerceModel("claude-sonnet-4-6"); got != "deepseek-v4-pro" {
		t.Fatalf("mapped model should resolve to mapping target, got %q", got)
	}
}

// No declared model list → cannot validate → leave the (mapped) model as-is so we
// don't break providers that don't enumerate their models.
func TestCoerceModelNoListIsNoop(t *testing.T) {
	p := &providerConfig{DefaultModel: "x", Models: nil}
	if got := p.coerceModel("anything-goes"); got != "anything-goes" {
		t.Fatalf("no model list should be a no-op, got %q", got)
	}
}

// Foreign model but no defaultModel to fall back to → leave as-is (can't guess).
func TestCoerceModelForeignNoDefaultLeavesAsIs(t *testing.T) {
	p := &providerConfig{DefaultModel: "", Models: []string{"a", "b"}}
	if got := p.coerceModel("c"); got != "c" {
		t.Fatalf("no default → leave foreign model as-is, got %q", got)
	}
}
