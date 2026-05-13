package voicecmd

import (
	"encoding/json"
	"strings"
)

func PrettyJSON(data []byte) string {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return trimmed
	}
	formatted, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return trimmed
	}
	return string(formatted)
}
