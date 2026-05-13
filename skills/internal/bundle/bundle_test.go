package bundle

import "testing"

func TestAgentWebpageLegacyAliasesAreRetired(t *testing.T) {
	for _, banned := range []string{"webpage", "webpage-ping", "ipc-ping"} {
		for _, name := range HosttoolAliases {
			if name == banned {
				t.Fatalf("HosttoolAliases should not contain %q", banned)
			}
		}
		foundRetired := false
		for _, name := range RetiredLocalLinks {
			if name == banned {
				foundRetired = true
				break
			}
		}
		if !foundRetired {
			t.Fatalf("RetiredLocalLinks should contain %q", banned)
		}
	}
}

func TestCodeServerBinaryIsNotRetired(t *testing.T) {
	for _, name := range RetiredLocalLinks {
		if name == "code-server" {
			t.Fatalf("RetiredLocalLinks should not contain %q", name)
		}
	}
}
