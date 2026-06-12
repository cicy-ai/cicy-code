//go:build windows

package main

import (
	"bytes"
	"fmt"
)

// ptmQuerySpec is one terminal capability query and the reply a real terminal
// sends back. Modern TUIs (opencode et al.) PROBE the terminal and BLOCK until
// answered before drawing; tmux answers on the program's behalf. Without this
// they hang on a blank alt-screen.
type ptmQuerySpec struct {
	pat   []byte
	reply func(cols, rows, row, col int) string
}

var ptmQuerySpecs = []ptmQuerySpec{
	{[]byte("\x1b[6n"), func(_, _, row, col int) string { return fmt.Sprintf("\x1b[%d;%dR", row, col) }},
	{[]byte("\x1b[5n"), func(_, _, _, _ int) string { return "\x1b[0n" }},
	{[]byte("\x1b[0c"), func(_, _, _, _ int) string { return "\x1b[?62;1;6;9;15;22c" }},
	{[]byte("\x1b[c"), func(_, _, _, _ int) string { return "\x1b[?62;1;6;9;15;22c" }},
	{[]byte("\x1b[>0c"), func(_, _, _, _ int) string { return "\x1b[>1;10;0c" }},
	{[]byte("\x1b[>c"), func(_, _, _, _ int) string { return "\x1b[>1;10;0c" }},
	{[]byte("\x1b[>0q"), func(_, _, _, _ int) string { return "\x1bP>|ptymux(0.1)\x1b\\" }},
	{[]byte("\x1b[18t"), func(cols, rows, _, _ int) string { return fmt.Sprintf("\x1b[8;%d;%dt", rows, cols) }},
	{[]byte("\x1b[14t"), func(cols, rows, _, _ int) string { return fmt.Sprintf("\x1b[4;%d;%dt", rows*16, cols*8) }},
	{[]byte("\x1b[?996n"), func(_, _, _, _ int) string { return "\x1b[?997;1n" }},
	{[]byte("\x1b[?u"), func(_, _, _, _ int) string { return "\x1b[?0u" }},
}

const ptmLongestQueryPat = 8

// ptmDrainQueries scans carry for capability queries, returns the concatenated
// replies plus the short trailing remainder to carry into the next read.
// Matched queries are consumed so they never fire twice.
func ptmDrainQueries(carry []byte, cols, rows, row, col int) (reply []byte, leftover []byte) {
	var out bytes.Buffer
	for {
		bestIdx, bestSpec := -1, ptmQuerySpec{}
		for _, q := range ptmQuerySpecs {
			if i := bytes.Index(carry, q.pat); i >= 0 && (bestIdx == -1 || i < bestIdx) {
				bestIdx, bestSpec = i, q
			}
		}
		if bestIdx == -1 {
			break
		}
		out.WriteString(bestSpec.reply(cols, rows, row, col))
		carry = carry[bestIdx+len(bestSpec.pat):]
	}
	if len(carry) > ptmLongestQueryPat {
		carry = carry[len(carry)-ptmLongestQueryPat:]
	}
	return out.Bytes(), carry
}
