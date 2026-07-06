// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
)

// RuleSet is the effective set of detection rules after applying the active
// policy to the builtin set. Rebuilt every time policy.json reloads.
type RuleSet struct {
	Rules []BuiltinRule
}

// BuildRuleSet merges builtin rules with policy overrides and custom rules.
// Returns the new RuleSet. On any custom-rule compile / dict-load failure
// it returns the error and the caller MUST keep the previous RuleSet
// active (per fail-mode rules — never run a half-loaded policy).
func BuildRuleSet(builtin []BuiltinRule, policy *Policy) (*RuleSet, error) {
	if policy == nil {
		policy = DefaultPolicy()
	}
	overrides := make(map[string]RuleOverride, len(policy.RulesOverride))
	for _, o := range policy.RulesOverride {
		overrides[o.ID] = o
	}

	rules := make([]BuiltinRule, 0, len(builtin)+len(policy.CustomRules))
	// RulesManaged: policy.json is the single source — the builtins have been
	// materialized into CustomRules, so DON'T merge the hardcoded layer (that is
	// what makes former built-ins editable/deletable; otherwise they'd re-appear).
	for _, r := range builtin {
		if policy.RulesManaged {
			break
		}
		o, ok := overrides[r.ID]
		if ok && o.Disabled {
			continue
		}
		if ok {
			if o.Severity != "" {
				r.Severity = o.Severity
			}
			if o.DefaultAction != "" {
				r.DefaultAction = o.DefaultAction
			}
			if o.Pattern != "" {
				// Replace the builtin's matcher with the operator's regex or JS.
				// All matching runs through the JS engine (compileMatcher); a bad
				// pattern keeps the original matcher rather than break.
				mt := o.MatchType
				if mt == "" {
					mt = "regex"
				}
				if d, err := compileMatcher(mt, o.Pattern, ""); err == nil {
					r.Pattern = o.Pattern
					r.Detect = d
				}
			}
		}
		rules = append(rules, r)
	}
	for _, c := range policy.CustomRules {
		if c.Disabled {
			continue
		}
		built, err := compileCustomRule(c)
		if err != nil {
			return nil, fmt.Errorf("audit: custom_rules[%s]: %w", c.ID, err)
		}
		rules = append(rules, built)
	}
	return &RuleSet{Rules: rules}, nil
}

// compileCustomRule produces a runtime BuiltinRule from a CustomRule spec.
func compileCustomRule(c CustomRule) (BuiltinRule, error) {
	rule := BuiltinRule{
		ID:             c.ID,
		Label:          c.Label,
		Category:       c.Category,
		Severity:       c.Severity,
		ScanDirections: append([]string{}, c.ScanDirections...),
		Inline:         c.Inline,
		DefaultAction:  c.DefaultAction,
	}
	if rule.Category == "" {
		rule.Category = "custom"
	}
	if rule.DefaultAction == "" {
		rule.DefaultAction = ActionLog
	}
	switch c.Match.Type {
	case "regex", "js":
		// All matching runs through the JS engine (compileMatcher): a regex is
		// translated to a JS RegExp matcher, a js rule runs its snippet. One path.
		d, err := compileMatcher(c.Match.Type, c.Match.Pattern, c.Match.Flags)
		if err != nil {
			return rule, err
		}
		rule.Detect = d
	case "dict_file":
		terms, err := loadDictFile(c.Match.Path)
		if err != nil {
			return rule, err
		}
		if len(terms) == 0 {
			rule.Detect = func([]byte) []Span { return nil }
		} else {
			rule.Detect = dictDetect(terms)
		}
	default:
		return rule, fmt.Errorf("unknown match.type %q", c.Match.Type)
	}
	return rule, nil
}

// loadDictFile parses a one-term-per-line file. Empty lines and lines whose
// first non-whitespace char is # are ignored. ~/... is expanded.
func loadDictFile(path string) ([]string, error) {
	expanded := expandUserPath(path)
	f, err := os.Open(expanded)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var terms []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		terms = append(terms, line)
	}
	if err := sc.Err(); err != nil {
		return terms, err
	}
	return terms, nil
}

// dictDetect returns all literal-substring matches across the payload for
// every term in the list. Substring overlap between terms produces multiple
// spans; per-rule turn-internal dedup is handled upstream.
func dictDetect(terms []string) func([]byte) []Span {
	byteTerms := make([][]byte, len(terms))
	for i, t := range terms {
		byteTerms[i] = []byte(t)
	}
	return func(payload []byte) []Span {
		var spans []Span
		for _, t := range byteTerms {
			if len(t) == 0 {
				continue
			}
			idx := 0
			for idx <= len(payload)-len(t) {
				pos := bytes.Index(payload[idx:], t)
				if pos < 0 {
					break
				}
				absStart := idx + pos
				absEnd := absStart + len(t)
				spans = append(spans, Span{
					Start:   absStart,
					End:     absEnd,
					Preview: maskPreview(string(payload[absStart:absEnd])),
				})
				idx = absEnd
			}
		}
		return spans
	}
}
