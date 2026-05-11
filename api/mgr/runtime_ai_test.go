package main

import (
	"os"
	"testing"
)

func TestNormalizePaneConfigJSONStillNormalizesRuntimeAI(t *testing.T) {
	withTempCicyRoot(t)
	body := `{
  "providers": {
    "items": [
      {
        "name": "OpenAI Default",
        "key": "openai-default",
        "url": "https://openai.example/v1",
        "apiKey": "test-openai",
        "protocol": "openai",
        "defaultModel": "gpt-5.5"
      }
    ]
  }
}`
	if err := os.WriteFile(cicyGlobalJSONPath, []byte(body), 0644); err != nil {
		t.Fatalf("write global.json: %v", err)
	}
	got, err := normalizePaneConfigJSON(`{"runtime_ai":{"provider_name":"openai-default"}}`)
	if err != nil {
		t.Fatalf("normalizePaneConfigJSON returned error: %v", err)
	}
	if got == "" {
		t.Fatalf("normalizePaneConfigJSON returned empty string")
	}
}
