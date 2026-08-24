package main

import (
	"reflect"
	"testing"
)

func TestListStartupAgentConfigsIncludesAllActiveLocalNonCicyAgents(t *testing.T) {
	withTempCicyRoot(t)
	withTestStore(t)

	fixtures := []struct {
		paneID    string
		agentType string
		active    bool
		machineID int
	}{
		{"w-10021:main.0", "codex", true, 0},
		{"w-10022:main.0", "claude", true, 0},
		{"w-10023:main.0", "gemini", false, 0},
		{"w-10024:main.0", "opencode", true, 9},
		{"w-10025:main.0", "cicy", true, 0},
		{"w-10026:main.0", "dispatcher", true, 0},
		{"w-10027:main.0", "secretary", true, 0},
	}
	for _, fixture := range fixtures {
		if _, err := store.Exec(`
			INSERT INTO agent_config (pane_id, title, workspace, init_script, config,
				role, default_model, agent_type, active, machine_id)
			VALUES (?, ?, ?, '', '{}', 'worker', '', ?, ?, ?)
		`, fixture.paneID, fixture.paneID, "/tmp/"+fixture.paneID, fixture.agentType, fixture.active, fixture.machineID); err != nil {
			t.Fatalf("insert %s: %v", fixture.paneID, err)
		}
	}

	configs, err := listStartupAgentConfigs()
	if err != nil {
		t.Fatalf("list startup agent configs: %v", err)
	}
	got := make([]string, 0, len(configs))
	for _, config := range configs {
		got = append(got, config.paneID)
	}
	want := []string{"w-10021:main.0", "w-10022:main.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("startup pane IDs = %v, want %v", got, want)
	}
}
