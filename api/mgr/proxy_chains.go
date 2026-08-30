package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Chained proxies and group-wide latency tests.
//
// A chain is a mihomo `relay` group: traffic enters through the first hop and
// exits through the last (`proxies: [hop1, hop2, …, exit]`). It lives in
// proxy-groups, can be a member of any select group like a node, and is
// testable through the controller like any proxy.
//
//	GET    /api/proxy/chains                       → {chains:[{name,hops}]}   (also embedded in /api/proxy/nodes)
//	POST   /api/proxy/chains  {name,hops,groups?}  → create; joins every select group unless `groups` lists some
//	PUT    /api/proxy/chains  {name,newName?,hops} → replace the route / rename (groups follow)
//	DELETE /api/proxy/chains?name=…                → remove from proxy-groups and every group's members
//	POST   /api/proxy/group-test {group,url?}      → controller /group/<name>/delay: {results:{member:ms}, failed:[…]}

var chainNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.\-]{0,63}$`)

func isRelayGroup(g *yaml.Node) bool { return strings.EqualFold(mappingGet(g, "type"), "relay") }

func (d *mihomoDoc) chains() []M {
	out := []M{}
	if s := d.seq("proxy-groups", false); s != nil {
		for _, it := range s.Content {
			if it.Kind == yaml.MappingNode && isRelayGroup(it) {
				out = append(out, M{"name": mappingGet(it, "name"), "hops": seqStrings(mappingSeq(it, "proxies", false))})
			}
		}
	}
	return out
}

// validateChainHops checks that every hop is an existing node (not a group —
// nesting groups inside a relay is how cycles happen) and that hops are unique.
func (d *mihomoDoc) validateChainHops(hops []string) ([]string, error) {
	nodes := map[string]bool{}
	if s := d.seq("proxies", false); s != nil {
		for _, it := range s.Content {
			nodes[mappingGet(it, "name")] = true
		}
	}
	clean := make([]string, 0, len(hops))
	seen := map[string]bool{}
	for _, h := range hops {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if !nodes[h] {
			return nil, fmt.Errorf("hop %q is not a node", h)
		}
		if seen[h] {
			return nil, fmt.Errorf("hop %q repeated", h)
		}
		seen[h] = true
		clean = append(clean, h)
	}
	if len(clean) < 2 {
		return nil, fmt.Errorf("a chain needs at least 2 hops")
	}
	return clean, nil
}

func chainNode(name string, hops []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, h := range hops {
		seq.Content = append(seq.Content, scalar(h))
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		scalar("name"), scalar(name),
		scalar("type"), scalar("relay"),
		scalar("proxies"), seq,
	}}
}

// joinSelectGroups appends name to every non-relay group (or only `want`).
func (d *mihomoDoc) joinSelectGroups(name string, want map[string]bool) []string {
	joined := []string{}
	if s := d.seq("proxy-groups", false); s != nil {
		for _, g := range s.Content {
			gname := mappingGet(g, "name")
			if gname == "" || gname == name || isRelayGroup(g) || (len(want) > 0 && !want[gname] && !isMihomoProbeName(gname)) {
				continue
			}
			members := mappingSeq(g, "proxies", true)
			if !seqHas(members, name) {
				members.Content = append(members.Content, scalar(name))
				if !isMihomoProbeName(gname) {
					joined = append(joined, gname)
				}
			}
		}
	}
	return joined
}

func handleProxyChains(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		d, err := loadMihomoDoc()
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		J(w, M{"success": true, "chains": d.chains()})
	case http.MethodPost:
		handleProxyChainCreate(w, r)
	case http.MethodPut:
		handleProxyChainUpdate(w, r)
	case http.MethodDelete:
		handleProxyChainDelete(w, r)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleProxyChainCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string   `json:"name"`
		Hops   []string `json:"hops"`
		Groups []string `json:"groups"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if !chainNameRe.MatchString(name) || isMihomoBuiltinName(name) || isMihomoProbeName(name) {
		httpErr(w, 400, "invalid chain name")
		return
	}
	d, err := loadMihomoDoc()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if _, n := d.findProxy(name); n != nil || d.findGroup(name) != nil {
		httpErr(w, 409, fmt.Sprintf("%q already exists", name))
		return
	}
	hops, err := d.validateChainHops(req.Hops)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	gs := d.seq("proxy-groups", true)
	gs.Content = append(gs.Content, chainNode(name, hops))
	want := map[string]bool{}
	for _, g := range req.Groups {
		want[strings.TrimSpace(g)] = true
	}
	joined := d.joinSelectGroups(name, want)
	if err := d.save(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	J(w, M{"success": true, "chain": M{"name": name, "hops": hops}, "groups": joined, "mihomo": reloadMihomoAfterEdit()})
}

func handleProxyChainUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string   `json:"name"`
		NewName string   `json:"newName"`
		Hops    []string `json:"hops"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	old := strings.TrimSpace(req.Name)
	newName := strings.TrimSpace(req.NewName)
	if newName == "" {
		newName = old
	}
	if !chainNameRe.MatchString(newName) || isMihomoBuiltinName(newName) || isMihomoProbeName(newName) {
		httpErr(w, 400, "invalid chain name")
		return
	}
	d, err := loadMihomoDoc()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	g := d.findGroup(old)
	if g == nil || !isRelayGroup(g) {
		httpErr(w, 404, fmt.Sprintf("chain %q not found", old))
		return
	}
	if newName != old {
		if _, n := d.findProxy(newName); n != nil || d.findGroup(newName) != nil {
			httpErr(w, 409, fmt.Sprintf("%q already exists", newName))
			return
		}
	}
	hops, err := d.validateChainHops(req.Hops)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	fresh := chainNode(newName, hops)
	fresh.HeadComment = g.HeadComment
	*g = *fresh
	if newName != old {
		if s := d.seq("proxy-groups", false); s != nil {
			for _, other := range s.Content {
				if members := mappingSeq(other, "proxies", false); members != nil {
					seqRename(members, old, newName)
				}
			}
		}
	}
	if err := d.save(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	J(w, M{"success": true, "chain": M{"name": newName, "hops": hops}, "renamed": newName != old, "mihomo": reloadMihomoAfterEdit()})
}

func handleProxyChainDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	d, err := loadMihomoDoc()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	gs := d.seq("proxy-groups", false)
	idx := -1
	if gs != nil {
		for i, it := range gs.Content {
			if mappingGet(it, "name") == name && isRelayGroup(it) {
				idx = i
			}
		}
	}
	if idx < 0 {
		httpErr(w, 404, fmt.Sprintf("chain %q not found", name))
		return
	}
	for _, g := range gs.Content {
		members := mappingSeq(g, "proxies", false)
		if members == nil || isRelayGroup(g) || isMihomoProbeName(mappingGet(g, "name")) || !seqHas(members, name) {
			continue
		}
		if len(members.Content) <= 1 && len(seqStrings(mappingSeq(g, "use", false))) == 0 {
			httpErr(w, 409, fmt.Sprintf("%q is the only member of group %q — add another member first", name, mappingGet(g, "name")))
			return
		}
	}
	gs.Content = append(gs.Content[:idx], gs.Content[idx+1:]...)
	left := []string{}
	for _, g := range gs.Content {
		if members := mappingSeq(g, "proxies", false); members != nil && seqRemove(members, name) {
			if isMihomoProbeName(mappingGet(g, "name")) {
				if len(members.Content) == 0 {
					members.Content = append(members.Content, scalar("DIRECT"))
				}
				continue
			}
			left = append(left, mappingGet(g, "name"))
		}
	}
	if err := d.save(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	J(w, M{"success": true, "name": name, "left_groups": left, "mihomo": reloadMihomoAfterEdit()})
}

// handleProxyGroupTest — POST /api/proxy/group-test {group, url?}
// One controller call measures every member of the group; it never changes
// which member the group selects.
func handleProxyGroupTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		Group string `json:"group"`
		URL   string `json:"url"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	group := strings.TrimSpace(req.Group)
	if group == "" {
		httpErr(w, 400, "group required")
		return
	}
	target := strings.TrimSpace(req.URL)
	if target == "" {
		target = "https://www.gstatic.com/generate_204"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	q := url.Values{"url": {target}, "timeout": {"6000"}}
	reqURL := mihomoController() + "/group/" + url.PathEscape(group) + "/delay?" + q.Encode()
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if secret := readMihomoControllerSecret(); secret != "" {
		hreq.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		httpErr(w, 502, "mihomo controller unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httpErr(w, 502, fmt.Sprintf("mihomo controller status %d", resp.StatusCode))
		return
	}
	var delays map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&delays); err != nil {
		httpErr(w, 502, "parse: "+err.Error())
		return
	}
	// Members missing from the map failed the probe.
	failed := []string{}
	if m, err := mihomoGroupMembers(group); err == nil {
		for _, name := range m {
			if _, ok := delays[name]; !ok {
				failed = append(failed, name)
			}
		}
	}
	J(w, M{"success": true, "group": group, "url": target, "results": delays, "failed": failed})
}

// mihomoGroupMembers reads a group's live member list from the controller.
func mihomoGroupMembers(group string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, mihomoController()+"/proxies/"+url.PathEscape(group), nil)
	if secret := readMihomoControllerSecret(); secret != "" {
		hreq.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		All []string `json:"all"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.All, nil
}

// readMihomoControllerSecret returns the `secret:` of mihomo.yaml ("" when
// unset, in which case the controller accepts unauthenticated local calls).
func readMihomoControllerSecret() string {
	path, err := mihomoYAMLPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "secret:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "secret:")), "\"'")
		}
	}
	return ""
}
