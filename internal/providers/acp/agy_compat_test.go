package acp

import (
	"encoding/hex"
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

func TestAgyStepUsage(t *testing.T) {
	const metadataHex = "0A0C088C81F9D70610E09F84C8011802320C088E81F9D70610D8C8DF82023A0C089081F9D7061088F5DCA303420C089081F9D7061088F5DCA3034A7A08F50B10F81218BB0528CA8B0530183A28626F742D30613835656136632D313565332D343763352D393666642D62616662653464653764623142210A0973657373696F6E494412142D333735303736333033343336323839353537394830508B055A176A55442D6176544343762D3376723050704B5F6169416B58F50B622461346337363939302D396333622D346137322D623866312D363437303837383364356664A201500A2463336165636463662D346635342D343263642D383764362D386334323831636536343562103E1816222430376165323134362D393733302D343137392D623961302D343530303631306136663466D201240A100808120C088E81F9D70610A8C9E482020A100803120C089081F9D70610E8CFDEA30382020C089081F9D7061088F5DCA3039A02203466666339373835376136373432323361396631646534316131343933643938"
	want := acpPromptUsage{InputTokens: 2424, OutputTokens: 699, CachedReadTokens: 83402, ThoughtTokens: 48}
	for _, test := range []struct {
		name string
		hex  string
		want acpPromptUsage
	}{
		{name: "agy metadata", hex: metadataHex, want: want},
		{name: "unknown fixed widths and group", hex: metadataHex + "0900000000000000001500000000334a02100934", want: want},
		{name: "truncated metadata", hex: metadataHex[:len(metadataHex)-2]},
		{name: "truncated usage varint", hex: "4a03100918"},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := hex.DecodeString(test.hex)
			if err != nil {
				t.Fatal(err)
			}
			if got := agyStepUsage(metadata); got != test.want {
				t.Fatalf("usage = %+v, want %+v", got, test.want)
			}
		})
	}
}
