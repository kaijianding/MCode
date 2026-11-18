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
