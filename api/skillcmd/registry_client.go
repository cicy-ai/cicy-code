package skillcmd

// registry_client.go — multi-source resolution for the client side.
//
// The client may have several registries configured (public + per-team
// private). list/search merge across all of them; info/install/update resolve
// a skill to the highest-precedence source that has it. Precedence = source
// order in registries.json (later wins), so a private skill shadows a public
// one of the same name.

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// installParentDir picks the install parent dir for a skill based on the
// registry it came from (the source-based layout):
//   - public registry        → ~/cicy-ai/skills            (flat)
//   - own local (localhost)   → ~/cicy-ai/skills/private
//   - another team's registry → ~/cicy-ai/skills/team/<source-name>
func installParentDir(reg *Registry) string {
	if reg == nil {
		return skillsRoot()
	}
	base := strings.TrimRight(reg.BaseURL, "/")
	if reg.Name == publicSourceName || base == DefaultRegistry {
		return skillsRoot()
	}
	if isLocalhostURL(base) {
		return privateSkillsParent()
	}
	return teamSkillsParent(reg.Name)
}

// isLocalhostURL reports whether u points at this machine.
func isLocalhostURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	h := p.Hostname()
	return h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.")
}

// resolvedSkill is a catalog entry tagged with the source it came from.
type resolvedSkill struct {
	Summary SkillSummary
	Source  string // registry source name
}

// mergedCatalog queries every configured source and merges by skill name
// (later source wins). Unreachable sources are skipped with a warning. Returns
// entries sorted by name.
func mergedCatalog(q, cat, agent string) ([]resolvedSkill, error) {
	regs := clientRegistries()
	byName := map[string]resolvedSkill{}
	anyOK := false
	var firstErr error
	for _, reg := range regs {
		resp, err := reg.ListSkills(q, cat, agent, 0, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: registry %q unreachable: %v\n", reg.Name, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		anyOK = true
		for _, s := range resp.Skills {
			byName[s.Name] = resolvedSkill{Summary: s, Source: reg.Name} // later wins
		}
	}
	if !anyOK {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, nil
	}
	out := make([]resolvedSkill, 0, len(byName))
	for _, rs := range byName {
		out = append(out, rs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Summary.Name < out[j].Summary.Name })
	return out, nil
}

// splitSourceSkill parses "source/skill" into its parts. Without a slash the
// source is empty (resolve by precedence).
func splitSourceSkill(target string) (source, skill string) {
	if i := strings.Index(target, "/"); i > 0 {
		return target[:i], target[i+1:]
	}
	return "", target
}

// registryForSkill picks the source for a skill. If explicitSource is set it
// must match a configured source name; otherwise the highest-precedence source
// that has the skill wins.
func registryForSkill(name, explicitSource string) (*Registry, error) {
	regs := clientRegistries()
	if explicitSource != "" {
		for _, r := range regs {
			if r.Name == explicitSource {
				return r, nil
			}
		}
		return nil, fmt.Errorf("no registry source named %q (see: skill registry sources)", explicitSource)
	}
	// reverse order → first hit is the highest-precedence source
	var lastErr error
	for i := len(regs) - 1; i >= 0; i-- {
		if _, err := regs[i].GetDetail(name); err == nil {
			return regs[i], nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("skill %q not found in any registry: %w", name, lastErr)
	}
	return nil, fmt.Errorf("skill %q not found in any registry", name)
}
