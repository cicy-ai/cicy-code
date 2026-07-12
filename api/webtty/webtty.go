// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package webtty

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

var mouseRe = regexp.MustCompile(`\x1b\[<[\d;]*[Mm]|\x1b\[M[\s\S]{3}`)
var daResponseRe = regexp.MustCompile(`\x1b\[[\?>][\d;]*c`)

// Slave-output coalescing: the pty delivers bursts as many small reads; pushing
// each read as its own WS frame (old behavior: 1KB per frame) costs a frame +
// base64 + term.write() per KB on the client. Batch reads and flush at most
// every coalesceFlushInterval (or immediately once coalesceFlushBytes piled
// up). 16ms is one display frame — echo latency stays imperceptible.
const (
	coalesceFlushInterval = 16 * time.Millisecond
	coalesceFlushBytes    = 8 * 1024
	slaveReadBufferSize   = 32 * 1024
)

// Private-mode (DECSET/DECRST) params stripped from the stream before it
// reaches the web viewer:
//
//   - 47/1047/1049 (alternate screen): tmux attach switches the client into
//     the alt screen, where xterm.js has NO scrollback — client-side scroll
//     history only works on the normal buffer. tmux still believes the mode
//     switch happened; the byte stream is otherwise identical.
//   - 1000/1001/1002/1003/1005/1006/1015/1016 (mouse tracking): with tmux
//     `mouse on`, attach asks the viewer terminal to report mouse events —
//     xterm.js then forwards wheel to tmux (→ copy-mode) instead of scrolling
//     its local buffer. Strip the enable so wheel stays client-side. The
//     master input path already drops any stray mouse reports (mouseRe).
//
// Everything else (cursor keys, bracketed paste 2004, focus events 1004, …)
// passes through untouched. Local tmux clients are unaffected — this filter
// lives in the web-viewer path only.
var privateModeRe = regexp.MustCompile(`\x1b\[\?([0-9;]+)([hl])`)

var viewerStrippedModes = map[string]bool{
	"47": true, "1047": true, "1049": true,
	"1000": true, "1001": true, "1002": true, "1003": true,
	"1005": true, "1006": true, "1015": true, "1016": true,
}

func stripViewerPrivateModes(data []byte) []byte {
	if !bytes.Contains(data, []byte("\x1b[?")) {
		return data
	}
	return privateModeRe.ReplaceAllFunc(data, func(seq []byte) []byte {
		m := privateModeRe.FindSubmatch(seq)
		params := strings.Split(string(m[1]), ";")
		kept := params[:0]
		for _, p := range params {
			if !viewerStrippedModes[p] {
				kept = append(kept, p)
			}
		}
		if len(kept) == len(params) {
			return seq
		}
		if len(kept) == 0 {
			return nil
		}
		return []byte("\x1b[?" + strings.Join(kept, ";") + string(m[2]))
	})
}

// WebTTY bridges a PTY slave and its PTY master.
// To support text-based streams and side channel commands such as
// terminal resizing, WebTTY uses an original protocol.
type WebTTY struct {
	// PTY Master, which probably a connection to browser
	masterConn Master
	// PTY Slave
	slave Slave

	windowTitle []byte
	permitWrite bool
	columns     int
	rows        int
	reconnect   int // in seconds
	masterPrefs []byte
	// initialOutput is written to the master as the first Output frame,
	// before any slave bytes — the attach-time backfill (e.g. tmux
	// capture-pane history) that seeds the viewer's local scrollback.
	initialOutput []byte

	bufferSize int
	writeMutex sync.Mutex
}

// New creates a new instance of WebTTY.
// masterConn is a connection to the PTY master,
// typically it's a websocket connection to a client.
// slave is a PTY slave such as a local command with a PTY.
func New(masterConn Master, slave Slave, options ...Option) (*WebTTY, error) {
	wt := &WebTTY{
		masterConn: masterConn,
		slave:      slave,

		permitWrite: false,
		columns:     0,
		rows:        0,

		bufferSize: 1024,
	}

	for _, option := range options {
		option(wt)
	}

	return wt, nil
}

// Run starts the main process of the WebTTY.
// This method blocks until the context is canceled.
// Note that the master and slave are left intact even
// after the context is canceled. Closing them is caller's
// responsibility.
// If the connection to one end gets closed, returns ErrSlaveClosed or ErrMasterClosed.
func (wt *WebTTY) Run(ctx context.Context) error {
	err := wt.sendInitializeMessage()
	if err != nil {
		return errors.Wrapf(err, "failed to send initializing message")
	}

	if len(wt.initialOutput) > 0 {
		// 走 handleSlaveReadEvent,让回填经过与直播完全相同的过滤。
		if err := wt.handleSlaveReadEvent(wt.initialOutput); err != nil {
			return errors.Wrapf(err, "failed to send initial output")
		}
	}

	errs := make(chan error, 2)
	done := make(chan struct{})
	defer close(done)

	go func() {
		errs <- wt.slaveReadLoop(done)
	}()

	go func() {
		errs <- func() error {
			buffer := make([]byte, wt.bufferSize)
			for {
				n, err := wt.masterConn.Read(buffer)
				if err != nil {
					return ErrMasterClosed
				}

				err = wt.handleMasterReadEvent(buffer[:n])
				if err != nil {
					return err
				}
			}
		}()
	}()

	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-errs:
	}

	return err
}

