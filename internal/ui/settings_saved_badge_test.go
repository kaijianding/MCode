package mcodeui

import (
	"os"
	"path/filepath"
	"testing"

	"mcode/internal/config"
)

func TestSettingsSavedBadge(t *testing.T) {
	t.Cleanup(settingsSaveWriters.Wait)
	t.Run("设置保存成功", func(t *testing.T) {
		st := NewSettingsState(settingsTestDir(t), nil, nil)
		commitSettingsControl(st, func(cfg *config.Config) { cfg.Appearance.ReduceMotion = !cfg.Appearance.ReduceMotion })
		if !st.savedBadgeVisible() {
			t.Fatal("保存成功后应显示徽标")
		}
	})
	t.Run("后台保存", func(t *testing.T) {
		st := NewSettingsState(settingsTestDir(t), nil, nil)
		st.updateAndSave(func(cfg *config.Config) { cfg.Appearance.ReduceMotion = !cfg.Appearance.ReduceMotion })
		if st.savedBadgeVisible() || st.errorMessage != "" {
			t.Fatal("后台保存成功不应显示徽标")
		}
	})
	t.Run("保存失败", func(t *testing.T) {
		st := NewSettingsState(settingsTestDir(t), nil, nil)
		path := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		st.dataDir = path
		commitSettingsControl(st, func(cfg *config.Config) { cfg.Appearance.ReduceMotion = !cfg.Appearance.ReduceMotion })
		if st.savedBadgeVisible() || st.errorMessage == "" {
			t.Fatal("保存失败应显示错误且不显示徽标")
		}
	})
}
