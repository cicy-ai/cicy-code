//go:build windows

package main

import "ttyd-go/server"

// customTTYFactory routes the web terminal to the native pty backend when it's
// active (so `tmux attach` is bypassed and the browser sees the live pane).
func customTTYFactory(target string) (server.Factory, bool) {
	return ptmTTYFactoryFor(target)
}
