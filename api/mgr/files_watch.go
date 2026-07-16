// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// Native files: fsnotify-driven file change WS push.
//
// Protocol (client → server):
//   {"type":"subscribe",  "path":"src/components"}
//   {"type":"unsubscribe","path":"src/components"}
//   {"type":"ping"}
//
// Server → client:
//   {"type":"created",  "path":"src/components/Foo.tsx"}
//   {"type":"modified", "path":"src/components/App.tsx","mtime":...}
//   {"type":"deleted",  "path":"src/components/Bar.tsx"}
//   {"type":"renamed",  "path":"new","old":"prev"}
//   {"type":"error",    "error":"..."}
//   {"type":"pong"}

import (
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

type fsWatchClient struct {
	conn       *websocket.Conn
	workspace  string
	watcher    *fsnotify.Watcher
	subscribed map[string]struct{} // workspace-relative dir paths
	writeMu    sync.Mutex
	done       chan struct{}
}

func (c *fsWatchClient) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.conn.WriteJSON(v)
}

func (c *fsWatchClient) sendError(msg string) {
	_ = c.writeJSON(map[string]string{"type": "error", "error": msg})
}

func handleFsWatch(w http.ResponseWriter, r *http.Request) {
	workspace, err := agentWorkspace(r.URL.Query().Get("agent_id"))
	if err != nil {
		fsErr(w, err)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		_ = conn.WriteJSON(map[string]string{"type": "error", "error": err.Error()})
		_ = conn.Close()
		return
	}
	c := &fsWatchClient{
		conn:       conn,
		workspace:  workspace,
		watcher:    watcher,
		subscribed: make(map[string]struct{}),
		done:       make(chan struct{}),
	}

	conn.SetReadLimit(8 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		return nil
	})

	go c.pumpEvents()
	go c.pingLoop()

	for {
		var msg struct {
			Type string `json:"type"`
			Path string `json:"path"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		switch msg.Type {
		case "subscribe":
			c.subscribe(msg.Path)
		case "unsubscribe":
			c.unsubscribe(msg.Path)
		case "ping":
			_ = c.writeJSON(map[string]string{"type": "pong"})
		}
	}

	close(c.done)
	_ = watcher.Close()
	_ = conn.Close()
}

func (c *fsWatchClient) subscribe(rel string) {
	abs, err := resolveReadPath(c.workspace, rel)
	if err != nil {
		c.sendError("subscribe_invalid_path:" + err.Error())
		return
	}
	key := filepath.ToSlash(rel)
	if _, ok := c.subscribed[key]; ok {
		return
	}
	if err := c.watcher.Add(abs); err != nil {
		c.sendError("watch_add_failed:" + err.Error())
		return
	}
	c.subscribed[key] = struct{}{}
}

func (c *fsWatchClient) unsubscribe(rel string) {
	abs, err := resolveReadPath(c.workspace, rel)
	if err != nil {
		return
	}
	key := filepath.ToSlash(rel)
	if _, ok := c.subscribed[key]; !ok {
		return
	}
	_ = c.watcher.Remove(abs)
	delete(c.subscribed, key)
}

func (c *fsWatchClient) pumpEvents() {
	for {
		select {
		case <-c.done:
			return
		case ev, ok := <-c.watcher.Events:
			if !ok {
				return
			}
			c.dispatch(ev)
		case err, ok := <-c.watcher.Errors:
			if !ok {
				return
			}
			c.sendError("watcher_error:" + err.Error())
		}
	}
}

func (c *fsWatchClient) dispatch(ev fsnotify.Event) {
	rel, err := filepath.Rel(c.workspace, ev.Name)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	// Filter our own .cicy-tmp atomic writes so saves don't appear as a
	// noisy created/deleted pair to subscribers.
	if strings.Contains(base, ".cicy-tmp-") || strings.HasSuffix(base, ".cicy-tmp") {
		return
	}
	payload := map[string]any{"path": rel}
	switch {
	case ev.Op&fsnotify.Create != 0:
		payload["type"] = "created"
	case ev.Op&fsnotify.Remove != 0:
		payload["type"] = "deleted"
	case ev.Op&fsnotify.Rename != 0:
		payload["type"] = "renamed"
	case ev.Op&fsnotify.Write != 0:
		payload["type"] = "modified"
	default:
		return
	}
	if err := c.writeJSON(payload); err != nil {
		log.Printf("[fs/watch] write err: %v", err)
	}
}

func (c *fsWatchClient) pingLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.writeMu.Lock()
			_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.writeMu.Unlock()
				return
			}
			c.writeMu.Unlock()
		}
	}
}

