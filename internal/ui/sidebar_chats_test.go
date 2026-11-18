package mcodeui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mcode/internal/testenv"
)

// (a) 只有对话项目、没有其他项目时，触发 createNewThread 会在对话项目创建会话，不弹失败 toast。
func TestChatsCreateNewThreadFallback(t *testing.T) {
	testenv.TempHome(t)
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	fake.SetProjectsAndThreads(
		[]Project{{ID: "chats-builtin", Name: "对话"}},
		map[string][]Thread{},
	)
	fake.SetProviders([]ProviderInfo{{ID: "claude", Name: "Claude", Available: true}})

	state := newChatState(fake)
	t.Cleanup(state.close)
	state.mu.Lock()
	state.projects = []Project{{ID: "chats-builtin", Name: "对话"}}
	state.newThreadProvider = "claude"
	state.mu.Unlock()

	state.createNewThread()

	waitUntil(t, "对话项目创建会话", func() bool {
		return fake.CreateThreadCalls() == 1
	})
	if got := fake.LastCreatedProjectID(); got != "chats-builtin" {
		t.Fatalf("应在对话项目创建会话, got %q", got)
	}
	state.mu.Lock()
	toast := state.toastMessage
	state.mu.Unlock()
	if toast != "" {
		t.Fatalf("不应弹出失败 toast, got %q", toast)
	}
}

// (b) 有普通项目且没有选中会话时，⌘N 仍落在普通项目。
func TestChatsCmdNUsesRegularProjectWhenUnselected(t *testing.T) {
	testenv.TempHome(t)
	withShortcutGOOS(t, "darwin")
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "对话"},
			{ID: "p1", Name: "普通项目"},
		},
		map[string][]Thread{},
	)
	fake.SetProviders([]ProviderInfo{{ID: "claude", Name: "Claude", Available: true}})

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return len(state.projects) == 2
	})

	state.mu.Lock()
	state.selectedThreadID = ""
	state.mu.Unlock()

	tt.Key(ui.Cmd, ui.KeyN)
	waitFor(t, tt, func() bool {
		return fake.CreateThreadCalls() == 1
	})
	if got := fake.LastCreatedProjectID(); got != "p1" {
		t.Fatalf("⌘N 无选中会话时应落在普通项目 p1, got %q", got)
	}
}

// (c) withoutProjects 剔除多个 id、忽略空 id、不改入参。
func TestChatsWithoutProjects(t *testing.T) {
	input := []Project{
		{ID: "p1", Name: "普通项目1"},
		{ID: "steward", Name: "总管项目"},
		{ID: "chats", Name: "对话项目"},
		{ID: "p2", Name: "普通项目2"},
	}

	got := withoutProjects(input, "", "steward", "chats", "")
	if len(got) != 2 || got[0].ID != "p1" || got[1].ID != "p2" {
		t.Fatalf("应剔除 steward 与 chats 并保留普通项目, got %+v", got)
	}
	if len(input) != 4 || input[1].ID != "steward" || input[2].ID != "chats" {
		t.Fatalf("不应修改入参切片, got %+v", input)
	}
	got[0].Name = "修改副本"
	if input[0].Name != "普通项目1" {
		t.Fatalf("返回的切片不应与入参共享底层数组, got %+v", input)
	}
}

// (d) 命令面板 buildPaletteItems 的项目项里没有对话项目。
func TestChatsPaletteExcludesChatsProject(t *testing.T) {
	testenv.TempHome(t)
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	state := newChatState(fake)
	t.Cleanup(state.close)

	state.mu.Lock()
	state.projects = []Project{
		{ID: "chats-builtin", Name: "对话"},
		{ID: "p1", Name: "普通项目"},
	}
	state.mu.Unlock()

	items := buildPaletteItems(state)
	var seenP1 bool
	for _, it := range items {
		if it.kind == "project" && it.id == "project-chats-builtin" {
			t.Fatalf("命令面板项目列表不应包含对话项目: %+v", it)
		}
		if it.kind == "project" && it.id == "project-p1" {
			seenP1 = true
		}
	}
	if !seenP1 {
		t.Fatal("命令面板项目列表应保留普通项目 p1")
	}
}

