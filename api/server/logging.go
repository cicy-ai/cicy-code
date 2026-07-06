// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"os"
	"strings"
)

func ttyDebugEnabled() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("CICY_DEBUG_TTYD")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
