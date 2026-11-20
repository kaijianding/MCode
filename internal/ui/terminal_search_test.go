package mcodeui

import (
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
	"mcode/internal/config"
)

func terminalSearchOpenModsForTest() ui.Modifiers {
	if runtime.GOOS == "darwin" {
		return ui.Cmd
	}
	return ui.Ctrl | ui.Shift
}

// 搜索条打开：绑定当前标签，搜索词与匹配计数从头开始。
func TestTerminalSearchStateOpened(t *testing.T) {
	var state terminalSearchState
	state = state.withText("panic")

	opened := state.opened(7)
	if !opened.open {
		t.Fatalf("opened 后应为打开状态")
	}
	if opened.tabID != 7 {
		t.Fatalf("opened 应绑定标签 7，得到 %d", opened.tabID)
	}
	if opened.text != "" || opened.total != 0 || opened.selected != 0 || opened.searching {
		t.Fatalf("opened 应清空搜索词与计数，得到 %+v", opened)
	}
}

// 关闭搜索条：清掉匹配计数与绑定标签，保留搜索词，方便下次再打开。
func TestTerminalSearchStateClosed(t *testing.T) {
	state := terminalSearchState{open: true, tabID: 3}.
		withText("build").
		withStatus("build", 12, 4, false)

	closed := state.closed()
	if closed.open || closed.tabID != 0 {
		t.Fatalf("closed 后应关闭且不绑定标签，得到 %+v", closed)
	}
	if closed.total != 0 || closed.selected != 0 || closed.searching {
		t.Fatalf("closed 应清空计数，得到 %+v", closed)
	}
	if closed.text != "build" {
		t.Fatalf("closed 应保留搜索词，得到 %+v", closed)
	}
}

// 改搜索词：词变了就重置计数并进入搜索中；同一个词不重置正在进行的搜索。
func TestTerminalSearchStateWithText(t *testing.T) {
	state := terminalSearchState{open: true}.
		withText("err").
		withStatus("err", 5, 2, false)

	same := state.withText("err")
	if same != state {
		t.Fatalf("同一个搜索词不应改动状态，得到 %+v", same)
	}

	other := state.withText("warn")
	if other.total != 0 || other.selected != 0 || !other.searching {
		t.Fatalf("换词应重置计数并进入搜索中，得到 %+v", other)
	}

	cleared := state.withText("")
	if cleared.text != "" || cleared.searching || cleared.total != 0 {
		t.Fatalf("清空搜索词应回到未搜索状态，得到 %+v", cleared)
	}
}

// 搜索状态回报：只接受与当前搜索词一致的状态，旧词的迟到状态直接忽略。
func TestTerminalSearchStateWithStatus(t *testing.T) {
	state := terminalSearchState{open: true}.withText("abc")

	stale := state.withStatus("ab", 9, 3, false)
	if stale != state {
		t.Fatalf("旧词的状态应被忽略，得到 %+v", stale)
	}

	fresh := state.withStatus("abc", 4, 1, true)
	if fresh.total != 4 || fresh.selected != 1 || !fresh.searching {
		t.Fatalf("应写入新的匹配计数，得到 %+v", fresh)
	}
}

