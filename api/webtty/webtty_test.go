// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package webtty

// 原文件是 gotty 上游的遗留测试,用的是早已不存在的 New(conn)/TTY() API,
// 在本仓库从未编译通过(整个包因此从未被测过)。此处以现行 API 重写,保留
// 上游"slave 输出直达 master"的用例意图,并覆盖:输出合帧、viewer 私有模式
// 过滤(备用屏/鼠标上报)、attach 回填(InitialOutput)、DA 应答过滤。

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSlave:测试端往 feedW 写,webtty 从 Read 读到;webtty 写来的输入进 sink。
type fakeSlave struct {
	feedR *io.PipeReader
	feedW *io.PipeWriter
	mu    sync.Mutex
	sink  bytes.Buffer
}

func newFakeSlave() *fakeSlave {
	r, w := io.Pipe()
	return &fakeSlave{feedR: r, feedW: w}
}

func (s *fakeSlave) Read(p []byte) (int, error) { return s.feedR.Read(p) }
func (s *fakeSlave) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink.Write(p)
}
func (s *fakeSlave) WindowTitleVariables() map[string]interface{} { return nil }
func (s *fakeSlave) ResizeTerminal(columns, rows int) error       { return nil }

// fakeMaster 收集 webtty 写给浏览器侧的协议帧;Read 阻塞直到 readCh 关闭。
type fakeMaster struct {
	mu     sync.Mutex
	frames [][]byte
	readCh chan []byte
}

func newFakeMaster() *fakeMaster { return &fakeMaster{readCh: make(chan []byte)} }

func (m *fakeMaster) Read(p []byte) (int, error) {
	data, ok := <-m.readCh
	if !ok {
		return 0, io.EOF
	}
	return copy(p, data), nil
}

func (m *fakeMaster) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frames = append(m.frames, append([]byte(nil), p...))
	return len(p), nil
}

// outputFrames 解出全部 Output 帧的 base64 解码内容和帧数。
func (m *fakeMaster) outputFrames(t *testing.T) ([][]byte, int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out [][]byte
	for _, f := range m.frames {
		if len(f) == 0 || f[0] != Output {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(string(f[1:]))
		if err != nil {
			t.Fatalf("bad base64 in Output frame: %v", err)
		}
		out = append(out, decoded)
	}
	return out, len(out)
}

func (m *fakeMaster) combinedOutput(t *testing.T) string {
	frames, _ := m.outputFrames(t)
	var b strings.Builder
	for _, f := range frames {
		b.Write(f)
	}
	return b.String()
}

func runTestWebTTY(t *testing.T, slave Slave, master Master, opts ...Option) (stop func()) {
	t.Helper()
	wt, err := New(master, slave, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = wt.Run(ctx)
		close(done)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("webtty did not stop")
		}
	}
}

