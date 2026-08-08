package main

import "testing"

func TestMihomoAuthenticationForUser(t *testing.T) {
	data := []byte("authentication:\n  - 'w-1001:secret:with-colon'\n  - 'alice:other'\n")
	password, ok := mihomoAuthenticationForUser(data, "w-1001")
	if !ok || password != "secret:with-colon" {
		t.Fatalf("got password=%q ok=%t", password, ok)
	}
	if _, ok := mihomoAuthenticationForUser(data, "missing"); ok {
		t.Fatal("missing user must not receive credentials")
	}
}
