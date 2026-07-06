// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package webtty

import (
	"io"
)

// Master represents a PTY master, usually it's a websocket connection.
type Master io.ReadWriter
