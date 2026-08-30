package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Node / group CRUD over ~/cicy-ai/db/mihomo.yaml.
//
// The yaml is edited as a yaml.Node tree so comments, ordering and quoting of
// everything we do not touch survive. Every write goes through
// writeMihomoYAMLValidated (mihomo -t) and is followed by `cicy-mihomo
// reload`, so a bad node never lands in the live config.
//
//	GET    /api/proxy/nodes                → {nodes:[{name,type,server,port,yaml}], groups:[{name,type,proxies,use}]}
//	POST   /api/proxy/nodes   {yaml, groups?}  → add one node (a single yaml mapping); joins every group unless `groups` lists some
//	PUT    /api/proxy/nodes   {name, yaml}     → replace the node; a renamed node is renamed inside every group too
//	DELETE /api/proxy/nodes?name=…             → remove the node from proxies and from every group
//	PUT    /api/proxy/groups/members {group, proxies:[…]} → set a group's member list (order kept)

// mihomoDoc is the parsed yaml document plus the path it came from.
type mihomoDoc struct {
	path string
	root yaml.Node
}

func loadMihomoDoc() (*mihomoDoc, error) {
	path, err := mihomoYAMLPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := &mihomoDoc{path: path}
	if err := yaml.Unmarshal(data, &d.root); err != nil {
		return nil, fmt.Errorf("parse mihomo.yaml: %w", err)
	}
	if d.root.Kind != yaml.DocumentNode || len(d.root.Content) == 0 || d.root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("mihomo.yaml is not a mapping document")
	}
	return d, nil
}

// top returns the document's top-level mapping.
func (d *mihomoDoc) top() *yaml.Node { return d.root.Content[0] }

// seq returns the top-level sequence under key, creating an empty one when
// create is set. Returns nil when absent and create is false.
func (d *mihomoDoc) seq(key string, create bool) *yaml.Node {
	top := d.top()
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == key {
			v := top.Content[i+1]
			if v.Kind == yaml.SequenceNode {
				return v
			}
			if v.Tag == "!!null" && create {
				v.Kind, v.Tag, v.Value, v.Content = yaml.SequenceNode, "!!seq", "", nil
				return v
			}
			return nil
		}
	}
	if !create {
		return nil
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	top.Content = append(top.Content, k, v)
	return v
}

// marshalYAML2 encodes with a 2-space indent — yaml.v3 defaults to 4, which
// would re-indent the whole file on every edit.
func marshalYAML2(n *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (d *mihomoDoc) save() error {
	out, err := marshalYAML2(&d.root)
	if err != nil {
		return err
	}
	return writeMihomoYAMLValidated(d.path, out)
}

// mappingGet returns the scalar value for key inside a mapping node.
func mappingGet(m *yaml.Node, key string) string {
	if m == nil || m.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1].Value
		}
	}
	return ""
}

// mappingSeq returns the sequence value for key inside a mapping node.
func mappingSeq(m *yaml.Node, key string, create bool) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind == yaml.SequenceNode {
				return v
			}
			if v.Tag == "!!null" && create {
				v.Kind, v.Tag, v.Value, v.Content = yaml.SequenceNode, "!!seq", "", nil
				return v
			}
			return nil
		}
	}
	if !create {
		return nil
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	m.Content = append(m.Content, k, v)
	return v
}

func seqStrings(s *yaml.Node) []string {
	if s == nil {
		return []string{} // never nil: the JSON consumer indexes .length
	}
	out := make([]string, 0, len(s.Content))
	for _, it := range s.Content {
		if it.Kind == yaml.ScalarNode {
			out = append(out, it.Value)
		}
	}
	return out
}

func seqHas(s *yaml.Node, v string) bool {
	for _, it := range s.Content {
		if it.Kind == yaml.ScalarNode && it.Value == v {
			return true
		}
	}
	return false
}

func seqRemove(s *yaml.Node, v string) bool {
	removed := false
	kept := s.Content[:0]
	for _, it := range s.Content {
		if it.Kind == yaml.ScalarNode && it.Value == v {
			removed = true
			continue
		}
		kept = append(kept, it)
	}
	s.Content = kept
	return removed
}