func (wt *WebTTY) sendInitializeMessage() error {
	err := wt.masterWrite(append([]byte{SetWindowTitle}, wt.windowTitle...))
	if err != nil {
		return errors.Wrapf(err, "failed to send window title")
	}

	if wt.reconnect > 0 {
		reconnect, _ := json.Marshal(wt.reconnect)
		err := wt.masterWrite(append([]byte{SetReconnect}, reconnect...))
		if err != nil {
			return errors.Wrapf(err, "failed to set reconnect")
		}
	}

	if wt.masterPrefs != nil {
		err := wt.masterWrite(append([]byte{SetPreferences}, wt.masterPrefs...))
		if err != nil {
			return errors.Wrapf(err, "failed to set preferences")
		}
	}

	return nil
}

// slaveReadLoop pumps slave output to the master, coalescing small pty reads
// into ≤coalesceFlushInterval / ≥coalesceFlushBytes frames. done releases the
// inner reader goroutine if the loop exits early (e.g. master write failure).
func (wt *WebTTY) slaveReadLoop(done <-chan struct{}) error {
	type readResult struct {
		data []byte
		err  error
	}
	reads := make(chan readResult)
	go func() {
		buffer := make([]byte, slaveReadBufferSize)
		for {
			n, err := wt.slave.Read(buffer)
			var chunk []byte
			if n > 0 {
				chunk = append([]byte(nil), buffer[:n]...)
			}
			select {
			case reads <- readResult{chunk, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	pending := make([]byte, 0, coalesceFlushBytes*2)
	timer := time.NewTimer(coalesceFlushInterval)
	if !timer.Stop() {
		<-timer.C
	}
	armed := false // timer 只在首个待发字节时武装一次,稳定输出流下不无限顺延
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		err := wt.handleSlaveReadEvent(pending)
		pending = pending[:0]
		return err
	}
	for {
		select {
		case r := <-reads:
			if len(r.data) > 0 {
				pending = append(pending, r.data...)
			}
			if r.err != nil {
				_ = flush()
				return ErrSlaveClosed
			}
			if len(pending) >= coalesceFlushBytes {
				if armed {
					if !timer.Stop() {
						<-timer.C
					}
					armed = false
				}
				if err := flush(); err != nil {
					return err
				}
			} else if !armed && len(pending) > 0 {
				timer.Reset(coalesceFlushInterval)
				armed = true
			}
		case <-timer.C:
			armed = false
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

func (wt *WebTTY) handleSlaveReadEvent(data []byte) error {
	// Filter DA response sequences (e.g. \x1b[?0;276;0c)
	data = daResponseRe.ReplaceAll(data, nil)
	// Keep the web viewer out of alt-screen / tmux mouse-tracking (see
	// stripViewerPrivateModes) so its local scrollback + wheel keep working.
	data = stripViewerPrivateModes(data)
	if len(data) == 0 {
		return nil
	}
	safeMessage := base64.StdEncoding.EncodeToString(data)
	err := wt.masterWrite(append([]byte{Output}, []byte(safeMessage)...))
	if err != nil {
		return errors.Wrapf(err, "failed to send message to master")
	}

	return nil
}

func (wt *WebTTY) masterWrite(data []byte) error {
	wt.writeMutex.Lock()
	defer wt.writeMutex.Unlock()

	_, err := wt.masterConn.Write(data)
	if err != nil {
		return errors.Wrapf(err, "failed to write to master")
	}

	return nil
}

func (wt *WebTTY) handleMasterReadEvent(data []byte) error {
	if len(data) == 0 {
		return errors.New("unexpected zero length read from master")
	}

	switch data[0] {
	case Input:
		if !wt.permitWrite {
			return nil
		}

		if len(data) <= 1 {
			return nil
		}

		// Filter mouse sequences (SGR: \e[<...M/m, X10: \e[M...)
		raw := data[1:]
		raw = mouseRe.ReplaceAll(raw, nil)
		// Filter DA responses from xterm.js (e.g. \x1b[?1;2c, \x1b[>0;276;0c)
		raw = daResponseRe.ReplaceAll(raw, nil)
		if len(raw) == 0 {
			return nil
		}
		if bytes.IndexByte(raw, '\r') >= 0 || bytes.IndexByte(raw, '\n') >= 0 {
			log.Printf("[webtty-input] len=%d raw=%q", len(raw), raw)
		}

		_, err := wt.slave.Write(raw)
		if err != nil {
			return errors.Wrapf(err, "failed to write received data to slave")
		}

	case Ping:
		err := wt.masterWrite([]byte{Pong})
		if err != nil {
			return errors.Wrapf(err, "failed to return Pong message to master")
		}

	case ResizeTerminal:
		if wt.columns != 0 && wt.rows != 0 {
			break
		}

		if len(data) <= 1 {
			return errors.New("received malformed remote command for terminal resize: empty payload")
		}

		var args argResizeTerminal
		err := json.Unmarshal(data[1:], &args)
		if err != nil {
			return errors.Wrapf(err, "received malformed data for terminal resize")
		}
		rows := wt.rows
		if rows == 0 {
			rows = int(args.Rows)
		}

		columns := wt.columns
		if columns == 0 {
			columns = int(args.Columns)
		}

		wt.slave.ResizeTerminal(columns, rows)
	default:
		return errors.Errorf("unknown message type `%c`", data[0])
	}

	return nil
}

type argResizeTerminal struct {
	Columns float64
	Rows    float64
}