// waitOutput 轮询直到合并输出包含 want(合帧最多延迟 16ms,不能同步断言)。
func waitOutput(t *testing.T, m *fakeMaster, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(m.combinedOutput(t), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("output never contained %q; got %q", want, m.combinedOutput(t))
}

// ── slave 输出直达 master(上游用例的现代化版)────────────────────────────────

func TestSlaveOutputReachesMaster(t *testing.T) {
	fs, fm := newFakeSlave(), newFakeMaster()
	stop := runTestWebTTY(t, fs, fm)
	defer stop()

	if _, err := fs.feedW.Write([]byte("foobar")); err != nil {
		t.Fatalf("feed: %v", err)
	}
	waitOutput(t, fm, "foobar")
}

// ── 合帧:一次爆发的多个小块必须被合并成远少于块数的帧 ────────────────────────

func TestSlaveOutputCoalesced(t *testing.T) {
	fs, fm := newFakeSlave(), newFakeMaster()
	stop := runTestWebTTY(t, fs, fm)
	defer stop()

	const chunks = 50
	for i := 0; i < chunks; i++ {
		if _, err := fs.feedW.Write([]byte("x")); err != nil {
			t.Fatalf("feed: %v", err)
		}
	}
	waitOutput(t, fm, strings.Repeat("x", chunks))
	_, frames := fm.outputFrames(t)
	// 旧行为:每次 Read 一帧 → 50 帧。合帧后应落进极少数帧;阈值放宽到 10,
	// 容忍调度抖动。
	if frames > 10 {
		t.Fatalf("50 back-to-back 1-byte chunks produced %d Output frames — coalescing not working", frames)
	}
}

// ── viewer 模式过滤:备用屏/鼠标上报被剥掉,其他 DECSET 原样通过 ───────────────

func TestViewerModesStripped(t *testing.T) {
	fs, fm := newFakeSlave(), newFakeMaster()
	stop := runTestWebTTY(t, fs, fm)
	defer stop()

	// tmux attach 的典型序列:进备用屏 + 开鼠标,夹着正常输出和括号粘贴模式。
	fs.feedW.Write([]byte("A\x1b[?1049hB\x1b[?1000;1006hC\x1b[?2004hD\x1b[?1049lE"))
	waitOutput(t, fm, "E")
	got := fm.combinedOutput(t)
	for _, banned := range []string{"\x1b[?1049h", "\x1b[?1049l", "1000", "1006"} {
		if strings.Contains(got, banned) {
			t.Errorf("viewer stream still contains %q: %q", banned, got)
		}
	}
	if !strings.Contains(got, "\x1b[?2004h") {
		t.Errorf("bracketed-paste DECSET was wrongly stripped: %q", got)
	}
	// 去掉所有转义序列后,正文必须原样、按序保留。
	plain := privateModeRe.ReplaceAllString(got, "")
	if plain != "ABCDE" {
		t.Errorf("payload text mangled: %q (from %q)", plain, got)
	}
}

func TestStripViewerPrivateModesUnit(t *testing.T) {
	// 混合参数:1006(剥)+ 1004 焦点事件(留)在同一序列里。
	if got := string(stripViewerPrivateModes([]byte("\x1b[?1006;1004h"))); got != "\x1b[?1004h" {
		t.Fatalf("mixed DECSET: got %q, want \\x1b[?1004h", got)
	}
	// 纯鼠标参数 → 整条消失。
	if got := string(stripViewerPrivateModes([]byte("\x1b[?1000h"))); got != "" {
		t.Fatalf("pure mouse DECSET survived: %q", got)
	}
	// 无关序列原样保留(光标键应用模式 ?1、藏光标 ?25)。
	if got := string(stripViewerPrivateModes([]byte("\x1b[?1h\x1b[?25l"))); got != "\x1b[?1h\x1b[?25l" {
		t.Fatalf("unrelated DECSET touched: %q", got)
	}
}

// ── 回填:InitialOutput 先于任何 slave 输出到达,且经过同一套过滤 ──────────────

func TestInitialOutputPrecedesLiveAndIsFiltered(t *testing.T) {
	fs, fm := newFakeSlave(), newFakeMaster()
	stop := runTestWebTTY(t, fs, fm,
		WithInitialOutput([]byte("history-line-1\r\n\x1b[?1049hhistory-line-2\r\n")))
	defer stop()

	fs.feedW.Write([]byte("live-line"))
	waitOutput(t, fm, "live-line")

	frames, _ := fm.outputFrames(t)
	if len(frames) == 0 {
		t.Fatal("no output frames")
	}
	first := string(frames[0])
	if !strings.Contains(first, "history-line-1") {
		t.Errorf("first Output frame is not the backfill: %q", first)
	}
	if strings.Contains(first, "\x1b[?1049h") {
		t.Errorf("backfill bypassed the viewer-mode filter: %q", first)
	}
	all := fm.combinedOutput(t)
	if strings.Index(all, "history-line-2") > strings.Index(all, "live-line") {
		t.Errorf("backfill arrived after live output: %q", all)
	}
}

// ── DA 应答过滤保持原语义(6236c89 的适配不能被合帧改动拆掉)────────────────────

func TestDAResponseStillFiltered(t *testing.T) {
	fs, fm := newFakeSlave(), newFakeMaster()
	stop := runTestWebTTY(t, fs, fm)
	defer stop()

	fs.feedW.Write([]byte("before\x1b[?0;276;0cafter"))
	waitOutput(t, fm, "after")
	if got := fm.combinedOutput(t); strings.Contains(got, "\x1b[?0;276;0c") || !strings.Contains(got, "beforeafter") {
		t.Fatalf("DA response filtering broken: %q", got)
	}
}
