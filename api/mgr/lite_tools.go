package main

// Custom-tool runtime for lite agents (todo #103). A custom tool is declared in
// lite-config.json as a FIXED argv template plus a param schema; this file turns
// such a declaration into (a) an Anthropic tool def and (b) a guarded executor.
//
// Injection safety: the argv is fixed at declaration time. "{param}" elements
// are replaced by schema-validated values via exec.Command's arg vector — there
// is NO shell, so a value can never become a new command, flag, pipe or
// redirect. The LLM controls a parameter's VALUE, never the command shape.
//
// Execution guardrails: context timeout (kill on overrun), cwd pinned to the
// agent's workspace, environment washed down to a minimal safe set (no tokens,
// no proxy, no agent identity), combined output truncated, every call audited.

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	liteToolDefaultTimeoutSec = 30
	liteToolMaxTimeoutSec     = 300
	liteToolDefaultMaxKB      = 16
	liteToolDefaultParamMaxLn = 4096
)

// liteParamSlot matches an argv element that is exactly a single "{name}".
var liteParamSlot = regexp.MustCompile(`^\{([a-zA-Z_][a-zA-Z0-9_]*)\}$`)

// liteCustomToolDefs returns Anthropic tool defs for the custom tools enabled on
// this instance, so the model can see and call them alongside the built-ins.
func liteCustomToolDefs(cfg liteConfig) []M {
	if len(cfg.customTools) == 0 {
		return nil
	}
	out := make([]M, 0, len(cfg.customTools))
	for name, t := range cfg.customTools {
		props := M{}
		required := []string{}
		for pname, p := range t.Params {
			desc := "value for {" + pname + "}"
			if len(p.Enum) > 0 {
				desc += " (one of: " + strings.Join(p.Enum, ", ") + ")"
			}
			props[pname] = M{"type": "string", "description": desc}
			if p.Required {
				required = append(required, pname)
			}
		}
		schema := M{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		out = append(out, M{
			"name":         name,
			"description":  t.Description,
			"input_schema": schema,
		})
	}
	return out
}

// runLiteCustomTool executes a declared custom tool after re-checking the gate
// (defense in depth — the def layer already filtered, but never trust one gate)
// and validating every param against its schema.
func runLiteCustomTool(cfg liteConfig, selfShortID, name string, input map[string]interface{}) string {
	// Gate #2: external profiles never run custom tools; the tool must be in
	// this instance's resolved custom set.
	if cfg.external {
		return "error: external profile may not run custom tools"
	}
	tool, ok := cfg.customTools[name]
	if !ok || !cfg.enabledTools[name] {
		return "error: tool " + name + " is not enabled for this agent"
	}
	if len(tool.Argv) == 0 {
		return "error: tool " + name + " has no argv template"
	}

	// Build the concrete argv: literals pass through; "{param}" slots are
	// replaced with the validated value. A "{param}" with no matching schema
	// entry is a declaration error — refuse rather than pass a literal "{x}".
	argv := make([]string, 0, len(tool.Argv))
	for _, elem := range tool.Argv {
		if m := liteParamSlot.FindStringSubmatch(elem); m != nil {
			pname := m[1]
			spec, declared := tool.Params[pname]
			if !declared {
				return fmt.Sprintf("error: argv references undeclared param {%s}", pname)
			}
			val, verr := validateLiteParam(pname, spec, input)
			if verr != "" {
				return "error: " + verr
			}
			argv = append(argv, val)
			continue
		}
		argv = append(argv, elem) // fixed literal — never from input
	}

	timeout := tool.TimeoutSec
	if timeout <= 0 {
		timeout = liteToolDefaultTimeoutSec
	}
	if timeout > liteToolMaxTimeoutSec {
		timeout = liteToolMaxTimeoutSec
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if cfg.workspace != "" {
		cmd.Dir = cfg.workspace
	}
	cmd.Env = liteToolSafeEnv()

	out, err := cmd.CombinedOutput()
	maxKB := tool.MaxOutputKB
	if maxKB <= 0 {
		maxKB = liteToolDefaultMaxKB
	}
	result := truncateForLog(string(out), maxKB*1024)

	exit := 0
	if ctx.Err() == context.DeadlineExceeded {
		result = fmt.Sprintf("error: tool %s timed out after %ds\n%s", name, timeout, result)
		exit = -2
	} else if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			result = "error: " + err.Error()
			exit = -1
		}
	}
	// Audit every invocation: who ran what, with which fixed argv, and the exit.
	log.Printf("[lite-tool] agent=%s tool=%s argv=%q exit=%d", selfShortID, name, argv, exit)
	if strings.TrimSpace(result) == "" {
		result = fmt.Sprintf("(tool %s exited %d, no output)", name, exit)
	}
	return result
}

// validateLiteParam checks one input value against its schema and returns the
// string to slot into argv, or a non-empty error message.
func validateLiteParam(pname string, spec liteToolParam, input map[string]interface{}) (string, string) {
	rawV, present := input[pname]
	val, _ := rawV.(string)
	val = strings.TrimSpace(val)
	if !present || val == "" {
		if spec.Required {
			return "", "missing required param " + pname
		}
		return "", "" // optional + empty → empty literal (rare; tool should design around it)
	}
	maxLen := spec.MaxLen
	if maxLen <= 0 {
		maxLen = liteToolDefaultParamMaxLn
	}
	if len(val) > maxLen {
		return "", fmt.Sprintf("param %s too long (%d > %d)", pname, len(val), maxLen)
	}
	if len(spec.Enum) > 0 {
		okv := false
		for _, e := range spec.Enum {
			if val == e {
				okv = true
				break
			}
		}
		if !okv {
			return "", fmt.Sprintf("param %s must be one of %v", pname, spec.Enum)
		}
	}
	if spec.Pattern != "" {
		re, err := regexp.Compile("^(?:" + spec.Pattern + ")$")
		if err != nil {
			return "", fmt.Sprintf("param %s has invalid pattern in config", pname)
		}
		if !re.MatchString(val) {
			return "", fmt.Sprintf("param %s does not match required pattern", pname)
		}
	}
	return val, ""
}

// liteToolSafeEnv returns a minimal environment for custom-tool subprocesses:
// PATH/HOME/locale only. NO api tokens, NO proxy vars, NO X_AGENT_* identity —
// a custom tool must not inherit the server's credentials or the MITM proxy.
func liteToolSafeEnv() []string {
	keep := []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM", "TMPDIR",
		"SystemRoot", "USERPROFILE", "TEMP", "TMP"} // last four for Windows
	var env []string
	for _, k := range keep {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}
