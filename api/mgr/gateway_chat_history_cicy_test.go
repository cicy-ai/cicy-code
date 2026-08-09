package main

import "testing"

func TestAgentChatHistoryReadsCicyConversation(t *testing.T) {
	withTestStore(t)
	id := "w-cicy-history-test"
	workspace := t.TempDir()
	if _, err := store.Exec(`INSERT INTO agent_config(pane_id,workspace,agent_type) VALUES(?,?,?)`, id+":main.0", workspace, "cicy"); err != nil {
		t.Fatal(err)
	}
	session := getCicySession(id, workspace)
	session.mu.Lock()
	session.messages = []M{
		{"role": "user", "content": "first question"},
		{"role": "assistant", "content": []M{{"type": "text", "text": "first answer"}}},
		{"role": "user", "content": "latest question"},
		{"role": "assistant", "content": []M{{"type": "text", "text": "latest answer"}}},
	}
	session.mu.Unlock()

	latest, err := agentChatHistoryData(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if latest["found"] != true || latest["q"] != "latest question" || latest["a"] != "latest answer" {
		t.Fatalf("unexpected latest turn: %#v", latest)
	}
	previous, err := agentChatHistoryData(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if previous["q"] != "first question" || previous["a"] != "first answer" {
		t.Fatalf("unexpected previous turn: %#v", previous)
	}
}
