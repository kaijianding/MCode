package mcodeui

import "testing"

func TestAgyChannelNeedsUpdate(t *testing.T) {
	for _, tc := range []struct {
		setting, version string
		want             bool
	}{
		{"", "1.5.7", true}, {"acp", "agy 1.4.9", true}, {"printmode", "1.5.7", false},
		{"", "1.5.8", false}, {"acp", "1.6.0", false}, {"acp", "", false}, {"acp", "unknown", false},
	} {
		if got := agyChannelNeedsUpdate(tc.setting, tc.version); got != tc.want {
			t.Errorf("setting %q version %q: got %v", tc.setting, tc.version, got)
		}
	}
}

func TestAgyChannelSearchIndex(t *testing.T) {
	for query, id := range map[string]string{"PrintMode": "row-agy-channel", "需要 1.5.8 以上": "row-agy-channel-update"} {
		found := false
		for _, hit := range matchSettingsItems(query) {
			if hit.Item.ID == id && hit.Item.Page == "models" && hit.Item.Sheet == "provider" {
				found = true
			}
		}
		if !found {
			t.Errorf("search %q did not find %s", query, id)
		}
	}
}

func TestAgyProviderSheetHasChannelSection(t *testing.T) {
	st := NewSettingsState(settingsTestDir(t), nil, newChatState(NewFakeClient()))
	for _, section := range agyProviderSheetSections(st) {
		if section.ID == "sec-agy-channel" {
			if section.Advanced {
				t.Fatal("通道节不应属于高级设置")
			}
			return
		}
	}
	t.Fatal("缺少通道节")
}
