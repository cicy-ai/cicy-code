//go:build !windows

package main

import "ttyd-go/server"

// customTTYFactory has no native backend off Windows — serveTTY uses tmux.
func customTTYFactory(target string) (server.Factory, bool) { return nil, false }
