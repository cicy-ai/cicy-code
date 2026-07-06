// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package homedir

import (
	"os"
)

func Expand(path string) string {
	if path[0:2] == "~/" {
		return os.Getenv("HOME") + path[1:]
	} else {
		return path
	}
}
