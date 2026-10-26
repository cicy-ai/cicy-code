package main

import (
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Kiro's service throttles per account. When several kiro agents share one
// login, turns end with
//
//	The request was throttled by the service (request_id: <uuid>)
//
// and kiro-cli does not retry: the pane just sits at its prompt. This loop
// notices that and sends a "continue" prompt with exponential backoff, at most
// once per request id, staggered across panes so the retries themselves don't
// trip the limit again. CICY_KIRO_THROTTLE_RETRY=0 turns it off.

const (
	kiroThrottleScanInterval = 15 * time.Second
	kiroThrottleBaseDelay    = 30 * time.Second
	kiroThrottleMaxDelay     = 5 * time.Minute
	kiroThrottleMaxRetries   = 6
	kiroThrottleStagger      = 20 * time.Second
	// A pane counts as recovered (backoff reset) once no throttle has shown
	// for this long; right after a retry the old error scrolls away while the
	// next turn may well be throttled again.
	kiroThrottleRecovered = 10 * time.Minute
	// The throttle line must be among the pane's last N non-empty lines; below
	// it kiro only prints Credits, the task counter, its footer and the prompt.
	kiroThrottleTailLines = 14
	kiroThrottleRetryText = "继续（上一轮因 Kiro 服务限流中断，请从中断处接着做）"
)

// Whitespace is stripped before matching: the message wraps at pane width,
// anywhere, including inside "request_id:".
var kiroThrottleRE = regexp.MustCompile(`Therequestwasthrottledbytheservice\(request_id:([0-9A-Za-z-]{8,64})\)`)

var kiroWhitespaceRE = regexp.MustCompile(`\s+`)

// kiroThrottleID returns the request id of a throttle error that is the last
// thing in the captured pane tail, or "".
func kiroThrottleID(capture string) string {
	var lines []string
	for _, l := range strings.Split(ansiRe.ReplaceAllString(capture, ""), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > kiroThrottleTailLines {
		lines = lines[len(lines)-kiroThrottleTailLines:]
	}
	flat := kiroWhitespaceRE.ReplaceAllString(strings.Join(lines, ""), "")
	m := kiroThrottleRE.FindAllStringSubmatch(flat, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

type kiroThrottlePane struct {
	id        string    // request id currently pending
	seen      time.Time // when that id was first seen
	last      time.Time // last scan that showed any throttle
	retries   int       // consecutive throttles answered on this pane
	handled   map[string]bool
	givenUpID string
}

type kiroThrottleRetrier struct {
	mu       sync.Mutex
	panes    map[string]*kiroThrottlePane
	lastSent time.Time
}

func newKiroThrottleRetrier() *kiroThrottleRetrier {
	return &kiroThrottleRetrier{panes: map[string]*kiroThrottlePane{}}
}

func kiroThrottleDelay(retries int) time.Duration {
	d := kiroThrottleBaseDelay
	for i := 0; i < retries && d < kiroThrottleMaxDelay; i++ {
		d *= 2
	}
	if d > kiroThrottleMaxDelay {
		d = kiroThrottleMaxDelay
	}
	return d
}

// observe feeds one pane's current throttle id ("" = none) and reports whether
// a retry should be sent now.
func (r *kiroThrottleRetrier) observe(pane, id string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.panes[pane]
	if p == nil {
		p = &kiroThrottlePane{handled: map[string]bool{}}
		r.panes[pane] = p
	}
	if id == "" {
		p.id = ""
		if now.Sub(p.last) >= kiroThrottleRecovered {
			p.retries, p.givenUpID = 0, ""
		}
		return false
	}
	p.last = now
	if p.handled[id] || p.givenUpID == id {
		return false
	}
	if p.id != id {
		p.id, p.seen = id, now
	}
	if p.retries >= kiroThrottleMaxRetries {
		p.givenUpID = id
		log.Printf("[kiro-throttle] %s: still throttled after %d retries, leaving it to the user (request_id=%s)", pane, p.retries, id)
		return false
	}
	if now.Sub(p.seen) < kiroThrottleDelay(p.retries) || now.Sub(r.lastSent) < kiroThrottleStagger {
		return false
	}
	p.handled[id] = true
	p.retries++
	r.lastSent = now
	return true
}

func kiroThrottleRetryEnabled() bool {
	v := strings.TrimSpace(os.Getenv("CICY_KIRO_THROTTLE_RETRY"))
	return v != "0" && !strings.EqualFold(v, "false")
}

func activeKiroPanes() []string {
	rows, err := store.Query("SELECT pane_id, COALESCE(agent_type,'') FROM agent_config WHERE active=1")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var pid, atype string
		if rows.Scan(&pid, &atype) == nil && normalizeAgentType(atype) == "kiro-cli" {
			out = append(out, normPaneID(pid))
		}
	}
	return out
}

func startKiroThrottleRetry() {
	if !kiroThrottleRetryEnabled() {
		log.Printf("[kiro-throttle] disabled (CICY_KIRO_THROTTLE_RETRY=0)")
		return
	}
	r := newKiroThrottleRetrier()
	for {
		time.Sleep(kiroThrottleScanInterval)
		for _, pane := range activeKiroPanes() {
			if _, err := runTmux("has-session", "-t", strings.Split(pane, ":")[0]); err != nil {
				continue
			}
			capture, err := runTmux("capture-pane", "-p", "-t", pane, "-S", "-60")
			if err != nil {
				continue
			}
			id := kiroThrottleID(capture)
			if !r.observe(pane, id, time.Now()) {
				continue
			}
			log.Printf("[kiro-throttle] %s: throttled (request_id=%s) → sending continue", shortPaneID(pane), id)
			sendTextToPaneDeferred(pane, kiroThrottleRetryText)
		}
	}
}
