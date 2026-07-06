//go:build windows

// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package localcommand

import "context"

func contextBackground() context.Context { return context.Background() }
