package app

import (
	"os"
	"testing"
	"time"

	"mcode/internal/config"
)

func TestCachedSwarmSettings(t *testing.T) {
	dataDir := t.TempDir()
	cfg := config.Defaults()
	disabled := false
	cfg.Orchestration.Enabled = &disabled
	if err := config.Save(dataDir, cfg); err != nil {
		t.Fatalf("save disabled config: %v", err)
	}

	getSettings := newCachedSwarmSettings(dataDir)
	if got := getSettings(); got.Enabled {
		t.Fatalf("cached settings enabled = true, want false")
	}

	enabled := true
	cfg.Orchestration.Enabled = &enabled
	if err := config.Save(dataDir, cfg); err != nil {
		t.Fatalf("save enabled config: %v", err)
	}
	path := config.ConfigPath(dataDir)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	changed := info.ModTime().Add(time.Second)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatalf("change config mtime: %v", err)
	}
	if got := getSettings(); !got.Enabled {
		t.Fatalf("cached settings enabled = false after update, want true")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	if got := getSettings(); !got.Enabled {
		t.Fatalf("settings enabled = false after config removal, want default true")
	}
}