func seqRename(s *yaml.Node, from, to string) {
	for _, it := range s.Content {
		if it.Kind == yaml.ScalarNode && it.Value == from {
			it.Value = to
		}
	}
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// parseProxyNodeYAML accepts one node, either as a bare mapping
// (`name: x\ntype: ss\n…`) or as a one-item list (`- name: x …`), and returns
// the mapping node in block style.
func parseProxyNodeYAML(text string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("invalid yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("empty node")
	}
	m := doc.Content[0]
	if m.Kind == yaml.SequenceNode {
		if len(m.Content) != 1 {
			return nil, fmt.Errorf("paste exactly one node")
		}
		m = m.Content[0]
	}
	if m.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("node must be a yaml mapping (name/type/server/port…)")
	}
	name := strings.TrimSpace(mappingGet(m, "name"))
	if name == "" {
		return nil, fmt.Errorf("node needs a name")
	}
	if strings.TrimSpace(mappingGet(m, "type")) == "" {
		return nil, fmt.Errorf("node needs a type")
	}
	if isMihomoBuiltinName(name) {
		return nil, fmt.Errorf("%q is a reserved name", name)
	}
	m.Style = 0 // block style: keeps the file readable regardless of how it was pasted
	return m, nil
}

func (d *mihomoDoc) findProxy(name string) (int, *yaml.Node) {
	s := d.seq("proxies", false)
	if s == nil {
		return -1, nil
	}
	for i, it := range s.Content {
		if it.Kind == yaml.MappingNode && mappingGet(it, "name") == name {
			return i, it
		}
	}
	return -1, nil
}

func (d *mihomoDoc) findGroup(name string) *yaml.Node {
	s := d.seq("proxy-groups", false)
	if s == nil {
		return nil
	}
	for _, it := range s.Content {
		if it.Kind == yaml.MappingNode && mappingGet(it, "name") == name {
			return it
		}
	}
	return nil
}

func (d *mihomoDoc) groupNames() []string {
	var out []string
	if s := d.seq("proxy-groups", false); s != nil {
		for _, it := range s.Content {
			if n := mappingGet(it, "name"); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

func proxyNodeSummary(m *yaml.Node) M {
	out, _ := marshalYAML2(m)
	return M{
		"name":   mappingGet(m, "name"),
		"type":   mappingGet(m, "type"),
		"server": mappingGet(m, "server"),
		"port":   mappingGet(m, "port"),
		"yaml":   string(out),
	}
}

func reloadMihomoAfterEdit() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if !mihomoControllerAlive(2 * time.Second) {
		return "not running"
	}
	if _, err := runCicyMihomo(ctx, "reload"); err != nil {
		return "reload failed: " + err.Error()
	}
	return "reloaded"
}

func handleProxyNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleProxyNodesList(w)
	case http.MethodPost:
		handleProxyNodeCreate(w, r)
	case http.MethodPut:
		handleProxyNodeUpdate(w, r)
	case http.MethodDelete:
		handleProxyNodeDelete(w, r)
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleProxyNodesList(w http.ResponseWriter) {
	d, err := loadMihomoDoc()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	nodes := []M{}
	if s := d.seq("proxies", false); s != nil {
		for _, it := range s.Content {
			if it.Kind == yaml.MappingNode {
				nodes = append(nodes, proxyNodeSummary(it))
			}
		}
	}
	groups := []M{}
	if s := d.seq("proxy-groups", false); s != nil {
		for _, it := range s.Content {
			if it.Kind != yaml.MappingNode {
				continue
			}
			groups = append(groups, M{
				"name":    mappingGet(it, "name"),
				"type":    mappingGet(it, "type"),
				"proxies": seqStrings(mappingSeq(it, "proxies", false)),
				"use":     seqStrings(mappingSeq(it, "use", false)),
			})
		}
	}
	J(w, M{"success": true, "path": d.path, "nodes": nodes, "groups": groups})
}

func handleProxyNodeCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAML   string   `json:"yaml"`
		Groups []string `json:"groups"` // empty → every group
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	node, err := parseProxyNodeYAML(req.YAML)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	name := mappingGet(node, "name")
	d, err := loadMihomoDoc()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if _, existing := d.findProxy(name); existing != nil {
		httpErr(w, 409, fmt.Sprintf("node %q already exists", name))
		return
	}
	if d.findGroup(name) != nil {
		httpErr(w, 409, fmt.Sprintf("%q is a proxy group", name))
		return
	}
	d.seq("proxies", true).Content = append(d.seq("proxies", true).Content, node)
	joined := []string{}
	want := map[string]bool{}
	for _, g := range req.Groups {
		want[strings.TrimSpace(g)] = true
	}
	if s := d.seq("proxy-groups", false); s != nil {
		for _, g := range s.Content {
			gname := mappingGet(g, "name")
			if gname == "" || (len(want) > 0 && !want[gname]) {
				continue
			}
			members := mappingSeq(g, "proxies", true)
			if !seqHas(members, name) {
				members.Content = append(members.Content, scalar(name))
				joined = append(joined, gname)
			}
		}
	}
	if err := d.save(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	J(w, M{"success": true, "node": proxyNodeSummary(node), "groups": joined, "mihomo": reloadMihomoAfterEdit()})
}

func handleProxyNodeUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		YAML string `json:"yaml"`
	}
	if err := readBody(r, &req); err != nil {
		httpErr(w, 400, "bad body: "+err.Error())
		return
	}
	old := strings.TrimSpace(req.Name)
	node, err := parseProxyNodeYAML(req.YAML)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	newName := mappingGet(node, "name")
	d, err := loadMihomoDoc()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	idx, existing := d.findProxy(old)
	if existing == nil {
		httpErr(w, 404, fmt.Sprintf("node %q not found", old))
		return
	}
	if newName != old {
		if _, dup := d.findProxy(newName); dup != nil || d.findGroup(newName) != nil {
			httpErr(w, 409, fmt.Sprintf("%q already exists", newName))
			return
		}
	}
	// Keep the comment attached above the old entry, if any.
	node.HeadComment = existing.HeadComment
	d.seq("proxies", false).Content[idx] = node
	if newName != old {
		if s := d.seq("proxy-groups", false); s != nil {
			for _, g := range s.Content {
				if members := mappingSeq(g, "proxies", false); members != nil {
					seqRename(members, old, newName)
				}
			}
		}
	}
	if err := d.save(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	J(w, M{"success": true, "node": proxyNodeSummary(node), "renamed": newName != old, "mihomo": reloadMihomoAfterEdit()})
}

func handleProxyNodeDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		httpErr(w, 400, "name required")
		return
	}
	d, err := loadMihomoDoc()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	idx, existing := d.findProxy(name)
	if existing == nil {
		httpErr(w, 404, fmt.Sprintf("node %q not found", name))
		return
	}
	// A group must keep at least one member or mihomo refuses the config.
	if s := d.seq("proxy-groups", false); s != nil {
		for _, g := range s.Content {
			members := mappingSeq(g, "proxies", false)
			if members == nil || !seqHas(members, name) {
				continue
			}
			if len(members.Content) <= 1 && len(seqStrings(mappingSeq(g, "use", false))) == 0 {
				httpErr(w, 409, fmt.Sprintf("%q is the only member of group %q — add another member first", name, mappingGet(g, "name")))
				return
			}
		}
	}
	proxies := d.seq("proxies", false)
	proxies.Content = append(proxies.Content[:idx], proxies.Content[idx+1:]...)
	left := []string{}
	if s := d.seq("proxy-groups", false); s != nil {
		for _, g := range s.Content {
			if members := mappingSeq(g, "proxies", false); members != nil && seqRemove(members, name) {
				left = append(left, mappingGet(g, "name"))
			}
		}
	}
	if err := d.save(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	J(w, M{"success": true, "name": name, "left_groups": left, "mihomo": reloadMihomoAfterEdit()})
}