// (e) 侧栏项目树不含对话项目，且对话段的 + 点击后在对话项目创建会话。
func TestChatsSidebarSectionAddAndFilter(t *testing.T) {
	testenv.TempHome(t)
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "内置对话"},
			{ID: "p1", Name: "普通项目"},
		},
		map[string][]Thread{
			"chats-builtin": {{ID: "c1", ProjectID: "chats-builtin", Title: "闲聊一号", UpdatedAt: now}},
			"p1":            {{ID: "t1", ProjectID: "p1", Title: "项目会话", UpdatedAt: now}},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool {
		return tt.HasText("对话") && tt.HasText("闲聊一号") && tt.HasText("项目会话")
	})

	if _, ok := tt.Find("收起项目 内置对话"); ok {
		t.Fatal("侧栏项目树不应渲染对话项目")
	}
	if _, ok := tt.Find("展开项目 内置对话"); ok {
		t.Fatal("侧栏项目树不应渲染对话项目")
	}

	if err := tt.Click("新建对话"); err != nil {
		t.Fatalf("点击对话段 + 按钮失败: %v", err)
	}
	waitFor(t, tt, func() bool {
		return fake.CreateThreadCalls() == 1
	})
	if got := fake.LastCreatedProjectID(); got != "chats-builtin" {
		t.Fatalf("点击对话段 + 应在对话项目创建会话, got %q", got)
	}
}

// (f) worktree 快捷键目标是对话项目时不调用 MoveToWorktree / 不建 worktree。
func TestChatsWorktreeShortcutSkipsWorktree(t *testing.T) {
	testenv.TempHome(t)
	withShortcutGOOS(t, "darwin")
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	fake.SetProjectsAndThreads(
		[]Project{{ID: "chats-builtin", Name: "对话"}},
		map[string][]Thread{
			"chats-builtin": {{ID: "c1", ProjectID: "chats-builtin", Title: "对话会话", UpdatedAt: time.Now()}},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.selectedThreadID == "c1"
	})

	tt.Key(ui.Cmd|ui.Alt, ui.KeyN)
	waitFor(t, tt, func() bool {
		return fake.CreateThreadCalls() == 1
	})

	state.mu.Lock()
	newID := state.selectedThreadID
	isWorktree := state.provisionalWorktrees[newID]
	state.mu.Unlock()
	if newID == "" || newID == "c1" {
		t.Fatalf("快捷键应创建并选中新会话, got %q", newID)
	}
	if isWorktree {
		t.Fatal("对话项目不应标记为 provisionalWorktrees")
	}

	// 输入首个字符触发转正，确认不会调用 MoveToWorktree。
	state.noteComposerDraft(newID, "你好")
	waitFor(t, tt, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		th, ok := state.threadSnapshotLocked(newID)
		return ok && !th.IsProvisional
	})
	fake.mu.Lock()
	moveCalls := len(fake.moveToWorktreeCalls)
	fake.mu.Unlock()
	if moveCalls != 0 {
		t.Fatalf("对话项目触发 worktree 快捷键不应调用 MoveToWorktree, got %d", moveCalls)
	}
}

