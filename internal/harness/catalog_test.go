package harness

import "testing"

func TestIsCLICatalogProvider(t *testing.T) {
	t.Run("builtin and unknown ids", func(t *testing.T) {
		for _, tc := range []struct {
			id   string
			want bool
		}{
			{"commandcode", true}, {"cline", true}, {"pi", true}, {"omp", true},
			{"copilot", true}, {"droid", true}, {"cursor", true}, {"opencode", true},
			{"grok", true}, {"devin", true}, {"mcode", false}, {"custom-test", false},
			{"anthropic", false}, {"deepseek", false},
			{"not-registered-agent-xyz", false},
		} {
			if got := IsCLICatalogProvider(tc.id); got != tc.want {
				t.Errorf("%s: got %v, want %v", tc.id, got, tc.want)
			}
		}
	})

	t.Run("registered custom ids", func(t *testing.T) {
		const id = "maki-test-agent-xyz"
		dynamicCatalogProviders.Delete(id)
		t.Cleanup(func() { dynamicCatalogProviders.Delete(id) })
		if IsCLICatalogProvider(id) {
			t.Fatalf("unregistered id must not be treated as CLI catalog provider")
		}
		RegisterCLICatalogProvider(" Maki-Test-Agent-XYZ ")
		if !IsCLICatalogProvider("maki-test-agent-xyz") {
			t.Fatalf("registered id must be treated as CLI catalog provider")
		}
		if !IsCLICatalogProvider("MAKI-TEST-AGENT-XYZ") {
			t.Fatalf("registration must normalize case and surrounding space")
		}
	})

	t.Run("empty ids are ignored", func(t *testing.T) {
		RegisterCLICatalogProvider("   ")
		if IsCLICatalogProvider("") {
			t.Fatalf("empty provider must not be registered")
		}
	})
}