// handleProxyGroupMembers sets a group's `proxies:` list (PUT) — order is
// kept as given. Members must be existing nodes, other groups, or the
// built-ins DIRECT / REJECT.
func handleProxyGroupMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		Group   string   `json:"group"`
		Proxies []string `json:"proxies"`
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
	d, err := loadMihomoDoc()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	g := d.findGroup(group)
	if g == nil {
		httpErr(w, 404, fmt.Sprintf("group %q not found", group))
		return
	}
	known := map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true}
	if s := d.seq("proxies", false); s != nil {
		for _, it := range s.Content {
			known[mappingGet(it, "name")] = true
		}
	}
	for _, n := range d.groupNames() {
		known[n] = true
	}
	clean := make([]string, 0, len(req.Proxies))
	seen := map[string]bool{}
	for _, p := range req.Proxies {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		if p == group {
			httpErr(w, 400, "a group cannot contain itself")
			return
		}
		if !known[p] {
			httpErr(w, 400, fmt.Sprintf("unknown member %q", p))
			return
		}
		seen[p] = true
		clean = append(clean, p)
	}
	if len(clean) == 0 && len(seqStrings(mappingSeq(g, "use", false))) == 0 {
		httpErr(w, 400, "a group needs at least one member")
		return
	}
	members := mappingSeq(g, "proxies", true)
	members.Content = members.Content[:0]
	for _, p := range clean {
		members.Content = append(members.Content, scalar(p))
	}
	if err := d.save(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	J(w, M{"success": true, "group": group, "proxies": seqStrings(members), "mihomo": reloadMihomoAfterEdit()})
}
