package skills

import "testing"

func TestBaseSkillNameIgnoresHelpDocs(t *testing.T) {
	if got := baseSkillName("help.md"); got != "" {
		t.Fatalf("baseSkillName(help.md) = %q, want empty", got)
	}
}