// (g) 选中总管会话时触发 createNewThread，落在第一个普通项目，不落在总管项目。
func TestChatsCreateNewThreadSkipsStewardThread(t *testing.T) {
	testenv.TempHome(t)
	fake := newStewardFakeClient("steward-t")
	fake.SetChatsProjectID("chats-builtin")
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "steward-p", Name: "总管项目"},
			{ID: "chats-builtin", Name: "对话"},
			{ID: "p1", Name: "普通项目"},
		},
		map[string][]Thread{
			"steward-p": {{ID: "steward-t", ProjectID: "steward-p", Title: "总管"}},
			"p1":        {{ID: "t1", ProjectID: "p1", Title: "普通会话"}},
		},
	)
	fake.SetProviders([]ProviderInfo{{ID: "claude", Name: "Claude", Available: true}})

	state := newChatState(fake)
	t.Cleanup(state.close)
	state.mu.Lock()
	state.projects = []Project{
		{ID: "steward-p", Name: "总管项目"},
		{ID: "chats-builtin", Name: "对话"},
		{ID: "p1", Name: "普通项目"},
	}
	state.threads = []Thread{
		{ID: "steward-t", ProjectID: "steward-p", Title: "总管"},
		{ID: "t1", ProjectID: "p1", Title: "普通会话"},
	}
	state.threadsByProj = map[string][]Thread{
		"steward-p": {{ID: "steward-t", ProjectID: "steward-p", Title: "总管"}},
		"p1":        {{ID: "t1", ProjectID: "p1", Title: "普通会话"}},
	}
	state.selectedThreadID = "steward-t"
	state.newThreadProvider = "claude"
	state.mu.Unlock()

	state.createNewThread()

	waitUntil(t, "在第一个普通项目创建会话", func() bool {
		return fake.CreateThreadCalls() == 1
	})
	if got := fake.LastCreatedProjectID(); got != "p1" {
		t.Fatalf("选中总管会话时新建会话应落在第一个普通项目 p1, got %q", got)
	}
}

// (h) 侧栏「对话」段仅在默认树视图且无搜索词时渲染，动态视图与搜索时不显示。
func TestChatsSidebarSectionHiddenInActivityAndSearch(t *testing.T) {
	testenv.TempHome(t)
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "内置对话"},
			{ID: "p1", Name: "普通项目"},
		},
		map[string][]Thread{
			"chats-builtin": {{ID: "c1", ProjectID: "chats-builtin", Title: "闲聊一号", UpdatedAt: now}},
			"p1":            {{ID: "t1", ProjectID: "p1", Title: "项目会话", UpdatedAt: now}},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	// (a) 默认树视图下显示「对话」段。
	waitFor(t, tt, func() bool {
		_, hasNewChat := tt.Find("新建对话")
		return hasNewChat && tt.HasText("对话") && tt.HasText("闲聊一号") && tt.HasText("项目会话")
	})

	// (b) 打开动态视图后，「对话」段不再渲染。
	state.toggleActivityView()
	tt.Frame()
	if _, ok := tt.Find("新建对话"); ok {
		t.Fatal("动态视图下不应渲染「对话」段")
	}

	// 关闭动态视图后恢复显示。
	state.toggleActivityView()
	tt.Frame()
	if _, ok := tt.Find("新建对话"); !ok {
		t.Fatal("关闭动态视图后应恢复渲染「对话」段")
	}

	// (c) 侧栏搜索词非空时，「对话」段不渲染。
	*getSearchQuery(state) = "项目"
	state.invalidate()
	tt.Frame()
	if _, ok := tt.Find("新建对话"); ok {
		t.Fatal("侧栏搜索时不应渲染「对话」段")
	}
	if _, ok := tt.Find("打开会话 项目会话"); !ok {
		t.Fatal("侧栏搜索应保留命中的普通项目会话")
	}

	// 清空搜索词后恢复显示。
	*getSearchQuery(state) = ""
	state.invalidate()
	tt.Frame()
	if _, ok := tt.Find("新建对话"); !ok {
		t.Fatal("清空侧栏搜索词后应恢复渲染「对话」段")
	}
}

