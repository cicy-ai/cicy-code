package main

import (
	"encoding/json"
	"log"
	"sync"
)

type HookFunc func(paneID string, old, new paneSt)

var (
	hooks   []HookFunc
	hooksMu sync.RWMutex
)

// RegisterHook 注册状态变化回调
func RegisterHook(fn HookFunc) {
	hooksMu.Lock()
	hooks = append(hooks, fn)
	hooksMu.Unlock()
}

// triggerHooks 触发所有 hook（内部调用）
func triggerHooks(paneID string, old, new paneSt) {
	hooksMu.RLock()
	list := make([]HookFunc, len(hooks))
	copy(list, hooks)
	hooksMu.RUnlock()

	for _, fn := range list {
		go func(f HookFunc) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[hook] panic: %v", r)
				}
			}()
			f(paneID, old, new)
		}(fn)
	}
}

// diffStatus 比较状态变化并触发 hook
func diffStatus(paneID string, oldRaw, newRaw json.RawMessage) {
	var old, new paneSt
	json.Unmarshal(oldRaw, &old)
	json.Unmarshal(newRaw, &new)

	changed := false
	if old.Status == nil && new.Status != nil {
		changed = true
	} else if old.Status != nil && new.Status != nil && *old.Status != *new.Status {
		changed = true
	}

	if changed {
		triggerHooks(paneID, old, new)
	}
}
