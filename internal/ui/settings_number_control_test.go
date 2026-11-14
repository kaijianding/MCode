package mcodeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"mcode/internal/config"
)

// newNumberFieldTestState 返回处于「按比例」档的外观页与配置目录，比例宽度输入可用。
func newNumberFieldTestState(t *testing.T) (*SettingsState, *ui.Tester, string) {
	t.Helper()
	dataDir := settingsTestDir(t)
	st := NewSettingsState(dataDir, nil, newChatState(NewFakeClient()))
	st.cfg.Appearance.ContentWidth = config.ContentWidthFull
	tt := ui.NewTester(func(c *ui.Context) { renderPageAppearance(c, st) }, 1200, 1200)
	waitFor(t, tt, func() bool { return tt.HasText("比例宽度") })
	return st, tt, dataDir
}

// numberFieldText 是输入框显示的文字：外观页上第一个单行文本输入框就是比例宽度。
func numberFieldText(tt *ui.Tester) string {
	a, ok := tt.AccessByRole(ui.RoleTextField, "")
	if !ok {
		return ""
	}
	return a.Value
}

// TestNumberFieldAcceptsTypedDigits 逐位输入 75，失焦后配置与磁盘都是 75。
// 旧实现每次按键都把越界的中间值（「7」）clamp 到下限 60 再回填输入框，
// 下一位按键就拼到 60 上，用户看到的永远是「1」开头的三位数。
func TestNumberFieldAcceptsTypedDigits(t *testing.T) {
	st, tt, dataDir := newNumberFieldTestState(t)
	if err := tt.Click("85"); err != nil {
		t.Fatalf("click percent input failed: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	for _, ch := range []string{"7", "5"} {
		tt.Type(ch)
	}
	tt.Frame()
	if got := st.cfg.Appearance.ContentWidthPercent; got != config.DefaultContentWidthPercent {
		t.Fatalf("编辑中途就写了配置：%d，期望保持 %d", got, config.DefaultContentWidthPercent)
	}
	if got := numberFieldText(tt); got != "75" {
		t.Fatalf("输入框没有显示正在输入的 75：%q", got)
	}
	if err := tt.Click("紧凑侧边栏"); err != nil {
		t.Fatalf("click elsewhere failed: %v", err)
	}
	waitFor(t, tt, func() bool { return st.cfg.Appearance.ContentWidthPercent == 75 })
	waitFor(t, tt, func() bool {
		saved, err := config.Load(dataDir)
		return err == nil && saved.Appearance.ContentWidthPercent == 75
	})
}

// TestNumberFieldRevertsOutOfRangeOnBlur 越界输入不改配置，失焦后输入框回到配置里的值。
// 不允许替用户改成边界值：900 不会变成 100，而是回到原来的 85。
func TestNumberFieldRevertsOutOfRangeOnBlur(t *testing.T) {
	st, tt, dataDir := newNumberFieldTestState(t)
	if err := tt.Click("85"); err != nil {
		t.Fatalf("click percent input failed: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	for _, ch := range []string{"9", "0", "0"} {
		tt.Type(ch)
	}
	tt.Frame()
	if got := st.cfg.Appearance.ContentWidthPercent; got != config.DefaultContentWidthPercent {
		t.Fatalf("编辑中途就写了配置：%d，期望保持 %d", got, config.DefaultContentWidthPercent)
	}
	if got := numberFieldText(tt); got != "900" {
		t.Fatalf("输入框没有显示正在输入的 900：%q", got)
	}
	// 点到页面上别处让输入框失焦。
	if err := tt.Click("紧凑侧边栏"); err != nil {
		t.Fatalf("click elsewhere failed: %v", err)
	}
	// 一次失焦就回到原来的值，不需要再失焦第二次。
	if got := numberFieldText(tt); got != "85" {
		t.Fatalf("失焦后输入框显示 %q，期望回到 85", got)
	}
	waitFor(t, tt, func() bool {
		_, editing := st.numberDraft("row-content-width-percent")
		return !editing
	})
	if got := st.cfg.Appearance.ContentWidthPercent; got != config.DefaultContentWidthPercent {
		t.Fatalf("越界输入被写进配置：%d，期望保持 %d", got, config.DefaultContentWidthPercent)
	}
	saved, err := config.Load(dataDir)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if saved.Appearance.ContentWidthPercent != config.DefaultContentWidthPercent {
		t.Fatalf("越界输入被落盘：%d", saved.Appearance.ContentWidthPercent)
	}
}

// TestNumberFieldRevertsOutOfRangeOnEnter 回车定稿与失焦同一条路径：越界输入被丢弃。
func TestNumberFieldRevertsOutOfRangeOnEnter(t *testing.T) {
	st, tt, _ := newNumberFieldTestState(t)
	if err := tt.Click("85"); err != nil {
		t.Fatalf("click percent input failed: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	for _, ch := range []string{"9", "0", "0"} {
		tt.Type(ch)
	}
	tt.Key(0, ui.KeyEnter)
	if got := numberFieldText(tt); got != "85" {
		t.Fatalf("回车后输入框显示 %q，期望回到 85", got)
	}
	waitFor(t, tt, func() bool {
		_, editing := st.numberDraft("row-content-width-percent")
		return !editing
	})
	if got := st.cfg.Appearance.ContentWidthPercent; got != config.DefaultContentWidthPercent {
		t.Fatalf("回车把越界输入写进了配置：%d，期望保持 %d", got, config.DefaultContentWidthPercent)
	}
}
