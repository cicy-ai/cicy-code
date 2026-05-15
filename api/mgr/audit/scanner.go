package audit

import "time"

// Scanner produces findings for one payload.
//
// Walking skeleton: NoopScanner returns no findings. Phase 2 (Detection
// Coverage) will introduce the builtin 10-rule set plus dictionary and
// custom-rule support per docs/v1/audit-system-design.md §6.3 and §7.
type Scanner interface {
	Scan(payload []byte, direction string, policy *Policy) []Finding
}

type NoopScanner struct{}

func (NoopScanner) Scan(payload []byte, direction string, policy *Policy) []Finding {
	_ = payload
	_ = direction
	_ = policy
	return []Finding{}
}

// elapsedMs returns the milliseconds since start, clamped to int.
func elapsedMs(start time.Time) int {
	return int(time.Since(start) / time.Millisecond)
}
