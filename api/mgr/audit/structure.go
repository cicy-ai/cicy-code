package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
)

// structure.go makes the scanner content-aware.
//
// The rules in builtin_rules.go regex-match over the whole request body as one
// flat byte blob. For AI traffic that body is structured JSON (messages, tool
// results, system prompt) interleaved with provider PROTOCOL fields — most
// importantly Anthropic's extended-thinking `signature` blobs, which are long
// base64 strings echoed back every turn. A flat scan flags those signatures
// (and base64 image data) as "high-entropy secrets", producing a flood of
// false positives that buries real findings.
//
// indexJSONStrings walks the body once and records, for every JSON string
// VALUE, its byte range in the ORIGINAL payload plus its dotted path. The
// scanner then (a) drops any match that lands inside a known-benign field
// (signature / image data) and (b) tags surviving matches with their field
// path so a human or the policy agent can actually triage them.
//
// Byte ranges are into the original payload, so span offsets — and the
// encrypted pre-redact archive that relies on them — are untouched.

type jsonStrRange struct {
	start int    // byte offset of the value token in the original payload
	end   int    // byte offset just past it
	key   string // the object key this value sits under ("" inside arrays)
	path  string // dotted path, e.g. messages[2].content[0].signature
}

type jsonFrame struct {
	isArray bool
	wantKey bool   // object only: next token is a key, not a value
	key     string // object: current key
	idx     int    // array: current index
	prefix  string // full path to this container
}

func curSeg(f *jsonFrame) string {
	if f == nil {
		return ""
	}
	if f.isArray {
		return "[" + strconv.Itoa(f.idx) + "]"
	}
	return "." + f.key
}

// indexJSONStrings returns the range+path of every string value, or nil if the
// payload is not valid JSON (in which case the caller falls back to a plain
// flat scan — behaviour unchanged from before this file existed).
func indexJSONStrings(payload []byte) []jsonStrRange {
	if len(payload) == 0 || (payload[0] != '{' && payload[0] != '[') {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var stack []*jsonFrame
	out := make([]jsonStrRange, 0, 64)

	// advance marks "a value just completed in the top frame".
	advance := func() {
		if len(stack) == 0 {
			return
		}
		top := stack[len(stack)-1]
		if top.isArray {
			top.idx++
		} else {
			top.wantKey = true
		}
	}

	for {
		off0 := dec.InputOffset()
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil // malformed / not JSON — bail to flat scan
		}
		off1 := dec.InputOffset()

		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{', '[':
				var prefix string
				if len(stack) > 0 {
					top := stack[len(stack)-1]
					prefix = top.prefix + curSeg(top)
				}
				f := &jsonFrame{isArray: t == '[', wantKey: t == '{', prefix: prefix}
				stack = append(stack, f)
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				advance() // the container itself was a value in its parent
			}
		default:
			if len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			if !top.isArray && top.wantKey {
				// object key
				if s, ok := t.(string); ok {
					top.key = s
				}
				top.wantKey = false
				continue
			}
			// a value
			if s, ok := t.(string); ok {
				_ = s
				out = append(out, jsonStrRange{
					start: int(off0),
					end:   int(off1),
					key:   keyOf(top),
					path:  top.prefix + curSeg(top),
				})
			}
			advance()
		}
	}
	return out
}

func keyOf(f *jsonFrame) string {
	if f.isArray {
		return ""
	}
	return f.key
}

// byteSeg is a half-open [lo,hi) byte range of the payload to scan.
type byteSeg struct{ lo, hi int }

// scanSegments returns the byte ranges a rule should scan: the whole payload
// MINUS the benign protocol fields (Anthropic signatures, image base64). Those
// blobs are the bulk of an AI request body, so skipping them — instead of
// regex-scanning megabytes of base64 only to discard the matches — is both
// faster and noise-free. Offsets stay global (each segment carries its base),
// so span offsets and the pre-redact archive are unaffected. Returns the whole
// payload as one segment for non-JSON bodies (ranges == nil) or when nothing is
// benign, preserving the original behaviour exactly.
func scanSegments(ranges []jsonStrRange, payload []byte) []byteSeg {
	whole := []byteSeg{{0, len(payload)}}
	if ranges == nil {
		return whole
	}
	benign := make([]jsonStrRange, 0, 8)
	for _, r := range ranges {
		if isBenignField(r, payload) {
			benign = append(benign, r)
		}
	}
	if len(benign) == 0 {
		return whole
	}
	sort.Slice(benign, func(i, j int) bool { return benign[i].start < benign[j].start })
	segs := make([]byteSeg, 0, len(benign)+1)
	cur := 0
	for _, b := range benign {
		if b.start > cur {
			segs = append(segs, byteSeg{cur, b.start})
		}
		if b.end > cur {
			cur = b.end
		}
	}
	if cur < len(payload) {
		segs = append(segs, byteSeg{cur, len(payload)})
	}
	if len(segs) == 0 {
		return whole
	}
	return segs
}

