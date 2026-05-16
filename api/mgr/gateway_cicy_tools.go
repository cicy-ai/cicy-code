package main

import (
	"fmt"
	"strings"
)

// cicyGatewayTool is a tool the gateway injects into every request.
type cicyGatewayTool struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
}

var cicyGatewayTools = []cicyGatewayTool{
	{
		Name:        "cicy_agent_tmux_ls",
		Description: "List all tmux panes currently running in this cicy environment. Returns pane IDs, window names, and running commands. Use this to discover which agent panes are active.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "cicy_agent_list",
		Description: "List all configured agents with their short pane ID, agent_type (claude / codex / opencode / …), and title. Use this to know which agents exist and what type they are.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
}

// injectCicyToolDefs appends cicy tool definitions to payload["tools"].
// provider "anthropic" → {name, description, input_schema}
// anything else (openai)  → {type:"function", function:{name, description, parameters}}
func injectCicyToolDefs(payload map[string]interface{}, provider string) map[string]interface{} {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	existing, _ := payload["tools"].([]interface{})
	prepend := make([]interface{}, 0, len(cicyGatewayTools))
	for _, t := range cicyGatewayTools {
		if provider == "anthropic" {
			prepend = append(prepend, map[string]interface{}{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Parameters,
			})
		} else {
			prepend = append(prepend, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			})
		}
	}
	payload["tools"] = append(prepend, existing...)
	return payload
}

func cicyToolTmuxLs() string {
	rows, err := store.Query(
		`SELECT pane_id, title, agent_type FROM agent_config ORDER BY pane_id`)
	if err != nil {
		return fmt.Sprintf("error querying panes: %v", err)
	}
	defer rows.Close()
	var lines []string
	lines = append(lines, "pane_id\tagent_type\ttitle")
	lines = append(lines, "-------\t----------\t-----")
	for rows.Next() {
		var paneID, title, agentType string
		if err := rows.Scan(&paneID, &title, &agentType); err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", shortPaneID(paneID), agentType, title))
	}
	return strings.Join(lines, "\n")
}

func cicyToolAgentList() string {
	rows, err := store.Query(
		`SELECT pane_id, agent_type, title, workspace FROM agent_config ORDER BY pane_id`)
	if err != nil {
		return fmt.Sprintf("error querying agents: %v", err)
	}
	defer rows.Close()
	var lines []string
	lines = append(lines, "id\tagent_type\ttitle\tworkspace")
	lines = append(lines, "--\t----------\t-----\t---------")
	for rows.Next() {
		var paneID, agentType, title, workspace string
		if err := rows.Scan(&paneID, &agentType, &title, &workspace); err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", shortPaneID(paneID), agentType, title, workspace))
	}
	return strings.Join(lines, "\n")
}
