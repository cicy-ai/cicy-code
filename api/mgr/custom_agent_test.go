package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A custom agent authored under ~/cicy-ai/agents/<slug>/AGENT.md must round-trip
// (write → scan → parse) and feed the SAME role-lookup chain the built-in roles
// use: employeeTemplateTools/Prompt resolve from it, and composeAgentMemory
// seeds its persona body into the new agent's guidance file.
func TestCustomAgentRoundTripAndLookup(t *testing.T) {
	prev := cicyRootDir
	cicyRootDir = t.TempDir()
	resetEmployeeTemplatesCache()
	defer func() { cicyRootDir = prev; resetEmployeeTemplatesCache() }()

	slug, err := writeCustomAgent(customAgent{
		Slug:  "销售助手",
		Name:  "销售助手",
		Tools: []string{"coordinate", "shell"},
		Model: "claude-opus-4-8",
		Body:  "你是一个销售助手,负责回答客户咨询。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if slug != "销售助手" {
		t.Fatalf("slug = %q", slug)
	}

	// scan
	all := scanCustomAgents()
	if len(all) != 1 || all[0].Name != "销售助手" || all[0].Model != "claude-opus-4-8" {
		t.Fatalf("scanCustomAgents = %+v", all)
	}

	// direct lookup
	ca, ok := customAgentFor("销售助手")
	if !ok || len(ca.Tools) != 2 || ca.Tools[1] != "shell" || !strings.Contains(ca.Body, "销售助手") {
		t.Fatalf("customAgentFor = %+v ok=%v", ca, ok)
	}

	// feeds the employees lookup chain (tools + persona prompt)
	if tools := employeeTemplateTools("销售助手"); len(tools) != 2 || tools[0] != "coordinate" {
		t.Errorf("employeeTemplateTools = %v", tools)
	}
	if p := employeeTemplatePrompt("销售助手"); !strings.Contains(p, "回答客户咨询") {
		t.Errorf("employeeTemplatePrompt = %q", p)
	}

	// seeded into a new agent's memory (no memory/agents/*.md file exists for it)
	mem := composeAgentMemory("w-9999", "/tmp/ws", "cicy", "", "销售助手")
	if !strings.Contains(mem, "回答客户咨询") {
		t.Errorf("composeAgentMemory missing persona body:\n%s", mem)
	}

	// delete
	if err := deleteCustomAgent("销售助手"); err != nil {
		t.Fatal(err)
	}
	if _, ok := customAgentFor("销售助手"); ok {
		t.Errorf("custom agent still present after delete")
	}
	if _, err := os.Stat(filepath.Join(customAgentsDir(), "销售助手")); !os.IsNotExist(err) {
		t.Errorf("dir not removed: %v", err)
	}
}
