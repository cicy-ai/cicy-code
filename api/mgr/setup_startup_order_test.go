package main

import (
	"reflect"
	"testing"
)

func TestRunAgentStartupPreparesSkillsBeforeLaunchingAgents(t *testing.T) {
	var stages []string
	selected := []string{"codex"}

	runAgentStartup(selected, func() {
		stages = append(stages, "install", "repair-bin", "surface")
	}, func(got []string) {
		if !reflect.DeepEqual(got, selected) {
			t.Fatalf("launch selected agents = %v, want %v", got, selected)
		}
		stages = append(stages, "launch")
	})

	want := []string{"install", "repair-bin", "surface", "launch"}
	if !reflect.DeepEqual(stages, want) {
		t.Fatalf("startup stages = %v, want %v", stages, want)
	}
}
