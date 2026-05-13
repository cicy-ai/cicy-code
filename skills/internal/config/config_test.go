package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultPathUsesCicyAISkillsDir(t *testing.T) {
	t.Setenv("HOME", "/tmp/cicy-home")
	if got, want := DefaultPath(), filepath.Join("/tmp/cicy-home", "cicy-ai", "skills", "config.json"); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}
