package app

import (
	"os"
	"testing"

	"mcode/internal/config"
)

func writeStewardName(t *testing.T, dir, name string) {
	t.Helper()
	body := `{"orchestration":{"steward":{"enabled":true,"name":"` + name + `"}}}`
	if err := os.WriteFile(config.ConfigPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 界面每帧读配置，所以 loadConfig 命中缓存时不能重新解析文件；文件状态变了必须重读。
func TestLoadConfigCache(t *testing.T) {
	dir := t.TempDir()
	writeStewardName(t, dir, "aaaa")
	client := &Client{dataDir: dir}

	if name, _ := client.StewardIdentity(); name != "aaaa" {
		t.Fatalf("first read = %q, want aaaa", name)
	}

	// 同大小、mtime 拨回旧值：文件状态与缓存一致，必须走缓存而不是重新解析。
	st, err := os.Stat(config.ConfigPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	writeStewardName(t, dir, "bbbb")
	if err := os.Chtimes(config.ConfigPath(dir), st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if name, _ := client.StewardIdentity(); name != "aaaa" {
		t.Errorf("unchanged file state = %q, want cached aaaa", name)
	}

	// 文件状态变了（大小不同）必须重读。
	writeStewardName(t, dir, "cccc")
	if name, _ := client.StewardIdentity(); name != "cccc" {
		t.Errorf("changed file = %q, want cccc", name)
	}

	// 写入方主动通知时立即失效，不必等文件状态变化。
	writeStewardName(t, dir, "dddd")
	st, err = os.Stat(config.ConfigPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	writeStewardName(t, dir, "eeee")
	if err := os.Chtimes(config.ConfigPath(dir), st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	client.notifyConfigChanged()
	if name, _ := client.StewardIdentity(); name != "eeee" {
		t.Errorf("after notify = %q, want eeee", name)
	}
}

// 缓存不能把坏配置读成缓存里的旧值：文件损坏时上报失败，调用方沿用默认。
func TestLoadConfigCacheReportsBadFile(t *testing.T) {
	dir := t.TempDir()
	writeStewardName(t, dir, "aaaa")
	client := &Client{dataDir: dir}
	if _, ok := client.loadConfig(); !ok {
		t.Fatal("first load failed")
	}
	if err := os.WriteFile(config.ConfigPath(dir), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.loadConfig(); ok {
		t.Error("broken config reported as ok")
	}
}
