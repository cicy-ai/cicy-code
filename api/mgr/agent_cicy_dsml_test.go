package main

import (
	"encoding/json"
	"testing"
)

// The exact leak observed in w-10073's conversation (2026-06-07): DeepSeek
// emitted its DSML tool-call markup as plain text — fullwidth pipes, two
// invokes, one with typed parameters.
const dsmlLeakSample = "嗨，让我看看有没有新动静。\n\n" +
	"<｜｜DSML｜｜tool_calls>\n" +
	"<｜｜DSML｜｜invoke name=\"a2a_status\">\n" +
	"</｜｜DSML｜｜invoke>\n" +
	"<｜｜DSML｜｜invoke name=\"agent_capture\">\n" +
	"<｜｜DSML｜｜parameter name=\"lines\" string=\"false\">5</｜｜DSML｜｜parameter>\n" +
	"<｜｜DSML｜｜parameter name=\"pane_id\" string=\"true\">w-1001</｜｜DSML｜｜parameter>\n" +
	"</｜｜DSML｜｜invoke>\n" +
	"</｜｜DSML｜｜tool_calls>"

func TestCicyRescueDSML(t *testing.T) {
	blocks := []interface{}{
		map[string]interface{}{"type": "text", "text": dsmlLeakSample},
	}
	out, ok := cicyRescueDSML(blocks, 0)
	if !ok {
		t.Fatal("expected rescue to trigger")
	}
	if len(out) != 3 {
		raw, _ := json.Marshal(out)
		t.Fatalf("expected [text, tool_use, tool_use], got %d blocks: %s", len(out), raw)
	}
	txt := out[0].(map[string]interface{})
	if txt["type"] != "text" || txt["text"] != "嗨，让我看看有没有新动静。" {
		t.Fatalf("prose not preserved/cleaned: %#v", txt)
	}
	tu1 := out[1].(map[string]interface{})
	if tu1["type"] != "tool_use" || tu1["name"] != "a2a_status" {
		t.Fatalf("first invoke wrong: %#v", tu1)
	}
	if len(tu1["input"].(map[string]interface{})) != 0 {
		t.Fatalf("a2a_status should have empty input: %#v", tu1["input"])
	}
	tu2 := out[2].(map[string]interface{})
	if tu2["name"] != "agent_capture" {
		t.Fatalf("second invoke wrong: %#v", tu2)
	}
	in2 := tu2["input"].(map[string]interface{})
	if in2["pane_id"] != "w-1001" {
		t.Fatalf("string param wrong: %#v", in2)
	}
	if n, okN := in2["lines"].(float64); !okN || n != 5 {
		t.Fatalf("typed (string=\"false\") param should decode as number 5: %#v", in2["lines"])
	}
	if tu1["id"] == tu2["id"] {
		t.Fatal("tool_use ids must be distinct")
	}
}

func TestCicyRescueDSMLAsciiVariant(t *testing.T) {
	blocks := []interface{}{
		map[string]interface{}{"type": "text", "text": "ok\n<||DSML||tool_calls>\n<||DSML||invoke name=\"todo_list\">\n</||DSML||invoke>\n</||DSML||tool_calls>"},
	}
	out, ok := cicyRescueDSML(blocks, 2)
	if !ok || len(out) != 2 {
		t.Fatalf("ascii variant not rescued: ok=%v blocks=%d", ok, len(out))
	}
	if out[1].(map[string]interface{})["name"] != "todo_list" {
		t.Fatalf("wrong tool name: %#v", out[1])
	}
}

func TestCicyRescueDSMLNoFalsePositive(t *testing.T) {
	blocks := []interface{}{
		map[string]interface{}{"type": "text", "text": "普通回复,没有任何标记。"},
		map[string]interface{}{"type": "tool_use", "id": "x", "name": "todo_list", "input": map[string]interface{}{}},
	}
	if _, ok := cicyRescueDSML(blocks, 0); ok {
		t.Fatal("must not rescue clean blocks")
	}
}
