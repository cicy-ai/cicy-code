package mitm

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

func egressStartEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	return ln.Addr().String()
}

// egressStartMockSOCKS5 is a minimal no-auth SOCKS5 server (mimicking the local
// mihomo mixed port) that tunnels CONNECT to the real target and records the
// last target it was asked to reach.
func egressStartMockSOCKS5(t *testing.T) (addr string, lastTarget func() string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("socks5 listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var last string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				hdr := make([]byte, 2) // ver, nmethods
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, int(hdr[1]))); err != nil {
					return
				}
				if _, err := c.Write([]byte{socks5Version, authNone}); err != nil {
					return
				}
				head := make([]byte, 4) // ver, cmd, rsv, atyp
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				var host string
				switch head[3] {
				case atypIPv4:
					b := make([]byte, 4)
					if _, err := io.ReadFull(c, b); err != nil {
						return
					}
					host = net.IP(b).String()
				case atypDomain:
					l := make([]byte, 1)
					if _, err := io.ReadFull(c, l); err != nil {
						return
					}
					b := make([]byte, int(l[0]))
					if _, err := io.ReadFull(c, b); err != nil {
						return
					}
					host = string(b)
				default:
					return
				}
				pb := make([]byte, 2)
				if _, err := io.ReadFull(c, pb); err != nil {
					return
				}
				target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb))))
				mu.Lock()
				last = target
				mu.Unlock()
				// reply: success, bound addr 0.0.0.0:0
				if _, err := c.Write([]byte{socks5Version, 0x00, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
					return
				}
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer up.Close()
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	return ln.Addr().String(), func() string { mu.Lock(); defer mu.Unlock(); return last }
}

func egressRoundtrip(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo mismatch: %q", buf)
	}
}

// global egress ON → every DialTCP is tunneled through the (mock) mihomo
// socks5, regardless of the static upstream mode.
func TestDialerDynamicEgressRoutesThroughMihomo(t *testing.T) {
	backend := egressStartEcho(t)
	socks, lastTarget := egressStartMockSOCKS5(t)

	d, err := NewDialer(UpstreamConfig{Mode: "direct", DialTimeout: Duration(5 * time.Second)},
		func() (bool, string, string) { return true, socks, "" })
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	conn, err := d.DialTCP(context.Background(), backend)
	if err != nil {
		t.Fatalf("DialTCP via egress: %v", err)
	}
	egressRoundtrip(t, conn)
	if got := lastTarget(); got != backend {
		t.Fatalf("egress did not route through mihomo: socks target=%q want=%q", got, backend)
	}
}

// global egress OFF → DialTCP uses the static mode (direct) and never touches
// the socks5 proxy.
func TestDialerEgressDisabledGoesDirect(t *testing.T) {
	backend := egressStartEcho(t)
	socks, lastTarget := egressStartMockSOCKS5(t)

	d, err := NewDialer(UpstreamConfig{Mode: "direct", DialTimeout: Duration(5 * time.Second)},
		func() (bool, string, string) { return false, socks, "" })
	if err != nil {
		t.Fatalf("NewDialer: %v", err)
	}
	conn, err := d.DialTCP(context.Background(), backend)
	if err != nil {
		t.Fatalf("DialTCP direct: %v", err)
	}
	egressRoundtrip(t, conn)
	if got := lastTarget(); got != "" {
		t.Fatalf("disabled egress must not touch socks5, saw target=%q", got)
	}
}
