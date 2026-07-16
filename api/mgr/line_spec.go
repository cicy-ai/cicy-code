// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// The Line Spec — the unit of build / run / sell on the platform.
//
// A production line is a DECLARATIVE FILE, not a script. That distinction is
// the whole platform thesis:
//
//   - It is the API. Third parties author a line.yaml; they never link against
//     our internals, so the engine can change under them.
//   - It is the unit of commerce. An id + version + price + take-rate is
//     something you can list, install, meter and settle. A pipeline.js is not.
//   - It is the unit of TRUST. The gates — above all the human approval gate —
//     are declared in the file, so "what will this thing do to my accounts?" is
//     answered by reading the spec instead of auditing the code. A declared gate
//     is enforced by the ENGINE; it is not left to an agent's good behaviour.
//   - It is the unit of measurement. Declared metrics let one factory board read
//     every line the same way, no matter who wrote it.
//
// Deliberately NOT in the spec: unit cost. Cost is READ from the gateway's
// per-request accounting (cost_credit), never declared and never estimated —
// a hand-written "est_unit_cost" is exactly the kind of plausible number that
// turns out to be 2× wrong.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LineSpec is one production line.
type LineSpec struct {
	ID      string `yaml:"id" json:"id"`
	Version string `yaml:"version" json:"version"`
	Name    string `yaml:"name,omitempty" json:"name,omitempty"`
	Summary string `yaml:"summary,omitempty" json:"summary,omitempty"`

	Inputs   LineInputs    `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Conveyor LineConveyor  `yaml:"conveyor,omitempty" json:"conveyor,omitempty"`
	Stations []LineStation `yaml:"stations" json:"stations"`

	// dir is where the spec was loaded from; every relative path in the spec
	// (role files, canon) resolves against it. Not part of the file.
	dir string `yaml:"-" json:"-"`
}

type LineInputs struct {
	// Canon is a shared raw-material file every station may read (brand canon,
	// house style, API contract — whatever the line is about).
	Canon string `yaml:"canon,omitempty" json:"canon,omitempty"`
}

type LineConveyor struct {
	// Backend is the WIP store. "cicy-todo" makes the run visible in the UI;
	// "" keeps the run in memory (useful in CI).
	Backend string `yaml:"backend,omitempty" json:"backend,omitempty"`
}

// LineStation is one transformation: in → out, with an optional gate.
type LineStation struct {
	ID   string `yaml:"id" json:"id"`
	Role string `yaml:"role,omitempty" json:"role,omitempty"`

	// RoleFile is the station's prompt — an AGENTS.md-shaped file. It becomes
	// the ephemeral station agent's role context, which is exactly how
	// resolveLiteConfig already loads an agent's character. Defaults to
	// stations/<id>.md when present.
	RoleFile string `yaml:"role_file,omitempty" json:"role_file,omitempty"`

	// Actor "human" marks an APPROVAL GATE: the engine stops the run dead and
	// will not proceed without an explicit, recorded approval. This is the one
	// station kind that runs no model at all.
	Actor string `yaml:"actor,omitempty" json:"actor,omitempty"`

	In  string `yaml:"in,omitempty" json:"in,omitempty"`
	Out string `yaml:"out,omitempty" json:"out,omitempty"`

	// OutFields are the keys the station's JSON output MUST carry. Missing keys
	// fail the station the same way a rule gate does — a station that silently
	// returns the wrong shape is how a pipeline rots.
	OutFields []string `yaml:"out_fields,omitempty" json:"out_fields,omitempty"`

	Gate   *LineGate `yaml:"gate,omitempty" json:"gate,omitempty"`
	Skills []string  `yaml:"skills,omitempty" json:"skills,omitempty"`

	Model string `yaml:"model,omitempty" json:"model,omitempty"`
}

// LineGate is a quality or approval gate.
type LineGate struct {
	// Type: "rule" (the station's own verdict decides) or "human".
	Type string `yaml:"type" json:"type"`

	// Field is the boolean/verdict key in the station output that the rule reads
	// (e.g. "result" holding pass|rework).
	Field string `yaml:"field,omitempty" json:"field,omitempty"`
	Pass  string `yaml:"pass,omitempty" json:"pass,omitempty"`

	// OnFail sends the WIP back to an earlier station: "rework -> draft".
	OnFail     string `yaml:"on_fail,omitempty" json:"on_fail,omitempty"`
	MaxRework  int    `yaml:"max_rework,omitempty" json:"max_rework,omitempty"`
	RequiredFor string `yaml:"required_for,omitempty" json:"required_for,omitempty"`
}

var (
	lineIDRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	lineVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	lineOnFailRe  = regexp.MustCompile(`^\s*rework\s*->\s*([a-z0-9][a-z0-9-]*)\s*$`)
)

// LoadLineSpec reads and validates a line.yaml.
func LoadLineSpec(path string) (*LineSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read line spec: %w", err)
	}
	var spec LineSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("parse line spec: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	spec.dir = filepath.Dir(abs)
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

// Dir is where the spec lives; relative paths resolve against it.
func (s *LineSpec) Dir() string { return s.dir }

// Resolve turns a spec-relative path into an absolute one, refusing anything
// that escapes the line package — a line is a distributable artifact and must
// not be able to read the host's filesystem by writing `../../../.ssh/id_rsa`
// as its canon.
func (s *LineSpec) Resolve(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the line package", rel)
	}
	full := filepath.Join(s.dir, filepath.FromSlash(rel))
	absRoot, err := filepath.Abs(s.dir)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if absFull != absRoot && !strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the line package", rel)
	}
	return absFull, nil
}

// Station returns the station with the given id.
func (s *LineSpec) Station(id string) (*LineStation, int, bool) {
	for i := range s.Stations {
		if s.Stations[i].ID == id {
			return &s.Stations[i], i, true
		}
	}
	return nil, -1, false
}

// HumanGates lists the stations that require a human approval.
func (s *LineSpec) HumanGates() []string {
	var out []string
	for _, st := range s.Stations {
		if st.IsHumanGate() {
			out = append(out, st.ID)
		}
	}
	return out
}

// IsHumanGate reports whether this station is an approval gate.
func (st *LineStation) IsHumanGate() bool {
	if strings.EqualFold(strings.TrimSpace(st.Actor), "human") {
		return true
	}
	return st.Gate != nil && strings.EqualFold(strings.TrimSpace(st.Gate.Type), "human")
}

// ReworkTarget returns the station id the gate loops back to, and whether it does.
func (g *LineGate) ReworkTarget() (string, bool) {
	if g == nil {
		return "", false
	}
	m := lineOnFailRe.FindStringSubmatch(g.OnFail)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Validate rejects a spec that could not run correctly, LOUDLY. A spec is a
// contract a buyer relies on; a silently-degraded one is worse than a rejected
// one.
func (s *LineSpec) Validate() error {
	var errs []string
	add := func(f string, a ...interface{}) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if !lineIDRe.MatchString(s.ID) {
		add("id %q must be lowercase alphanumeric/dash (it is the marketplace key)", s.ID)
	}
	if !lineVersionRe.MatchString(s.Version) {
		add("version %q must be semver x.y.z (buyers pin versions)", s.Version)
	}
	if len(s.Stations) == 0 {
		add("a line needs at least one station")
	}

	seen := map[string]bool{}
	for i, st := range s.Stations {
		where := fmt.Sprintf("stations[%d]", i)
		if st.ID != "" {
			where = fmt.Sprintf("station %q", st.ID)
		}
		if !lineIDRe.MatchString(st.ID) {
			add("%s: id must be lowercase alphanumeric/dash", where)
			continue
		}
		if seen[st.ID] {
			add("%s: duplicate station id", where)
		}
		seen[st.ID] = true

		if st.IsHumanGate() {
			// An approval gate runs no model — a role/out on it is a sign the
			// author thinks it does something, which it must not.
			if st.RoleFile != "" || len(st.OutFields) > 0 {
				add("%s: a human gate runs no model; drop role_file/out_fields", where)
			}
			continue
		}

		if st.RoleFile != "" {
			if _, err := s.Resolve(st.RoleFile); err != nil {
				add("%s: role_file: %v", where, err)
			}
		}
		if st.Gate != nil {
			g := st.Gate
			switch strings.ToLower(strings.TrimSpace(g.Type)) {
			case "rule":
				if strings.TrimSpace(g.Field) == "" {
					add("%s: gate.field is required for a rule gate (which key holds the verdict?)", where)
				}
				if strings.TrimSpace(g.Pass) == "" {
					add("%s: gate.pass is required for a rule gate (which value means pass?)", where)
				}
				if target, ok := g.ReworkTarget(); ok {
					if _, _, found := s.Station(target); !found {
						add("%s: gate.on_fail loops back to unknown station %q", where, target)
					}
					if g.MaxRework <= 0 {
						// An unbounded rework loop is an unbounded bill.
						add("%s: gate.max_rework must be > 0 when on_fail loops back", where)
					}
				} else if strings.TrimSpace(g.OnFail) != "" {
					add("%s: gate.on_fail must read `rework -> <station-id>`", where)
				}
			case "human":
				// handled above
			case "":
				add("%s: gate.type is required", where)
			default:
				add("%s: unknown gate.type %q (want rule|human)", where, g.Type)
			}
		}
	}

	if s.Inputs.Canon != "" {
		if _, err := s.Resolve(s.Inputs.Canon); err != nil {
			add("inputs.canon: %v", err)
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("invalid line spec:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