// (i) 项目树视图搜索「对话」会话标题时渲染「对话」段且仅列命中会话，对话项目名不参与匹配。
func TestChatsSidebarSearchTreeView(t *testing.T) {
	testenv.TempHome(t)
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "内置对话"},
			{ID: "p1", Name: "普通项目"},
		},
		map[string][]Thread{
			"chats-builtin": {
				{ID: "c1", ProjectID: "chats-builtin", Title: "闲聊一号", UpdatedAt: now},
				{ID: "c2", ProjectID: "chats-builtin", Title: "休眠闲聊", Dormant: true, UpdatedAt: now.Add(-time.Hour)},
				{ID: "c3", ProjectID: "chats-builtin", Title: "普通话题", UpdatedAt: now},
			},
			"p1": {
				{ID: "t1", ProjectID: "p1", Title: "项目会话", UpdatedAt: now},
			},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool {
		return tt.HasText("闲聊一号") && tt.HasText("项目会话")
	})

	*getSearchQuery(state) = "闲聊"
	state.invalidate()
	tt.Frame()

	for _, want := range []string{"新建对话", "打开会话 闲聊一号", "打开会话 休眠闲聊"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("树视图搜索应显示 %q: %q", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"打开会话 普通话题", "打开会话 项目会话", "收起项目 普通项目", "展开项目 普通项目"} {
		if _, ok := tt.Find(unwanted); ok {
			t.Fatalf("树视图搜索不应显示未命中项 %q: %q", unwanted, tt.Texts())
		}
	}

	// 对话项目名不参与匹配。
	*getSearchQuery(state) = "内置对话"
	state.invalidate()
	tt.Frame()
	if !tt.HasText("无结果") {
		t.Fatalf("对话项目名不应参与匹配，期望显示无结果卡片: %q", tt.Texts())
	}
}

// (j) 动态视图搜索能搜到「对话」项目里的活跃会话，并跳过休眠和子代理会话。
func TestChatsSidebarSearchActivityView(t *testing.T) {
	testenv.TempHome(t)
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "内置对话"},
			{ID: "p1", Name: "普通项目"},
		},
		map[string][]Thread{
			"chats-builtin": {
				{ID: "c1", ProjectID: "chats-builtin", Title: "闲聊一号", UpdatedAt: now},
				{ID: "c2", ProjectID: "chats-builtin", Title: "休眠闲聊", Dormant: true, UpdatedAt: now.Add(-time.Hour)},
				{ID: "c3", ProjectID: "chats-builtin", Title: "闲聊子代理", IsHelper: true, ParentThreadID: "c1", UpdatedAt: now},
				{ID: "c4", ProjectID: "chats-builtin", Title: "无关对话", UpdatedAt: now},
			},
			"p1": {
				{ID: "t1", ProjectID: "p1", Title: "项目闲聊", UpdatedAt: now},
				{ID: "t2", ProjectID: "p1", Title: "普通会话", UpdatedAt: now},
			},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool {
		return tt.HasText("闲聊一号") && tt.HasText("项目闲聊")
	})

	state.toggleActivityView()
	*getSearchQuery(state) = "闲聊"
	state.invalidate()
	tt.Frame()

	for _, want := range []string{"打开会话 闲聊一号", "打开会话 项目闲聊"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("动态视图搜索应显示 %q: %q", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"新建对话", "打开会话 休眠闲聊", "打开会话 闲聊子代理", "打开会话 无关对话", "打开会话 普通会话"} {
		if _, ok := tt.Find(unwanted); ok {
			t.Fatalf("动态视图搜索不应显示 %q: %q", unwanted, tt.Texts())
		}
	}
	if tt.HasText("无结果") {
		t.Fatalf("动态视图搜索有命中时不应显示无结果卡片: %q", tt.Texts())
	}
}

// (k) 只有「对话」会话命中时，不显示无结果卡片。
func TestChatsSidebarSearchOnlyChatsNoEmptyCard(t *testing.T) {
	testenv.TempHome(t)
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "内置对话"},
			{ID: "p1", Name: "普通项目"},
		},
		map[string][]Thread{
			"chats-builtin": {
				{ID: "c1", ProjectID: "chats-builtin", Title: "专属闲聊", UpdatedAt: now},
			},
			"p1": {
				{ID: "t1", ProjectID: "p1", Title: "项目会话", UpdatedAt: now},
			},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool {
		return tt.HasText("专属闲聊") && tt.HasText("项目会话")
	})

	*getSearchQuery(state) = "专属"
	state.invalidate()
	tt.Frame()

	if _, ok := tt.Find("打开会话 专属闲聊"); !ok {
		t.Fatalf("仅对话命中时应显示命中会话: %q", tt.Texts())
	}
	if tt.HasText("无结果") || tt.HasText("试试输入会话标题或项目名称") {
		t.Fatalf("仅对话命中时不应显示无结果卡片: %q", tt.Texts())
	}
	if _, ok := tt.Find("打开会话 项目会话"); ok {
		t.Fatalf("未命中的普通项目会话不应显示: %q", tt.Texts())
	}

	state.toggleActivityView()
	tt.Frame()
	if _, ok := tt.Find("打开会话 专属闲聊"); !ok {
		t.Fatalf("动态视图下仅对话命中时应显示命中会话: %q", tt.Texts())
	}
	if tt.HasText("无结果") {
		t.Fatalf("动态视图下仅对话命中时不应显示无结果卡片: %q", tt.Texts())
	}
}

