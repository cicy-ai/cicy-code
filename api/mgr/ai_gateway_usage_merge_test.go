// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"testing"
)

// Anthropic reports usage as a CUMULATIVE SNAPSHOT, not as increments: the same
// input/cache counts appear on `message_start` AND again on `message_delta`
// (output_tokens grows to its final value there). Summing the two events —
// which aiGatewayMergeUsage used to do for every numeric key — double-counts
// every input bucket, so cost came out ~2× too high on every streamed request.
//
// Field-level guard: usage buckets merge by MAX (monotonic snapshot), never by +.
func anthropicStreamBody(inputTokens, cacheRead, cacheWrite, outputTokens int) []byte {
	return []byte(fmt.Sprintf(`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","model":"claude-opus-4-8","usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":1}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":%d}}

event: message_stop
data: {"type":"message_stop"}
`, inputTokens, cacheRead, cacheWrite, inputTokens, cacheRead, cacheWrite, outputTokens))
}

func TestStreamUsageNotDoubleCounted(t *testing.T) {
	// Realistic long-context turn: a big cache read, a small cache write.
	const (
		fresh      = 7
		cacheRead  = 773_501
		cacheWrite = 3_323
		output     = 1_646
	)
	// Anthropic's `input_tokens` on the wire is the FRESH count; cache buckets
	// are reported separately.
	parsed := aiGatewayParseStreamResponse(anthropicStreamBody(fresh, cacheRead, cacheWrite, output))

	for _, c := range []struct {
		key  string
		want int
	}{
		{"input_tokens", fresh},
		{"cache_read_input_tokens", cacheRead},
		{"cache_creation_input_tokens", cacheWrite},
		{"output_tokens", output},
	} {
		if got := aiGatewayInt(parsed.Usage[c.key]); got != c.want {
			t.Errorf("%s = %d, want %d (%.2f× — usage merged by + instead of max)",
				c.key, got, c.want, float64(got)/float64(c.want))
		}
	}
}

func TestStreamUsageStaysInsideContextWindow(t *testing.T) {
	// The whole prompt sits just under the 1M window. A merge that sums
	// message_start + message_delta reports ~2M input — physically impossible,
	// and exactly the fingerprint seen in production usage.jsonl.
	const window = 1_000_000
	parsed := aiGatewayParseStreamResponse(anthropicStreamBody(4, 981_474, 0, 900))

	total := aiGatewayInt(parsed.Usage["input_tokens"]) +
		aiGatewayInt(parsed.Usage["cache_read_input_tokens"]) +
		aiGatewayInt(parsed.Usage["cache_creation_input_tokens"])
	if total > window {
		t.Fatalf("total input = %d, exceeds the %d context window — impossible for one request", total, window)
	}
}

func TestStreamUsageCostNotInflated(t *testing.T) {
	// claude-opus-4-8: in $5 / out $25 / cache-read $0.5 / cache-write $6.25 per 1M.
	// The cache-write bucket dominates a cold turn, so a 2× inflation there is
	// the single biggest cost error.
	const (
		fresh      = 8
		cacheRead  = 19_228
		cacheWrite = 742_987
		output     = 1_646
	)
	parsed := aiGatewayParseStreamResponse(anthropicStreamBody(fresh, cacheRead, cacheWrite, output))

	got := aiGatewayEstimateCostCredit("claude-opus-4-8", parsed.Usage)
	want, _ := estimateModelCostTokens("claude-opus-4-8", fresh, cacheRead, cacheWrite, output)
	if got != want {
		t.Fatalf("cost = $%.4f, want $%.4f (%.2f× overcharge)", got, want, got/want)
	}
}

// A usage snapshot that arrives only once (OpenAI-style: usage on the final
// chunk) must survive the max-merge unchanged.
func TestStreamUsageSingleSnapshotUnchanged(t *testing.T) {
	body := []byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":1200,"output_tokens":340}}}

data: [DONE]
`)
	parsed := aiGatewayParseStreamResponse(body)
	if got := aiGatewayInt(parsed.Usage["input_tokens"]); got != 1200 {
		t.Errorf("input_tokens = %d, want 1200", got)
	}
	if got := aiGatewayInt(parsed.Usage["output_tokens"]); got != 340 {
		t.Errorf("output_tokens = %d, want 340", got)
	}
}