// bufSeg maps a region of the concatenated scan buffer back to the original
// payload: buffer[bufStart : bufStart+length] == payload[globalStart : globalStart+length].
type bufSeg struct{ bufStart, globalStart, length int }

// scanSep separates concatenated segments. Four newlines is low-entropy and
// not part of any secret/PII pattern, so it neither creates spurious matches
// nor lets a high-entropy run bridge two originally-separate regions.
const scanSep = "\n\n\n\n"

// buildScanBuffer concatenates the segments into ONE buffer so each rule scans
// once (instead of once per segment, ~hundreds of Detect calls). Returns the
// buffer plus the map needed to translate match offsets back to global payload
// offsets. For a single whole-payload segment it returns the payload itself
// with an identity map — zero copy, behaviour identical to a plain scan.
func buildScanBuffer(segments []byteSeg, payload []byte) ([]byte, []bufSeg) {
	if len(segments) == 1 && segments[0].lo == 0 && segments[0].hi == len(payload) {
		return payload, []bufSeg{{0, 0, len(payload)}}
	}
	total := 0
	for _, s := range segments {
		total += s.hi - s.lo
	}
	total += len(scanSep) * (len(segments) - 1)
	buf := make([]byte, 0, total)
	segMap := make([]bufSeg, 0, len(segments))
	for i, s := range segments {
		if i > 0 {
			buf = append(buf, scanSep...)
		}
		segMap = append(segMap, bufSeg{bufStart: len(buf), globalStart: s.lo, length: s.hi - s.lo})
		buf = append(buf, payload[s.lo:s.hi]...)
	}
	return buf, segMap
}

// mapToGlobal translates a [localStart,localEnd) match in the scan buffer back
// to global payload offsets. ok=false when the match starts in a separator or
// crosses a segment boundary (rejected — a real secret never spans two
// originally-disjoint regions).
func mapToGlobal(localStart, localEnd int, segMap []bufSeg) (gStart, gEnd int, ok bool) {
	i := sort.Search(len(segMap), func(i int) bool {
		return segMap[i].bufStart+segMap[i].length > localStart
	})
	if i >= len(segMap) {
		return 0, 0, false
	}
	seg := segMap[i]
	if localStart < seg.bufStart || localEnd-seg.bufStart > seg.length {
		return 0, 0, false
	}
	return seg.globalStart + (localStart - seg.bufStart), seg.globalStart + (localEnd - seg.bufStart), true
}

// isBenignField decides whether a string value is a provider PROTOCOL field
// that must never be treated as a user secret.
func isBenignField(r jsonStrRange, payload []byte) bool {
	switch r.key {
	case "signature":
		// Anthropic extended-thinking block signature (base64, echoed back).
		return true
	case "data":
		// Anthropic image block: content[].source.data (base64 image bytes).
		if strings.Contains(r.path, ".source") && (r.end-r.start) > 256 {
			return true
		}
	case "url":
		// OpenAI-style image_url: "data:image/...;base64,...."
		if r.end-r.start > 0 && bytes.Contains(payload[r.start:r.end], []byte("data:image")) {
			return true
		}
	}
	return false
}

// applyStructure drops matches that fall inside benign protocol fields and tags
// the survivors with their JSON path. When ranges is nil (non-JSON body) the
// spans are returned unchanged.
func applyStructure(spans []Span, ranges []jsonStrRange, payload []byte) []Span {
	if ranges == nil || len(spans) == 0 {
		return spans
	}
	out := make([]Span, 0, len(spans))
	for _, sp := range spans {
		var path string
		lo, hi, found := 0, 0, false
		benign := false
		for _, r := range ranges {
			if sp.Start >= r.start && sp.Start < r.end {
				path = r.path
				lo, hi, found = r.start, r.end, true
				if isBenignField(r, payload) {
					benign = true
				}
				break
			}
		}
		if benign {
			continue
		}
		sp.Path = path
		if found {
			sp.Context = matchContext(payload, sp, lo, hi)
		}
		out = append(out, sp)
	}
	return out
}

const ctxRadius = 40

// matchContext returns a short, sanitized window of the field value around the
// match, with the matched bytes themselves masked. lo/hi bound the containing
// JSON string value so the window never bleeds into neighbouring fields.
func matchContext(payload []byte, sp Span, lo, hi int) string {
	if sp.Start < 0 || sp.End > len(payload) || sp.Start > sp.End || lo < 0 || hi > len(payload) {
		return ""
	}
	start := sp.Start - ctxRadius
	if start < lo {
		start = lo
	}
	end := sp.End + ctxRadius
	if end > hi {
		end = hi
	}
	var b strings.Builder
	if start > lo {
		b.WriteString("…")
	}
	b.Write(payload[start:sp.Start])
	b.WriteString(maskPreview(string(payload[sp.Start:sp.End])))
	b.Write(payload[sp.End:end])
	if end < hi {
		b.WriteString("…")
	}
	return sanitizeContext(b.String())
}

func sanitizeContext(s string) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			b.WriteByte(' ')
		case r < 0x20:
			// drop other control chars
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 220 {
		out = out[:220] + "…"
	}
	return out
}
