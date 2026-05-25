// Package mitm implements a man-in-the-middle proxy that captures plaintext
// from non-cooperative AI clients (those that ignore ANTHROPIC_BASE_URL etc.)
// and feeds it into the existing audit pipeline.
//
// Architecture and rationale: see docs/v1/mitm-system-design.md.
//
// Single-node deployment is the default. Chain mode (upstream.mode=chain)
// is wired through the same code paths; see §5.10 of the design doc.
package mitm

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the runtime configuration loaded from
// ~/cicy-ai/mitm/config.json. A minimum valid config is {"enabled": true}
// — all other fields fall back to defaults.
type Config struct {
	Enabled      bool           `json:"enabled"`
	SOCKS5Listen string         `json:"socks5_listen"`
	CA           CAConfig       `json:"ca"`
	Hosts        HostsConfig    `json:"hosts"`
	Node         NodeConfig     `json:"node"`
	Upstream     UpstreamConfig `json:"upstream"`
	Identity     IdentityConfig `json:"identity"`
	Audit        AuditConfig    `json:"audit"`
}

// CAConfig controls the dynamic CA used to sign leaf certs.
type CAConfig struct {
	CertPath       string `json:"cert_path"`
	KeyPath        string `json:"key_path"`
	LeafCacheSize  int    `json:"leaf_cache_size"`
	LeafValidYears int    `json:"leaf_valid_years"`
}

// HostsConfig declares which hosts are intercepted vs passthrough.
type HostsConfig struct {
	Whitelist []string `json:"whitelist"`
}

// NodeConfig identifies this MITM instance in a chain. For single-node
// deployments, leave ID empty and FinalHop=true.
type NodeConfig struct {
	ID       string `json:"id"`
	FinalHop bool   `json:"final_hop"`
	MaxHops  int    `json:"max_hops"`
}

// UpstreamConfig controls how the MITM dials the next hop.
//   - "direct": dial real host directly (system CA pool).
//   - "mihomo": dial via mihomo SOCKS5 (still real host, system CA pool).
//   - "chain":  dial next MITM node's SOCKS5 port (trust_ca CA pool).
type UpstreamConfig struct {
	Mode         string         `json:"mode"`
	MihomoSOCKS5 string         `json:"mihomo_socks5,omitempty"`
	MihomoAuth   string         `json:"mihomo_auth,omitempty"`
	Chain        *ChainConfig   `json:"chain,omitempty"`
	DialTimeout  Duration       `json:"dial_timeout,omitempty"`
	TLSTimeout   Duration       `json:"tls_timeout,omitempty"`
}

// ChainConfig is the upstream.chain block (mode=chain only).
type ChainConfig struct {
	NextHop string   `json:"next_hop"`
	Auth    string   `json:"auth,omitempty"`
	TrustCA string   `json:"trust_ca"`
	Timeout Duration `json:"timeout,omitempty"`
}

// IdentityConfig describes the rules for inferring agent_id from a
// SOCKS5 connection. Rules are evaluated top-to-bottom; first match wins.
type IdentityConfig struct {
	Rules []IdentityRule `json:"rules"`
}

// IdentityRule is a single inference step. The Kind field selects which
// other fields are honored.
//
//   kind=socks5_username  → read SOCKS5 USERNAME field (RFC 1929)
//   kind=port_map         → look up Map[inboundPort]
//   kind=client_ip        → look up Map[clientIP]
//   kind=fallback         → use Value (supports {host} placeholder)
type IdentityRule struct {
	Kind  string            `json:"kind"`
	Map   map[string]string `json:"map,omitempty"`
	Value string            `json:"value,omitempty"`
}

// AuditConfig points at the on-disk audit history root. Defaults to
// ~/cicy-ai/workers, matching the existing ai_gateway_audit layout.
type AuditConfig struct {
	HistoryRoot string `json:"history_root"`
}

// Duration is a JSON-marshalable time.Duration. Marshals as e.g. "30s".
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Duration(d).String() + `"`), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("mitm: invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// DefaultConfigPath returns ~/cicy-ai/mitm/config.json.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cicy-ai", "mitm", "config.json"), nil
}

