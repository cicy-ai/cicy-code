package main

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"testing"
)

func TestAiGatewayMaybeGunzipChinese(t *testing.T) {
	plain := []byte(`{"content":[{"type":"text","text":"看下为什么中文是乱码"}]}`)
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write(plain)
	zw.Close()
	gz := b.Bytes()

	// via Content-Encoding header
	h := http.Header{"Content-Encoding": []string{"gzip"}}
	if got := aiGatewayMaybeGunzip(h, gz); !bytes.Equal(got, plain) {
		t.Fatalf("header path: got %q", got)
	}
	// via magic bytes (no header)
	if got := aiGatewayMaybeGunzip(http.Header{}, gz); !bytes.Equal(got, plain) {
		t.Fatalf("magic path: got %q", got)
	}
	// plain passthrough unchanged
	if got := aiGatewayMaybeGunzip(http.Header{}, plain); !bytes.Equal(got, plain) {
		t.Fatalf("plain path changed body")
	}
	t.Logf("OK: gunzipped -> %s", aiGatewayMaybeGunzip(h, gz))
}
