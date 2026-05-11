package main

import "testing"

func TestProxySettingsRoundTrip(t *testing.T) {
	cfg, err := mergeProxySettingsIntoConfigJSON("{}", &proxySettings{Password: "secret", Rule: "IN-USER,w-10001,proxy-a"})
	if err != nil {
		t.Fatalf("mergeProxySettingsIntoConfigJSON error: %v", err)
	}
	ps := extractProxySettingsFromConfigJSON(cfg)
	if ps == nil {
		t.Fatal("expected proxy settings")
	}
	if ps.Password != "secret" || ps.Rule != "IN-USER,w-10001,proxy-a" {
		t.Fatalf("unexpected proxy settings: %+v", ps)
	}
}

func TestProxySettingsClearsWhenEmpty(t *testing.T) {
	cfg, err := mergeProxySettingsIntoConfigJSON(`{"proxy":{"password":"secret","rule":"IN-USER,w-10001,proxy-a"}}`, nil)
	if err != nil {
		t.Fatalf("mergeProxySettingsIntoConfigJSON error: %v", err)
	}
	ps := extractProxySettingsFromConfigJSON(cfg)
	if ps != nil {
		t.Fatalf("expected nil proxy settings, got %+v", ps)
	}
}