// LoadConfig reads and validates the config file. Returns a default
// (disabled) config if the file does not exist.
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		p, err := DefaultConfigPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	cfg := &Config{}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.applyDefaults()
			return cfg, nil
		}
		return nil, fmt.Errorf("mitm: read config: %w", err)
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("mitm: parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	home, _ := os.UserHomeDir()
	if c.SOCKS5Listen == "" {
		c.SOCKS5Listen = "127.0.0.1:1085"
	}
	if c.CA.CertPath == "" {
		c.CA.CertPath = filepath.Join(home, "cicy-ai", "db", "mitm-ca.crt")
	}
	if c.CA.KeyPath == "" {
		c.CA.KeyPath = filepath.Join(home, "cicy-ai", "db", "mitm-ca.key")
	}
	if c.CA.LeafCacheSize == 0 {
		c.CA.LeafCacheSize = 1024
	}
	if c.CA.LeafValidYears == 0 {
		c.CA.LeafValidYears = 1
	}
	if len(c.Hosts.Whitelist) == 0 {
		c.Hosts.Whitelist = []string{
			"api.anthropic.com",
			"api.openai.com",
			"api.deepseek.com",
			"generativelanguage.googleapis.com",
		}
	}
	if c.Node.MaxHops == 0 {
		c.Node.MaxHops = 10
	}
	if c.Node.ID == "" {
		hostname, _ := os.Hostname()
		c.Node.ID = "mitm-" + hostname
	}
	if c.Upstream.Mode == "" {
		c.Upstream.Mode = "direct"
	}
	if c.Upstream.DialTimeout == 0 {
		c.Upstream.DialTimeout = Duration(30 * time.Second)
	}
	if c.Upstream.TLSTimeout == 0 {
		c.Upstream.TLSTimeout = Duration(30 * time.Second)
	}
	if c.Upstream.Chain != nil && c.Upstream.Chain.Timeout == 0 {
		c.Upstream.Chain.Timeout = Duration(30 * time.Second)
	}
	if len(c.Identity.Rules) == 0 {
		c.Identity.Rules = []IdentityRule{
			{Kind: "socks5_username"},
			{Kind: "fallback", Value: "mitm:{host}"},
		}
	}
	if c.Audit.HistoryRoot == "" {
		c.Audit.HistoryRoot = filepath.Join(home, "cicy-ai", "workers")
	}
}

// Validate enforces invariants that LoadConfig won't catch from defaults.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	switch c.Upstream.Mode {
	case "direct", "mihomo", "chain":
	default:
		return fmt.Errorf("mitm: upstream.mode must be direct|mihomo|chain, got %q", c.Upstream.Mode)
	}
	if c.Upstream.Mode == "mihomo" && c.Upstream.MihomoSOCKS5 == "" {
		return fmt.Errorf("mitm: upstream.mode=mihomo requires mihomo_socks5")
	}
	if c.Upstream.Mode == "chain" {
		if c.Upstream.Chain == nil || c.Upstream.Chain.NextHop == "" {
			return fmt.Errorf("mitm: upstream.mode=chain requires upstream.chain.next_hop")
		}
		if c.Upstream.Chain.TrustCA == "" {
			return fmt.Errorf("mitm: upstream.mode=chain requires upstream.chain.trust_ca")
		}
	}
	if c.Node.MaxHops < 1 || c.Node.MaxHops > 32 {
		return fmt.Errorf("mitm: node.max_hops must be 1..32, got %d", c.Node.MaxHops)
	}
	for _, h := range c.Hosts.Whitelist {
		if strings.ContainsAny(h, "/?#") {
			return fmt.Errorf("mitm: hosts.whitelist must contain bare hosts, got %q", h)
		}
		if _, err := url.Parse("https://" + h); err != nil {
			return fmt.Errorf("mitm: invalid host %q: %w", h, err)
		}
	}
	return nil
}

// IsWhitelisted returns true iff the host (exact, case-insensitive) is in
// the MITM whitelist. host may include the :port suffix; it is stripped.
func (c *Config) IsWhitelisted(host string) bool {
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)
	for _, w := range c.Hosts.Whitelist {
		if strings.ToLower(w) == host {
			return true
		}
	}
	return false
}