// 匹配计数文案：无词为空、搜索中提示进度、无匹配明说、有匹配显示「第几个/共几个」。
func TestTerminalSearchStateMatchLabel(t *testing.T) {
	cases := []struct {
		name  string
		state terminalSearchState
		want  string
	}{
		{name: "无搜索词", state: terminalSearchState{}, want: ""},
		{name: "搜索中", state: terminalSearchState{text: "a", searching: true}, want: "搜索中…"},
		{name: "无匹配", state: terminalSearchState{text: "a"}, want: "无匹配"},
		{name: "只有总数", state: terminalSearchState{text: "a", total: 3}, want: "3 个匹配"},
		{name: "第几个", state: terminalSearchState{text: "a", total: 3, selected: 2}, want: "2/3"},
	}
	for _, tc := range cases {
		if got := tc.state.matchLabel(); got != tc.want {
			t.Errorf("%s: matchLabel()=%q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// 终端字号夹取：零值与负值取默认，越界夹到上下界，合法值原样返回。
func TestClampTerminalFontSize(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{in: 0, want: config.DefaultTerminalFontSize},
		{in: -3, want: config.DefaultTerminalFontSize},
		{in: config.MinTerminalFontSize - 1, want: config.MinTerminalFontSize},
		{in: config.MinTerminalFontSize, want: config.MinTerminalFontSize},
		{in: config.DefaultTerminalFontSize, want: config.DefaultTerminalFontSize},
		{in: config.MaxTerminalFontSize, want: config.MaxTerminalFontSize},
		{in: config.MaxTerminalFontSize + 1, want: config.MaxTerminalFontSize},
	}
	for _, tc := range cases {
		if got := clampTerminalFontSize(tc.in); got != tc.want {
			t.Errorf("clampTerminalFontSize(%d)=%d，期望 %d", tc.in, got, tc.want)
		}
	}
}

// openTerminalSearchIfFocused 仅在终端可见、聚焦且存在活动标签时打开搜索条。
func TestOpenTerminalSearchIfFocused(t *testing.T) {
	stubTerminals(t)
	if openTerminalSearchIfFocused(nil) {
		t.Fatal("nil chatState 不应打开终端搜索")
	}

	s := newChatState(NewFakeClient())
	defer s.close()
	s.selectedThreadID = "t1"
	st := getTerminalState(s)

	if openTerminalSearchIfFocused(s) {
		t.Fatal("未展开且无标签时不应打开终端搜索")
	}

	tab, err := createTerminalTab(s, st, t.TempDir(), false)
	if err != nil {
		t.Fatalf("createTerminalTab 失败: %v", err)
	}

	terminalMu.Lock()
	st.visible = false
	st.focused = true
	terminalMu.Unlock()
	if openTerminalSearchIfFocused(s) {
		t.Fatal("终端收起时不应打开终端搜索")
	}

	terminalMu.Lock()
	st.visible = true
	st.focused = false
	terminalMu.Unlock()
	if openTerminalSearchIfFocused(s) {
		t.Fatal("终端未聚焦时不应打开终端搜索")
	}

	terminalMu.Lock()
	st.focused = true
	terminalMu.Unlock()
	if !openTerminalSearchIfFocused(s) {
		t.Fatal("终端可见且聚焦并有活动标签时应返回 true")
	}

	terminalMu.Lock()
	search := st.search
	terminalMu.Unlock()
	if !search.open || search.tabID != tab.id {
		t.Fatalf("期望打开绑定标签 %d 的搜索条，实际 %+v", tab.id, search)
	}
}

// handleTerminalSearchKey 与 openTerminalSearchIfFocused 共用打开逻辑，按平台热键打开并在 Esc 时关闭。
func TestHandleTerminalSearchKeyOpensAndCloses(t *testing.T) {
	stubTerminals(t)
	s := newChatState(NewFakeClient())
	defer s.close()
	s.selectedThreadID = "t1"
	st := getTerminalState(s)
	tab, err := createTerminalTab(s, st, t.TempDir(), false)
	if err != nil {
		t.Fatalf("createTerminalTab 失败: %v", err)
	}

	if !handleTerminalSearchKey(s, terminalSearchOpenModsForTest(), ui.KeyF) {
		t.Fatal("终端搜索快捷键应返回 true")
	}
	terminalMu.Lock()
	opened := st.search
	terminalMu.Unlock()
	if !opened.open || opened.tabID != tab.id {
		t.Fatalf("快捷键后搜索条应绑定标签 %d 并打开，实际 %+v", tab.id, opened)
	}

	if !handleTerminalSearchKey(s, 0, ui.KeyEscape) {
		t.Fatal("Esc 应关闭已打开的终端搜索条")
	}
	terminalMu.Lock()
	closed := st.search
	terminalMu.Unlock()
	if closed.open {
		t.Fatalf("Esc 后搜索条应关闭，实际 %+v", closed)
	}
}
