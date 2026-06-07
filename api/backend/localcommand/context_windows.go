//go:build windows

package localcommand

import "context"

func contextBackground() context.Context { return context.Background() }
