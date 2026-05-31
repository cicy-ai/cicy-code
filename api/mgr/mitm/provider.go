package mitm

import "strings"

// ProviderFromHost maps an upstream host to a cicy-code provider id.
// Returns "unknown" for hosts we don't recognize (still audited, just
// not classified for token/cost extraction).
func ProviderFromHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	switch {
	case strings.HasSuffix(host, "anthropic.com"):
		return "anthropic"
	case strings.HasSuffix(host, "openai.com"):
		return "openai"
	case strings.HasSuffix(host, "deepseek.com"):
		return "openai" // deepseek uses OpenAI-compatible chat completions
	case strings.HasSuffix(host, "opencode.ai"):
		return "openai" // OpenCode Zen — OpenAI-compatible chat completions
	case strings.HasSuffix(host, "googleapis.com"):
		return "google"
	case strings.HasSuffix(host, "x.ai"):
		return "openai"
	default:
		return "unknown"
	}
}
