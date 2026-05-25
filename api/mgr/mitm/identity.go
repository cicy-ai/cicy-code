package mitm

import (
	"net"
	"strconv"
	"strings"
)

// Identity is what we attach to each turn so audit can attribute the
// request to an agent / human / fallback. Fields may be empty.
type Identity struct {
	AgentID  string
	ClientIP string
	Profile  string // mihomo listener name if inferable (e.g. chrome-profile-1)
}

// InferIdentity applies the rules in order and returns the first match.
// inboundLocalAddr is the local side of the SOCKS5 connection (used by
// port_map rules); username is the SOCKS5 username if any.
func InferIdentity(rules []IdentityRule, clientAddr net.Addr, inboundLocalAddr net.Addr, username string, host string) Identity {
	id := Identity{
		ClientIP: ipOnly(clientAddr),
	}
	for _, rule := range rules {
		switch rule.Kind {
		case "socks5_username":
			if username != "" {
				id.AgentID = username
				return id
			}
		case "port_map":
			port := portOnly(inboundLocalAddr)
			if port == "" {
				continue
			}
			if v, ok := rule.Map[port]; ok && v != "" {
				id.AgentID = v
				return id
			}
		case "client_ip":
			if v, ok := rule.Map[id.ClientIP]; ok && v != "" {
				id.AgentID = v
				return id
			}
		case "fallback":
			value := rule.Value
			if value == "" {
				value = "mitm:{host}"
			}
			id.AgentID = strings.ReplaceAll(value, "{host}", host)
			return id
		}
	}
	if id.AgentID == "" {
		id.AgentID = "mitm:" + host
	}
	return id
}

func ipOnly(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

func portOnly(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}
	if _, perr := strconv.Atoi(port); perr != nil {
		return ""
	}
	return port
}
