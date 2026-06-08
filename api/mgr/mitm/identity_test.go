package mitm

import (
	"net"
	"testing"
)

type fakeAddr struct{ addr string }

func (f fakeAddr) Network() string { return "tcp" }
func (f fakeAddr) String() string  { return f.addr }

func TestInferIdentity_UsernameWinsFirst(t *testing.T) {
	rules := []IdentityRule{
		{Kind: "socks5_username"},
		{Kind: "fallback", Value: "fallback-{host}"},
	}
	id := InferIdentity(rules, fakeAddr{"1.2.3.4:5555"}, fakeAddr{"127.0.0.1:1085"}, "w-10042", "api.anthropic.com")
	if id.AgentID != "w-10042" {
		t.Fatalf("AgentID=%q, want w-10042", id.AgentID)
	}
	if id.ClientIP != "1.2.3.4" {
		t.Fatalf("ClientIP=%q, want 1.2.3.4", id.ClientIP)
	}
}

func TestInferIdentity_PortMap(t *testing.T) {
	rules := []IdentityRule{
		{Kind: "socks5_username"},
		{Kind: "port_map", Map: map[string]string{"20001": "w-1001", "20002": "w-10002"}},
		{Kind: "fallback", Value: "mitm:{host}"},
	}
	id := InferIdentity(rules, fakeAddr{"127.0.0.1:9999"}, fakeAddr{"127.0.0.1:20002"}, "", "api.openai.com")
	if id.AgentID != "w-10002" {
		t.Fatalf("port_map miss: %q", id.AgentID)
	}
}

func TestInferIdentity_FallbackTemplate(t *testing.T) {
	rules := []IdentityRule{
		{Kind: "fallback", Value: "mitm:{host}"},
	}
	id := InferIdentity(rules, fakeAddr{"127.0.0.1:9999"}, fakeAddr{"127.0.0.1:1085"}, "", "api.anthropic.com")
	if id.AgentID != "mitm:api.anthropic.com" {
		t.Fatalf("AgentID=%q, want mitm:api.anthropic.com", id.AgentID)
	}
}

func TestInferIdentity_NoRulesUsesDefaultFallback(t *testing.T) {
	id := InferIdentity(nil, fakeAddr{"127.0.0.1:9999"}, fakeAddr{"127.0.0.1:1085"}, "", "api.anthropic.com")
	if id.AgentID != "mitm:api.anthropic.com" {
		t.Fatalf("default fallback broken: %q", id.AgentID)
	}
}

func TestInferIdentity_IPv6ClientAddr(t *testing.T) {
	rules := []IdentityRule{{Kind: "fallback", Value: "mitm:{host}"}}
	id := InferIdentity(rules, &net.TCPAddr{IP: net.ParseIP("::1"), Port: 5000}, fakeAddr{"127.0.0.1:1085"}, "", "x.test")
	if id.ClientIP != "::1" {
		t.Fatalf("IPv6 ClientIP=%q", id.ClientIP)
	}
}

func TestConfig_IsWhitelisted(t *testing.T) {
	c := &Config{Hosts: HostsConfig{Whitelist: []string{"api.anthropic.com", "API.OPENAI.com"}}}
	if !c.IsWhitelisted("api.anthropic.com") {
		t.Fatal("missed exact match")
	}
	if !c.IsWhitelisted("api.openai.com:443") {
		t.Fatal("missed host:port form")
	}
	if c.IsWhitelisted("github.com") {
		t.Fatal("false positive")
	}
}

func TestConfig_Defaults(t *testing.T) {
	c := &Config{}
	c.applyDefaults()
	if c.SOCKS5Listen != "127.0.0.1:1085" {
		t.Fatalf("SOCKS5Listen default: %q", c.SOCKS5Listen)
	}
	if c.CA.LeafCacheSize != 1024 {
		t.Fatalf("LeafCacheSize default: %d", c.CA.LeafCacheSize)
	}
	if c.Node.MaxHops != 10 {
		t.Fatalf("MaxHops default: %d", c.Node.MaxHops)
	}
	if len(c.Hosts.Whitelist) == 0 {
		t.Fatal("whitelist should have defaults")
	}
}

func TestConfig_ValidateChainRequiresFields(t *testing.T) {
	c := &Config{Enabled: true, Upstream: UpstreamConfig{Mode: "chain"}}
	c.applyDefaults()
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for chain without chain.next_hop")
	}
}

func TestProviderFromHost(t *testing.T) {
	cases := map[string]string{
		"api.anthropic.com":                 "anthropic",
		"api.openai.com":                    "openai",
		"api.deepseek.com":                  "openai",
		"generativelanguage.googleapis.com": "google",
		"github.com":                        "unknown",
		"":                                  "unknown",
	}
	for host, want := range cases {
		if got := ProviderFromHost(host); got != want {
			t.Errorf("ProviderFromHost(%q)=%q, want %q", host, got, want)
		}
	}
}
