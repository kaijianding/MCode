package mcodeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// setupTerminalFindTester 构造带已选会话与桩终端的根视图测试器，用于验证 ⌘F 与菜单查找分流。
func setupTerminalFindTester(t *testing.T) (*ui.Tester, *chatState, *terminalState) {
	t.Helper()
	t.Setenv("MCODE_DATA", t.TempDir())
	stubTerminals(t)

	fake := NewFakeClient()
	fake.projects[0].Path = t.TempDir()
	state, view := buildRoot(fake, WithSettings(settingsTestDir(t), nil))
	t.Cleanup(state.close)

	tt := ui.NewTester(view, 1200, 800)
	waitFor(t, tt, func() bool { return tt.HasText("测试会话1") })
	return tt, state, getTerminalState(state)
}

// openAndFocusTerminalTab 展开终端 dock，等待活动终端视图完成布局并点击聚焦。
func openAndFocusTerminalTab(t *testing.T, tt *ui.Tester, state *chatState, st *terminalState) int {
	t.Helper()
	toggleTerminal(state)
	waitFor(t, tt, func() bool {
		r, ok := tt.Find("Terminal")
		return ok && r.W > 0 && r.H > 0
	})
	if err := tt.Click("Terminal"); err != nil {
		t.Fatalf("点击终端视图失败: %v", err)
	}
	tt.Frame()
	if !tt.Focused("Terminal") {
		t.Fatal("点击后终端视图应获得焦点")
	}
	terminalMu.Lock()
	focused := st.focused
	tab := activeTerminalTab(st, state.selectedThreadID)
	terminalMu.Unlock()
	if !focused {
		t.Fatal("终端视图聚焦后 terminalState.focused 应为 true")
	}
	if tab == nil {
		t.Fatal("应存在活动的终端标签")
	}
	return tab.id
}

// TestUIReview24_MenuFindInThreadOpensTerminalSearchWhenFocused 验证终端聚焦且有活动标签时，
// 菜单动作 find-in-thread 打开终端搜索条而不打开会话内查找。
func TestUIReview24_MenuFindInThreadOpensTerminalSearchWhenFocused(t *testing.T) {
	tt, state, st := setupTerminalFindTester(t)
	tabID := openAndFocusTerminalTab(t, tt, state, st)

	state.enqueueAction("find-in-thread", 0)
	tt.Frame()

	terminalMu.Lock()
	search := st.search
	terminalMu.Unlock()
	if !search.open || search.tabID != tabID {
		t.Fatalf("菜单 find-in-thread 应打开绑定标签 %d 的终端搜索条，实际 %+v", tabID, search)
	}
	if _, ok := tt.Find("搜索终端输出"); !ok {
		t.Fatalf("界面应显示终端搜索输入框，texts=%v", tt.Texts())
	}
	if getChatFindState(state).open {
		t.Fatal("终端聚焦时菜单 find-in-thread 不应打开会话内查找")
	}
}

// TestUIReview24_MenuFindInThreadOpensChatFindWhenUnfocused 验证终端未聚焦时，
// 菜单动作 find-in-thread 打开会话内查找而不打开终端搜索条。
func TestUIReview24_MenuFindInThreadOpensChatFindWhenUnfocused(t *testing.T) {
	t.Run("终端未打开", func(t *testing.T) {
		tt, state, st := setupTerminalFindTester(t)

		state.enqueueAction("find-in-thread", 0)
		tt.Frame()

		if !getChatFindState(state).open {
			t.Fatal("终端未打开时菜单 find-in-thread 应打开会话内查找")
		}
		terminalMu.Lock()
		termSearchOpen := st.search.open
		terminalMu.Unlock()
		if termSearchOpen {
			t.Fatal("终端未打开时不应打开终端搜索条")
		}
	})

	t.Run("终端已打开但焦点在输入框", func(t *testing.T) {
		tt, state, st := setupTerminalFindTester(t)
		_ = openAndFocusTerminalTab(t, tt, state, st)

		if err := tt.Click("消息输入框"); err != nil {
			t.Fatalf("点击消息输入框失败: %v", err)
		}
		tt.Frame()
		terminalMu.Lock()
		focused := st.focused
		terminalMu.Unlock()
		if focused {
			t.Fatal("焦点切回消息输入框后 terminalState.focused 应为 false")
		}

		state.enqueueAction("find-in-thread", 0)
		tt.Frame()

		if !getChatFindState(state).open {
			t.Fatal("终端失焦后菜单 find-in-thread 应打开会话内查找")
		}
		terminalMu.Lock()
		termSearchOpen := st.search.open
		terminalMu.Unlock()
		if termSearchOpen {
			t.Fatal("终端失焦后不应打开终端搜索条")
		}
	})
}

// TestUIReview24_CmdFFocusTerminalOpensTerminalSearch 验证在终端视图聚焦时按下 ⌘F，
// 会打开终端搜索条而非会话内查找；收起终端后焦点标记同步清除。
func TestUIReview24_CmdFFocusTerminalOpensTerminalSearch(t *testing.T) {
	tt, state, st := setupTerminalFindTester(t)
	tabID := openAndFocusTerminalTab(t, tt, state, st)

	mods, key, ok := shortcutBindingFor("find-in-thread")
	if !ok {
		t.Fatal("缺少 find-in-thread 快捷键绑定")
	}
	tt.Key(mods, key)
	tt.Frame()

	terminalMu.Lock()
	search := st.search
	terminalMu.Unlock()
	if !search.open || search.tabID != tabID {
		t.Fatalf("终端聚焦时按 ⌘F 应打开绑定标签 %d 的终端搜索条，实际 %+v", tabID, search)
	}
	if _, ok := tt.Find("搜索终端输出"); !ok {
		t.Fatalf("界面应显示终端搜索输入框，texts=%v", tt.Texts())
	}
	if getChatFindState(state).open {
		t.Fatal("终端聚焦时按 ⌘F 不应打开会话内查找")
	}

	// 收起终端后，不再渲染终端视图，focused 状态应回到 false。
	toggleTerminal(state)
	tt.Frame()
	terminalMu.Lock()
	focused := st.focused
	terminalMu.Unlock()
	if focused {
		t.Fatal("收起终端后 terminalState.focused 应重置为 false")
	}
}
