package mitm

import (
	"bufio"
	"encoding/base64"
	"net"
	"testing"
	"time"
)

func TestReadHTTPConnect(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	go func() {
		req := "CONNECT api.anthropic.com:443 HTTP/1.1\r\n" +
			"Host: api.anthropic.com:443\r\n" +
			"Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("w-10004:x")) + "\r\n" +
			"\r\n"
		_, _ = client.Write([]byte(req))
		// read the "200 Connection Established" so the server-side Write unblocks
		_, _ = bufio.NewReader(client).ReadString('\n')
	}()

	req, err := readHTTPConnect(server, 5*time.Second)
	if err != nil {
		t.Fatalf("readHTTPConnect: %v", err)
	}
	if req.Host != "api.anthropic.com" || req.Port != 443 {
		t.Fatalf("host/port = %s:%d, want api.anthropic.com:443", req.Host, req.Port)
	}
	if req.Username != "w-10004" {
		t.Fatalf("username = %q, want w-10004", req.Username)
	}
}

func TestParseProxyAuthUsername(t *testing.T) {
	cases := map[string]string{
		"Basic " + base64.StdEncoding.EncodeToString([]byte("w-10004:x")): "w-10004",
		"Basic " + base64.StdEncoding.EncodeToString([]byte("w-99:")):     "w-99",
		"Basic " + base64.StdEncoding.EncodeToString([]byte("solo")):      "solo",
		"Bearer xyz":                                                       "",
		"":                                                                 "",
	}
	for in, want := range cases {
		if got := parseProxyAuthUsername(in); got != want {
			t.Fatalf("parseProxyAuthUsername(%q) = %q, want %q", in, got, want)
		}
	}
}