func TestChatsMoveToProjectMenu(t *testing.T) {
	testenv.TempHome(t)
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "对话"},
			{ID: "p1", Name: "普通项目"},
		},
		map[string][]Thread{
			"chats-builtin": {{ID: "c1", ProjectID: "chats-builtin", Title: "对话会话", UpdatedAt: now}},
			"p1":            {{ID: "t1", ProjectID: "p1", Title: "普通会话", UpdatedAt: now}},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool {
		return tt.HasText("对话会话") && tt.HasText("普通会话")
	})

	// 1. 普通会话右键菜单：有「迁入 Worktree…」，没有「移到项目…」。
	if err := tt.RightClick("普通会话"); err != nil {
		t.Fatalf("RightClick 普通会话失败: %v", err)
	}
	tt.Frame()
	normalMenu := tt.Menu()
	var hasWorktree, hasMoveToProject bool
	for _, item := range normalMenu {
		if item == "迁入 Worktree…" {
			hasWorktree = true
		}
		if item == "移到项目…" {
			hasMoveToProject = true
		}
	}
	if !hasWorktree || hasMoveToProject {
		t.Fatalf("普通会话菜单应含「迁入 Worktree…」且不含「移到项目…」，实际: %v", normalMenu)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// 2. 对话会话右键菜单：有「移到项目…」，没有「迁入 Worktree…」。
	if err := tt.RightClick("对话会话"); err != nil {
		t.Fatalf("RightClick 对话会话失败: %v", err)
	}
	tt.Frame()
	chatsMenu := tt.Menu()
	hasWorktree, hasMoveToProject = false, false
	for _, item := range chatsMenu {
		if item == "迁入 Worktree…" {
			hasWorktree = true
		}
		if item == "移到项目…" {
			hasMoveToProject = true
		}
	}
	if !hasMoveToProject || hasWorktree {
		t.Fatalf("对话会话菜单应含「移到项目…」且不含「迁入 Worktree…」，实际: %v", chatsMenu)
	}

	// 3. 点击「移到项目…」打开弹窗，选择「普通项目」后移动并保持选中。
	if err := tt.ChooseMenuItem("移到项目…"); err != nil {
		t.Fatalf("ChooseMenuItem 移到项目… 失败: %v", err)
	}
	tt.Frame()
	if _, ok := tt.Find("modal-move-to-project-card"); !ok {
		t.Fatal("点击「移到项目…」后应打开项目选择弹窗")
	}
	if err := tt.Click("普通项目"); err != nil {
		t.Fatalf("点击目标项目失败: %v", err)
	}
	waitFor(t, tt, func() bool {
		fake.mu.Lock()
		calls := len(fake.moveThreadToProjectCalls)
		fake.mu.Unlock()
		state.mu.Lock()
		inTarget := false
		for _, th := range state.threadsByProj["p1"] {
			if th.ID == "c1" {
				inTarget = true
				break
			}
		}
		inChats := len(state.threadsByProj["chats-builtin"])
		state.mu.Unlock()
		return calls == 1 && inTarget && inChats == 0
	})
	state.mu.Lock()
	selected := state.selectedThreadID
	state.mu.Unlock()
	if selected != "c1" {
		t.Fatalf("移动后应保持选中该会话 c1, got %q", selected)
	}
}
