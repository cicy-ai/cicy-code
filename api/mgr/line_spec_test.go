// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLine lays out a line package on disk and returns the path to its line.yaml.
func writeLine(t *testing.T, yaml string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	specPath := filepath.Join(dir, "line.yaml")
	if err := os.WriteFile(specPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return specPath
}

const goodLine = `
id: content-factory
version: 1.0.0
stations:
  - id: draft
    in: seed
    out: draft
    out_fields: [posts]
  - id: qc
    in: draft
    out: verdict
    out_fields: [result]
    gate:
      type: rule
      field: result
      pass: pass
      on_fail: rework -> draft
      max_rework: 2
  - id: approve
    actor: human
`

func TestLoadLineSpec(t *testing.T) {
	p := writeLine(t, goodLine, map[string]string{
		"stations/draft.md": "You draft.",
		"stations/qc.md":    "You review.",
	})
	spec, err := LoadLineSpec(p)
	if err != nil {
		t.Fatalf("LoadLineSpec: %v", err)
	}
	if spec.ID != "content-factory" || spec.Version != "1.0.0" {
		t.Fatalf("id/version = %s@%s", spec.ID, spec.Version)
	}
	if got := spec.HumanGates(); len(got) != 1 || got[0] != "approve" {
		t.Fatalf("HumanGates() = %v, want [approve]", got)
	}
	st, idx, ok := spec.Station("qc")
	if !ok || idx != 1 {
		t.Fatalf("Station(qc) = idx %d, ok %v", idx, ok)
	}
	if target, ok := st.Gate.ReworkTarget(); !ok || target != "draft" {
		t.Fatalf("ReworkTarget() = %q, %v; want draft, true", target, ok)
	}
}

// The spec is a contract a buyer relies on. A bad one must be REJECTED, loudly —
// a silently-degraded line is worse than one that refuses to load.
func TestLineSpecValidationRejects(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			"unbounded rework loop is an unbounded bill",
			"id: x\nversion: 1.0.0\nstations:\n  - id: a\n    out_fields: [r]\n  - id: b\n    gate: {type: rule, field: r, pass: ok, on_fail: 'rework -> a'}\n",
			"max_rework",
		},
		{
			"rework loops back to a station that does not exist",
			"id: x\nversion: 1.0.0\nstations:\n  - id: b\n    gate: {type: rule, field: r, pass: ok, on_fail: 'rework -> ghost', max_rework: 1}\n",
			"unknown station",
		},
		{
			"rule gate with no field to read",
			"id: x\nversion: 1.0.0\nstations:\n  - id: b\n    gate: {type: rule, pass: ok}\n",
			"gate.field is required",
		},
		{
			"version must be semver — buyers pin versions",
			"id: x\nversion: v1\nstations:\n  - id: a\n",
			"semver",
		},
		{
			"duplicate station ids",
			"id: x\nversion: 1.0.0\nstations:\n  - id: a\n  - id: a\n",
			"duplicate",
		},
		{
			"a human gate must not pretend to do work",
			"id: x\nversion: 1.0.0\nstations:\n  - id: a\n    actor: human\n    out_fields: [oops]\n",
			"runs no model",
		},
		{
			"unknown gate type",
			"id: x\nversion: 1.0.0\nstations:\n  - id: a\n    gate: {type: vibes}\n",
			"unknown gate.type",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadLineSpec(writeLine(t, tc.yaml, nil))
			if err == nil {
				t.Fatal("spec loaded, but it should have been rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v\nwant it to mention %q", err, tc.want)
			}
		})
	}
}

// A line is a DISTRIBUTABLE artifact — someone else's yaml running on your box.
// It must not be able to read the host filesystem by pointing its canon or a
// station role at ../../../.ssh/id_rsa.
func TestLineSpecRefusesToEscapeItsPackage(t *testing.T) {
	p := writeLine(t, "id: x\nversion: 1.0.0\nstations:\n  - id: a\n", nil)
	spec, err := LoadLineSpec(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"../outside.md",
		"stations/../../outside.md",
		"/etc/passwd",
		"../../../../../../etc/passwd",
	} {
		if got, err := spec.Resolve(bad); err == nil {
			t.Errorf("Resolve(%q) = %q with no error — it escaped the line package", bad, got)
		}
	}
	// …while a legitimate in-package path still resolves.
	if _, err := spec.Resolve("stations/a.md"); err != nil {
		t.Errorf("Resolve(stations/a.md) failed: %v", err)
	}
}

func TestLineSpecValidatesRoleAndCanonPaths(t *testing.T) {
	yaml := "id: x\nversion: 1.0.0\ninputs:\n  canon: ../../../etc/passwd\nstations:\n  - id: a\n"
	_, err := LoadLineSpec(writeLine(t, yaml, nil))
	if err == nil || !strings.Contains(err.Error(), "escapes the line package") {
		t.Fatalf("err = %v; a canon pointing outside the package must be rejected", err)
	}
}
