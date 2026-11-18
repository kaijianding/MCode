package acp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgySessionDBPrefersCLI(t *testing.T) {
	const sid = "3f2b8c1e-9a4d-4f6e-8b2c-1d5e7a9c0b3f"
	geminiDir := t.TempDir()
	cliDB := filepath.Join(geminiDir, "antigravity-cli", "conversations", sid+".db")
	acpDB := filepath.Join(geminiDir, "antigravity-acp", "conversations", sid+".db")

	writeDB := func(t *testing.T, dbPath string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dbPath, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	reset := func(t *testing.T) {
		t.Helper()
		_ = os.RemoveAll(filepath.Join(geminiDir, "antigravity-cli"))
		_ = os.RemoveAll(filepath.Join(geminiDir, "antigravity-acp"))
	}

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		want  string
	}{
		{name: "仅 cli", setup: func(t *testing.T) { writeDB(t, cliDB) }, want: cliDB},
		{name: "仅 acp", setup: func(t *testing.T) { writeDB(t, acpDB) }, want: acpDB},
		{name: "两者都有", setup: func(t *testing.T) { writeDB(t, cliDB); writeDB(t, acpDB) }, want: cliDB},
		{name: "两者都没有", setup: func(t *testing.T) {}, want: cliDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reset(t)
			tc.setup(t)
			if got := agySessionDB(geminiDir, sid); got != tc.want {
				t.Fatalf("agySessionDB = %q, want %q", got, tc.want)
			}
		})
	}

	reset(t)
	if got := agySessionDB(geminiDir, "../../etc/passwd"); got != "" {
		t.Fatalf("非法 sid 返回 %q, want 空串", got)
	}
}
