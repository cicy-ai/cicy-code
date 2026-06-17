package main

import "testing"

// Harness scaffolding (slash-command wrappers, the auto-compaction preamble)
// must never read as a human prompt — aiGatewaySanitizeUserQuestion blanks them
// so aiGatewayExtractQuestion / the prompts-only history view don't surface them.
func TestSanitizeDropsHarnessScaffolding(t *testing.T) {
	cases := map[string]string{
		"This session is being continued from a previous conversation. blah blah":                            "",
		"  \n This session is being continued from a previous conversation that ran out of context":          "",
		"<local-command-caveat>do not respond</local-command-caveat>\n<command-name>/compact</command-name>": "",
		"<command-name>/clear</command-name><command-message>clear</command-message>":                         "",
		"上线了么": "上线了么", // a real prompt survives untouched
		"<system-reminder>ctx</system-reminder>修一下这个 bug": "修一下这个 bug",
	}
	for in, want := range cases {
		if got := aiGatewaySanitizeUserQuestion(in); got != want {
			t.Fatalf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
