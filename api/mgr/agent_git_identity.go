package main

import (
	"os"
	"strings"
)

// Every agent pane gets its own git identity and mailbox:
//
//	<agent>.<instance>@cicy-ai.com
//
// Mail to that address is caught by Cloudflare Email Routing on
// cicy-ai.com (catch-all) and stored by the cicy-agent-mail Worker; the
// agent reads it with `cicy-agent mail`. Using the same address as the git
// author lets an agent verify it on GitHub by itself. <instance> is the hub
// hostname label (limeng.hub.cicy-ai.com → limeng), so only hub-mode
// instances get one. CICY_AGENT_GIT_IDENTITY=0 turns the injection off.
const agentMailDomain = "cicy-ai.com"

// hubSelfLabel returns this instance's hub hostname label: the cached
// directory's self entry when present, else the hub's slug of the login name.
func hubSelfLabel(cred cicyCloudCredential) string {
	c := &hubDirectoryCache
	c.Lock()
	for _, inst := range c.instances {
		if inst.Self && inst.ProxyHost != "" {
			c.Unlock()
			return strings.ToLower(strings.SplitN(inst.ProxyHost, ".", 2)[0])
		}
	}
	c.Unlock()
	return hubSlugify(cred.TeamID)
}

// hubSlugify mirrors cicy-ws-hub's slugify: lower-case, runs of anything
// outside [a-z0-9] become one '-', trimmed, at most 40 chars.
func hubSlugify(name string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

func agentMailAddress(shortID string) string {
	agent := strings.ToLower(strings.TrimSpace(shortID))
	if agent == "" || strings.ContainsAny(agent, ".@ ") {
		return ""
	}
	cred, ok := loadCiCyCloudCredential()
	if !ok || cred.Mode != cicyCloudModeHub {
		return ""
	}
	label := hubSelfLabel(cred)
	if label == "" {
		return ""
	}
	return agent + "." + label + "@" + agentMailDomain
}

// agentGitIdentityEnv is the per-pane git author/committer. Env vars beat
// repo and global git config, so the identity holds in shared ~/projects
// checkouts too.
func agentGitIdentityEnv(shortID string) map[string]string {
	if v := strings.TrimSpace(os.Getenv("CICY_AGENT_GIT_IDENTITY")); v == "0" || strings.EqualFold(v, "false") {
		return nil
	}
	addr := agentMailAddress(shortID)
	if addr == "" {
		return nil
	}
	name := strings.SplitN(addr, "@", 2)[0]
	return map[string]string{
		"CICY_AGENT_EMAIL":    addr,
		"GIT_AUTHOR_NAME":     name,
		"GIT_AUTHOR_EMAIL":    addr,
		"GIT_COMMITTER_NAME":  name,
		"GIT_COMMITTER_EMAIL": addr,
	}
}
