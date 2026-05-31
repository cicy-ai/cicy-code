package skillcmd

import (
	"os"
	"testing"
)

// isolateHome points HOME at a temp dir and clears the single-source env
// override so source tests hit registries.json.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CICY_SKILLS_REGISTRY", "")
	t.Setenv("CICY_SKILLS_REGISTRY_TOKEN", "")
}

func TestEffectiveSourcesDefaultsToPublic(t *testing.T) {
	isolateHome(t)
	srcs := effectiveSources()
	if len(srcs) != 1 || srcs[0].Name != publicSourceName || srcs[0].URL != DefaultRegistry {
		t.Fatalf("default sources = %+v, want [public]", srcs)
	}
}

func TestEnvOverrideSingleSource(t *testing.T) {
	isolateHome(t)
	t.Setenv("CICY_SKILLS_REGISTRY", "http://example:1234")
	t.Setenv("CICY_SKILLS_REGISTRY_TOKEN", "tok")
	srcs := effectiveSources()
	if len(srcs) != 1 || srcs[0].URL != "http://example:1234" || srcs[0].Token != "tok" {
		t.Fatalf("env override = %+v", srcs)
	}
}

func TestAddSeedsPublicThenAppends(t *testing.T) {
	isolateHome(t)
	if err := cmdRegistryAdd([]string{"http://team-a:8787/", "--name", "team-a", "--token", "TA"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	srcs, err := loadSources()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(srcs) != 2 {
		t.Fatalf("want 2 sources (public+team-a), got %d: %+v", len(srcs), srcs)
	}
	// public seeded first (lower precedence), team-a appended last (higher)
	if srcs[0].Name != publicSourceName {
		t.Errorf("source[0]=%q, want public", srcs[0].Name)
	}
	last := srcs[len(srcs)-1]
	if last.Name != "team-a" || last.URL != "http://team-a:8787" || last.Token != "TA" {
		t.Errorf("team-a entry wrong: %+v", last)
	}
}

func TestAddUpsertsSameName(t *testing.T) {
	isolateHome(t)
	_ = cmdRegistryAdd([]string{"http://old:1", "--name", "team-a", "--token", "OLD"})
	_ = cmdRegistryAdd([]string{"http://new:2", "--name", "team-a", "--token", "NEW"})
	srcs, _ := loadSources()
	count := 0
	for _, s := range srcs {
		if s.Name == "team-a" {
			count++
			if s.URL != "http://new:2" || s.Token != "NEW" {
				t.Errorf("upsert kept stale value: %+v", s)
			}
		}
	}
	if count != 1 {
		t.Errorf("team-a appears %d times, want 1", count)
	}
}

func TestRemoveSource(t *testing.T) {
	isolateHome(t)
	_ = cmdRegistryAdd([]string{"http://team-a:8787", "--name", "team-a", "--token", "TA"})
	if err := cmdRegistryRemove([]string{"team-a"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	srcs, _ := loadSources()
	for _, s := range srcs {
		if s.Name == "team-a" {
			t.Errorf("team-a still present after remove")
		}
	}
	// removing a missing source errors
	if err := cmdRegistryRemove([]string{"nope"}); err == nil {
		t.Errorf("remove missing: want error, got nil")
	}
}

func TestRegistriesFilePermissions(t *testing.T) {
	isolateHome(t)
	_ = cmdRegistryAdd([]string{"http://team-a:8787", "--name", "team-a", "--token", "TA"})
	info, err := os.Stat(registriesJSONPath())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("registries.json perm = %o, want 600 (holds tokens)", info.Mode().Perm())
	}
}

func TestInstallParentDir(t *testing.T) {
	isolateHome(t)
	root := skillsRoot()
	cases := []struct {
		reg  *Registry
		want string
	}{
		{&Registry{Name: "public", BaseURL: DefaultRegistry}, root},
		{&Registry{Name: "anything", BaseURL: "https://skills.cicy-ai.com"}, root},
		{&Registry{Name: "mine", BaseURL: "http://localhost:8787"}, privateSkillsParent()},
		{&Registry{Name: "mine2", BaseURL: "http://127.0.0.1:9000"}, privateSkillsParent()},
		{&Registry{Name: "team-a", BaseURL: "http://10.0.0.5:8787"}, teamSkillsParent("team-a")},
		{&Registry{Name: "team-b", BaseURL: "http://team-b.corp:8787"}, teamSkillsParent("team-b")},
		{nil, root},
	}
	for _, c := range cases {
		if got := installParentDir(c.reg); got != c.want {
			t.Errorf("installParentDir(%+v) = %q, want %q", c.reg, got, c.want)
		}
	}
}

func TestSameHostScoping(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"http://h:8787/v1/x/download", "http://h:8787", true},
		{"https://github.com/y.zip", "http://h:8787", false},
		{"http://h:8788/x", "http://h:8787", false}, // different port
	}
	for _, c := range cases {
		if got := sameHost(c.a, c.b); got != c.want {
			t.Errorf("sameHost(%q,%q)=%v, want %v", c.a, c.b, got, c.want)
		}
	}
}
