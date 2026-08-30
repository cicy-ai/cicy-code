package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Dedicated exit-IP probe path.
//
// A group's selected node is the user's decision: nothing in cicy-code may
// change it on its own. So the exit-IP probe no longer repoints
// default_proxy_group at the node under test. Instead mihomo gets a private
// listener + select group that only the probe uses:
//
//	listeners:     - name: cicy-probe, type: mixed, port: <probePort>, listen: 127.0.0.1
//	proxy-groups:  - name: cicy-probe-group, type: select, proxies: [<every node>]
//	rules:         - IN-NAME,cicy-probe,cicy-probe-group   (first rule)
//
// The probe switches cicy-probe-group (nobody else routes through it) and
// sends the probe request through the cicy-probe listener. Both are hidden
// from the UI. Provisioning is additive, validated with `mihomo -t`, and
// happens on first use; if it cannot be provisioned the probe reports an
// error rather than touching any user-visible group.

const (
	probeListenerName = "cicy-probe"
	probeGroupName    = "cicy-probe-group"
	probePortDefault  = 19002
)

var probeSetupMu sync.Mutex

// isMihomoProbeName reports whether name belongs to the private probe path.
func isMihomoProbeName(name string) bool {
	return name == probeListenerName || name == probeGroupName
}

// mihomoProbeState reads the probe listener port and whether the group exists.
func mihomoProbeState(d *mihomoDoc) (port int, hasListener, hasGroup, hasRule bool) {
	if s := d.seq("listeners", false); s != nil {
		for _, it := range s.Content {
			if mappingGet(it, "name") == probeListenerName {
				hasListener = true
				port, _ = strconv.Atoi(mappingGet(it, "port"))
			}
		}
	}
	hasGroup = d.findGroup(probeGroupName) != nil
	if s := d.seq("rules", false); s != nil {
		for _, it := range s.Content {
			if it.Kind == yaml.ScalarNode && strings.HasPrefix(it.Value, "IN-NAME,"+probeListenerName+",") {
				hasRule = true
			}
		}
	}
	return
}

func freeLoopbackPort(preferred int) int {
	for p := preferred; p < preferred+50; p++ {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			_ = l.Close()
			return p
		}
	}
	return 0
}

// ensureMihomoProbePath provisions the listener/group/rule when missing and
// returns the listener port. It never edits anything else in the file.
func ensureMihomoProbePath() (int, error) {
	probeSetupMu.Lock()
	defer probeSetupMu.Unlock()
	d, err := loadMihomoDoc()
	if err != nil {
		return 0, err
	}
	port, hasListener, hasGroup, hasRule := mihomoProbeState(d)
	// Keep the probe group's member list in step with proxies: (node CRUD
	// already appends new nodes to every group; this covers hand edits).
	changed := false
	if hasListener && hasGroup && hasRule && port > 0 {
		g := d.findGroup(probeGroupName)
		members := mappingSeq(g, "proxies", true)
		if s := d.seq("proxies", false); s != nil {
			for _, it := range s.Content {
				if n := mappingGet(it, "name"); n != "" && !seqHas(members, n) {
					members.Content = append(members.Content, scalar(n))
					changed = true
				}
			}
		}
		for _, c := range d.chains() {
			if n, _ := c["name"].(string); n != "" && !seqHas(members, n) {
				members.Content = append(members.Content, scalar(n))
				changed = true
			}
		}
		if !changed {
			return port, nil
		}
	} else {
		if !hasListener {
			port = freeLoopbackPort(probePortDefault)
			if port == 0 {
				return 0, fmt.Errorf("no free loopback port for the probe listener")
			}
			ls := d.seq("listeners", true)
			node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				scalar("name"), scalar(probeListenerName),
				scalar("type"), scalar("mixed"),
				scalar("port"), {Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(port)},
				scalar("listen"), scalar("127.0.0.1"),
			}}
			node.HeadComment = "Private listener for cicy-code exit-IP probes — routed to " + probeGroupName + " by the IN-NAME rule; never used by agents or browsers."
			ls.Content = append(ls.Content, node)
			changed = true
		}
		if !hasGroup {
			names := []*yaml.Node{}
			if s := d.seq("proxies", false); s != nil {
				for _, it := range s.Content {
					if n := mappingGet(it, "name"); n != "" {
						names = append(names, scalar(n))
					}
				}
			}
			if len(names) == 0 {
				names = append(names, scalar("DIRECT"))
			}
			gs := d.seq("proxy-groups", true)
			g := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				scalar("name"), scalar(probeGroupName),
				scalar("type"), scalar("select"),
				scalar("proxies"), {Kind: yaml.SequenceNode, Tag: "!!seq", Content: names},
			}}
			g.HeadComment = "Probe-only group (hidden in the UI): cicy-code switches this one to test a node's exit IP, so user-facing groups are never touched."
			gs.Content = append(gs.Content, g)
			changed = true
		}
		if !hasRule {
			rs := d.seq("rules", true)
			rule := scalar("IN-NAME," + probeListenerName + "," + probeGroupName)
			rs.Content = append([]*yaml.Node{rule}, rs.Content...)
			changed = true
		}
	}
	if changed {
		if err := d.save(); err != nil {
			return 0, err
		}
		if r := reloadMihomoAfterEdit(); r != "reloaded" && r != "not running" {
			return 0, fmt.Errorf("mihomo %s", r)
		}
	}
	return port, nil
}
