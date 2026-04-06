package server

import (
	"os"
	"strings"
)

func ttyDebugEnabled() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("CICY_DEBUG_TTYD")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
