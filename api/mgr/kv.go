package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// KV is a simple key-value store with optional file persistence
type KV struct {
	mu   sync.RWMutex
	data map[string]string
	file string // empty = memory only
}

var kv *KV

func initKV() {
	path := os.Getenv("KV_PATH")
	// Local mode defaults to ~/.cicy/kv.json
	if path == "" && os.Getenv("SAAS_MODE") != "1" && os.Getenv("MYSQL_DSN") == "" {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".cicy")
		os.MkdirAll(dir, 0755)
		path = filepath.Join(dir, "kv.json")
	}
	kv = &KV{data: make(map[string]string), file: path}
	if path != "" {
		kv.load()
	}
}

func (k *KV) load() {
	data, err := os.ReadFile(k.file)
	if err != nil {
		return
	}
	json.Unmarshal(data, &k.data)
}

func (k *KV) save() {
	if k.file == "" {
		return
	}
	data, _ := json.Marshal(k.data)
	os.WriteFile(k.file, data, 0644)
}

func (k *KV) Get(key string) string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.data[key]
}

func (k *KV) Set(key, val string) {
	k.mu.Lock()
	k.data[key] = val
	k.save()
	k.mu.Unlock()
}

func (k *KV) Del(key string) {
	k.mu.Lock()
	delete(k.data, key)
	k.save()
	k.mu.Unlock()
}

// List operations (for queue)
func (k *KV) LPush(key, val string) {
	k.mu.Lock()
	var list []string
	if raw := k.data[key]; raw != "" {
		json.Unmarshal([]byte(raw), &list)
	}
	list = append([]string{val}, list...)
	data, _ := json.Marshal(list)
	k.data[key] = string(data)
	k.save()
	k.mu.Unlock()
}

func (k *KV) RPop(key string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	var list []string
	if raw := k.data[key]; raw != "" {
		json.Unmarshal([]byte(raw), &list)
	}
	if len(list) == 0 {
		return ""
	}
	val := list[len(list)-1]
	list = list[:len(list)-1]
	data, _ := json.Marshal(list)
	k.data[key] = string(data)
	k.save()
	return val
}

func (k *KV) LRange(key string, start, stop int) []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	var list []string
	if raw := k.data[key]; raw != "" {
		json.Unmarshal([]byte(raw), &list)
	}
	n := len(list)
	if n == 0 {
		return nil
	}
	// Handle negative indices (Redis style)
	if start < 0 {
		start = n + start
	}
	if stop < 0 {
		stop = n + stop
	}
	if start < 0 {
		start = 0
	}
	if stop >= n {
		stop = n - 1
	}
	if start > stop || start >= n {
		return nil
	}
	return list[start : stop+1]
}

// Pub/Sub (in-memory only)
type PubSub struct {
	mu   sync.RWMutex
	subs map[string][]chan string
}

var pubsub = &PubSub{subs: make(map[string][]chan string)}

func (p *PubSub) Subscribe(ch string) chan string {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := make(chan string, 100)
	p.subs[ch] = append(p.subs[ch], c)
	return c
}

func (p *PubSub) Unsubscribe(ch string, c chan string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	subs := p.subs[ch]
	for i, s := range subs {
		if s == c {
			p.subs[ch] = append(subs[:i], subs[i+1:]...)
			close(c)
			return
		}
	}
}

func (p *PubSub) Publish(ch, msg string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, c := range p.subs[ch] {
		select {
		case c <- msg:
		default: // drop if full
		}
	}
}
