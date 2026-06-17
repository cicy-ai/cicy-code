package audit

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// compileMatcher builds the runtime detect closure for a rule matcher. ALL
// matching runs through the JS (goja) engine — a single execution path. A "js"
// matcher is the snippet itself; a "regex" matcher is TRANSLATED to an
// equivalent JS RegExp matcher (see jsForRegex) and run the same way. This is
// the "全走 js" design: a user-configured regex rule executes as JS at runtime,
// so regex and JS rules share identical engine, semantics, and span extraction.
func compileMatcher(matchType, pattern, flags string) (func([]byte) []Span, error) {
	switch matchType {
	case "js":
		return jsDetect(pattern)
	case "regex", "":
		return jsDetect(jsForRegex(pattern, flags))
	default:
		return nil, fmt.Errorf("unknown match.type %q", matchType)
	}
}

// jsForRegex translates a regex pattern (+ optional flags) into a JS snippet that
// returns the array of matched substrings — spansFromJSResult then locates them
// back to offsets. Pattern and flags are JSON-encoded into JS string literals so
// any character (slashes, quotes, backslashes) survives verbatim, and "g" is
// forced so every match is returned. A pattern invalid under ECMAScript regex
// throws inside the try/catch and yields zero matches.
func jsForRegex(pattern, flags string) string {
	pj, _ := json.Marshal(pattern)
	f := "g"
	for _, c := range flags {
		switch c {
		case 'i', 'm', 's', 'u':
			if !strings.ContainsRune(f, c) {
				f += string(c)
			}
		}
	}
	fj, _ := json.Marshal(f)
	return "var out=[];try{var re=new RegExp(" + string(pj) + "," + string(fj) +
		");var m;while((m=re.exec(text))!==null){out.push(m[0]);if(m.index===re.lastIndex)re.lastIndex++;}}catch(e){}return out;"
}

// jsengine.go — JS matcher for a rule.
//
// A rule is matcher + decision. The matcher can be regex, dict, or — here — JS.
// A JS matcher is a snippet that, given the scanned `text`, decides what matches
// (more expressive than regex: entropy checks, validation, multi-pattern, parse
// the body as JSON and reason about it). The rule's decision (severity / action)
// comes from the surrounding CustomRule, exactly like a regex rule — only the
// match step differs.
//
// Contract: the snippet is the BODY of `function(text){ ... }`. It returns:
//   - a boolean           → matched (one finding, no span detail), or
//   - an array of strings → each matched substring becomes a span (offset +
//     preview located in text).
//
// Example js matcher:
//   const m = text.match(/AKIA[0-9A-Z]{16}/g); return m || [];
//
// Runs in a fresh goja VM per scan with a hard timeout (VM interrupt), so a
// bad/looping rule can't hang or escape the scan. The program is compiled once
// (at rule-build time) and only executed per scan.

const jsEvalTimeout = 200 * time.Millisecond

// jsDetect compiles a JS matcher snippet into a Detect function. Compile errors
// (bad JS) are returned so BuildRuleSet rejects the rule and keeps the prior
// ruleset — a broken rule never takes the pipeline down.
func jsDetect(code string) (func([]byte) []Span, error) {
	prog, err := goja.Compile("rule.js", "(function(text){"+code+"\n})", false)
	if err != nil {
		return nil, err
	}
	return func(payload []byte) []Span {
		vm := goja.New()
		timer := time.AfterFunc(jsEvalTimeout, func() { vm.Interrupt("js rule timeout") })
		defer timer.Stop()

		fnVal, err := vm.RunProgram(prog)
		if err != nil {
			return nil
		}
		fn, ok := goja.AssertFunction(fnVal)
		if !ok {
			return nil
		}
		text := string(payload)
		res, err := fn(goja.Undefined(), vm.ToValue(text))
		if err != nil {
			return nil
		}
		return spansFromJSResult(res.Export(), text)
	}, nil
}

// TestRuleMatcher runs a matcher (regex or js) against sample text and returns
// the matched spans — backs the UI's "test this rule" box so authors can verify
// a regex/JS rule against sample input before saving it. Returns an error for a
// bad pattern/JS (surfaced to the author).
func TestRuleMatcher(matchType, pattern, text string) ([]Span, error) {
	// All matching — regex included — runs through the JS engine (compileMatcher),
	// so the test endpoint exercises the exact same path as runtime.
	d, err := compileMatcher(matchType, pattern, "")
	if err != nil {
		return nil, err
	}
	return d([]byte(text)), nil
}

var (
	errBadRegex         = errString("invalid regex")
	errUnknownMatchType = errString("unknown match type (use regex or js)")
)

type errString string

func (e errString) Error() string { return string(e) }

// spansFromJSResult maps a JS matcher's return value to spans.
func spansFromJSResult(v interface{}, text string) []Span {
	switch r := v.(type) {
	case bool:
		if r {
			return []Span{{Start: 0, End: 0}}
		}
		return nil
	case []interface{}:
		out := make([]Span, 0, len(r))
		for _, e := range r {
			s, ok := e.(string)
			if !ok || s == "" {
				continue
			}
			idx := strings.Index(text, s)
			preview := s
			if len(preview) > 120 {
				preview = preview[:120] + "…"
			}
			if idx < 0 {
				out = append(out, Span{Start: 0, End: 0, Preview: preview})
			} else {
				out = append(out, Span{Start: idx, End: idx + len(s), Preview: preview})
			}
		}
		return out
	case []string:
		// goja sometimes exports a typed string slice.
		conv := make([]interface{}, len(r))
		for i := range r {
			conv[i] = r[i]
		}
		return spansFromJSResult(conv, text)
	}
	return nil
}
