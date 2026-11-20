package mcodeui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"mcode/internal/config"
)

func assertInsideBounds(t *testing.T, name string, inner, outer ui.Rect) {
	t.Helper()
	const eps float32 = 0.5
	if inner.X < outer.X-eps || inner.Y < outer.Y-eps ||
		inner.X+inner.W > outer.X+outer.W+eps ||
		inner.Y+inner.H > outer.Y+outer.H+eps {
		t.Errorf("%s 超出容器边界: inner=%+v, outer=%+v", name, inner, outer)
	}
}

func assertNoHorizOverlap(t *testing.T, leftName string, left ui.Rect, rightName string, right ui.Rect) {
	t.Helper()
	const eps float32 = 0.5
	if left.X+left.W > right.X+eps {
		t.Errorf("%s 与 %s 横向重叠/越界: left=%+v, right=%+v", leftName, rightName, left, right)
	}
}

// TestUIReview27SwarmSummaryPanelNarrow 验证 (a) 汇总面板在 360 宽主区里不溢出且行尾按钮留在面板内。
func TestUIReview27SwarmSummaryPanelNarrow(t *testing.T) {
	fake := NewFakeClient()
	s := newChatState(fake)
	t.Cleanup(s.close)

	const leadID = "lead-review27-summary"
	s.selectedThreadID = leadID
	longName := strings.Repeat("LongAgentName", 3)
	bees := []SwarmBee{
		{
			ThreadID:     "bee-review",
			Index:        1,
			Name:         longName + "-A",
			Task:         "验证窄面板徽章与审查按钮换行",
			Status:       "completed",
			Provider:     "anthropic",
			Model:        "claude-sonnet-4-5-20250929",
			DirectModel:  true,
			Tokens:       12345,
			ToolCalls:    8,
			PendingMerge: true,
			Merged:       false,
			Credibility: &SwarmCredibility{
				Cited:    4,
				Verified: 3,
				Summary:  "存在未核实引用",
			},
		},
		{
			ThreadID:  "bee-running",
			Index:     2,
			Name:      longName + "-B",
			Task:      "验证窄面板徽章与停止按钮换行",
			Status:    "running",
			Provider:  "openai",
			Model:     "gpt-5.4-codex",
			Tokens:    9876,
			ToolCalls: 12,
		},
		{
			ThreadID:  "bee-merged",
			Index:     3,
			Name:      longName + "-C",
			Task:      "验证窄面板徽章与查看改动按钮换行",
			Status:    "completed",
			Provider:  "openai",
			Model:     "gpt-5.4-codex",
			Tokens:    4321,
			ToolCalls: 5,
			Merged:    true,
		},
	}

	hp := getSwarmPanelState(s)
	swarmPanelMu.Lock()
	hp.open = true
	hp.loaded = true
	hp.loadedFor = leadID
	hp.setStateLocked(SwarmState{
		Available:    true,
		Enabled:      true,
		LeadThreadID: leadID,
		Bees:         bees,
	})
	swarmPanelMu.Unlock()

	const winW float32 = 360
	const winH float32 = 640
	tt := ui.NewTesterWith(func(c *ui.Context) {
		renderSwarmPanel(c, s)
	}, int(winW), int(winH), ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
	tt.Frame()

	winBounds := ui.Rect{X: 0, Y: 0, W: winW, H: winH}
	for _, label := range []string{"关闭汇总面板", "在独立窗口中打开", "只规划", "关闭", "停止全部", "清理已结束", "审查", "查看改动", "停止"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("未找到 %q，texts=%v", label, tt.Texts())
		}
		assertInsideBounds(t, label, r, winBounds)
	}
}

// TestUIReview27SwarmMenuLongTask 验证 (b) 子代理菜单在 100 字符任务名下，任务文本和行尾按钮留在菜单内。
func TestUIReview27SwarmMenuLongTask(t *testing.T) {
	fake := NewFakeClient()
	s := newChatState(fake)
	const leadID = "lead-review27-menu"
	s.selectedThreadID = leadID

	longTask := strings.Repeat("abcdefghij", 10) // 100 字符连续无空格任务
	bees := []SwarmBee{
		{
			ThreadID: "bee-selected",
			Index:    1,
			Name:     "Aspen",
			Tier:     "fast",
			Task:     "当前展示的子代理",
			Status:   "completed",
		},
		{
			ThreadID: "bee-long-task",
			Index:    2,
			Name:     "Birch",
			Tier:     "smart",
			Task:     longTask,
			Status:   "running",
		},
	}
	hp := getSwarmPanelState(s)
	swarmPanelMu.Lock()
	hp.loadedFor = leadID
	hp.setStateLocked(SwarmState{
		Available:    true,
		Enabled:      true,
		LeadThreadID: leadID,
		Bees:         bees,
	})
	swarmPanelMu.Unlock()

	reset := func() {
		swarmFloatMu.Lock()
		delete(swarmFloatPanelDismissed, leadID)
		delete(swarmFloatDismissedBees, leadID)
		delete(swarmBeeDoneSince, leadID)
		delete(swarmBeeAutoHideHandled, leadID)
		delete(swarmBeeSeenActive, leadID)
		delete(swarmBeeKeptOpen, leadID)
		delete(swarmMenuOpen, leadID+"|"+string(teamPanelID))
		delete(swarmSelectedBeeID, leadID)
		swarmFloatMu.Unlock()
	}
	reset()
	swarmFloatMu.Lock()
	swarmBeeSeenActive[leadID] = map[string]bool{"bee-selected": true, "bee-long-task": true}
	swarmSelectedBeeID[leadID] = "bee-selected"
	swarmFloatMu.Unlock()
	t.Cleanup(func() {
		closeEmbeddedBees(s)
		reset()
		s.close()
	})

	tt := ui.NewTesterWith(func(c *ui.Context) {
		renderSwarmBeeFloats(c, s)
	}, 900, 700, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
	tt.Frame()

	if err := tt.Click("子代理列表，共 2 个"); err != nil {
		t.Fatalf("打开子代理菜单失败: %v", err)
	}
	tt.Frame()

	menuKey := "swarm-bees-menu-" + leadID + "|" + string(teamPanelID)
	menuRect, ok := tt.Find(menuKey)
	if !ok {
		t.Fatalf("未找到菜单面板 %q", menuKey)
	}
	if menuRect.W > 321 {
		t.Errorf("菜单宽度 = %v，超出 maxWidth=320", menuRect.W)
	}
	stopRect, ok := tt.Find("停止 Birch")
	if !ok {
		t.Fatalf("菜单内未找到按钮 %q", "停止 Birch")
	}
	assertInsideBounds(t, "停止 Birch", stopRect, menuRect)
	popRect, ok := tt.Find("在独立窗口打开 Birch")
	if !ok {
		t.Fatalf("菜单内未找到按钮 %q", "在独立窗口打开 Birch")
	}
	assertInsideBounds(t, "在独立窗口打开 Birch", popRect, menuRect)
	assertNoHorizOverlap(t, "停止 Birch", stopRect, "在独立窗口打开 Birch", popRect)
	if taskRect, ok := tt.Find(longTask); ok {
		assertInsideBounds(t, "bee.Task", taskRect, menuRect)
		assertNoHorizOverlap(t, "bee.Task", taskRect, "停止 Birch", stopRect)
	}
}

// TestUIReview27SwarmFloatStripAndBeeWindowNarrow 验证 (c) 浮窗顶栏与独立窗口在 420 宽、长 bee 名下不溢出。
func TestUIReview27SwarmFloatStripAndBeeWindowNarrow(t *testing.T) {
	longBeeName := strings.Repeat("VeryLongBeeAgentName", 3) // 60 字符

	t.Run("浮窗顶栏", func(t *testing.T) {
		fake := NewFakeClient()
		s := newChatState(fake)
		const leadID = "lead-review27-strip"
		s.selectedThreadID = leadID

		bees := []SwarmBee{
			{
				ThreadID:  "bee-strip",
				Index:     1,
				Name:      longBeeName,
				Tier:      "smart",
				Status:    "running",
				StartedAt: time.Now().Add(-15 * time.Second),
			},
		}
		hp := getSwarmPanelState(s)
		swarmPanelMu.Lock()
		hp.loadedFor = leadID
		hp.setStateLocked(SwarmState{
			Available:    true,
			Enabled:      true,
			LeadThreadID: leadID,
			Bees:         bees,
		})
		swarmPanelMu.Unlock()

		reset := func() {
			swarmFloatMu.Lock()
			delete(swarmFloatPanelDismissed, leadID)
			delete(swarmFloatDismissedBees, leadID)
			delete(swarmBeeDoneSince, leadID)
			delete(swarmBeeAutoHideHandled, leadID)
			delete(swarmBeeSeenActive, leadID)
			delete(swarmBeeKeptOpen, leadID)
			delete(swarmMenuOpen, leadID+"|"+string(teamPanelID))
			delete(swarmSelectedBeeID, leadID)
			swarmFloatMu.Unlock()
		}
		reset()
		swarmFloatMu.Lock()
		swarmBeeSeenActive[leadID] = map[string]bool{"bee-strip": true}
		swarmFloatMu.Unlock()
		t.Cleanup(func() {
			closeEmbeddedBees(s)
			reset()
			s.close()
		})

		const winW float32 = 420
		const winH float32 = 600
		tt := ui.NewTesterWith(func(c *ui.Context) {
			renderSwarmBeeFloats(c, s)
		}, int(winW), int(winH), ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
		tt.Frame()

		winBounds := ui.Rect{X: 0, Y: 0, W: winW, H: winH}
		handleRect, ok := tt.Find(longBeeName + " 面板，可拖动")
		if !ok {
			t.Fatalf("未找到顶栏拖拽句柄")
		}
		popRect, ok := tt.Find("在独立窗口打开 " + longBeeName)
		if !ok {
			t.Fatalf("未找到独立窗口按钮")
		}
		closeRect, ok := tt.Find("关闭面板")
		if !ok {
			t.Fatalf("未找到关闭面板按钮")
		}
		countRect, ok := tt.Find("子代理列表，共 1 个")
		if !ok {
			t.Fatalf("未找到子代理计数按钮")
		}
		assertInsideBounds(t, "countBtn", countRect, winBounds)
		assertInsideBounds(t, "handle", handleRect, winBounds)
		assertInsideBounds(t, "pop", popRect, winBounds)
		assertInsideBounds(t, "closeBtn", closeRect, winBounds)
		assertNoHorizOverlap(t, "countBtn", countRect, "handle", handleRect)
		assertNoHorizOverlap(t, "handle", handleRect, "pop", popRect)
		assertNoHorizOverlap(t, "pop", popRect, "closeBtn", closeRect)
	})

	t.Run("独立窗口", func(t *testing.T) {
		fake := NewFakeClient()
		parent := newChatState(fake)
		const leadID = "lead-review27-beewin"
		parent.selectedThreadID = leadID

		bees := []SwarmBee{
			{
				ThreadID:  "bee-win",
				Index:     1,
				Name:      longBeeName,
				Status:    "running",
				StartedAt: time.Now().Add(-42 * time.Second),
			},
		}
		hp := getSwarmPanelState(parent)
		swarmPanelMu.Lock()
		hp.loadedFor = leadID
		hp.setStateLocked(SwarmState{
			Available:    true,
			Enabled:      true,
			LeadThreadID: leadID,
			Bees:         bees,
		})
		swarmPanelMu.Unlock()

		child := newEmbeddedBeeState(parent)
		t.Cleanup(func() {
			releaseEmbeddedBeeState(child)
			parent.close()
		})

		const winW float32 = beeWindowMinWidth
		const winH float32 = beeWindowMinHeight
		tt := ui.NewTesterWith(func(c *ui.Context) {
			renderBeeWindow(c, parent, "bee-win", child)
		}, int(winW), int(winH), ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
		tt.Frame()

		winBounds := ui.Rect{X: 0, Y: 0, W: winW, H: winH}
		if nameRect, ok := tt.Find(longBeeName); ok {
			assertInsideBounds(t, "beeWindow.Name", nameRect, winBounds)
		}
		if statusRect, ok := tt.Find("正在工作"); ok {
			assertInsideBounds(t, "beeWindow.Status", statusRect, winBounds)
		} else {
			t.Fatalf("未找到状态文本，texts=%v", tt.Texts())
		}
		if durRect, ok := tt.Find("42秒"); ok {
			assertInsideBounds(t, "beeWindow.Duration", durRect, winBounds)
		} else {
			t.Fatalf("未找到时长文本，texts=%v", tt.Texts())
		}
	})
}

// TestUIReview27KanbanListRowLongTitle 验证 (d) 840 宽任务窗口列表视图中，100 字符任务标题不把行尾信息挤出任务行。
func TestUIReview27KanbanListRowLongTitle(t *testing.T) {
	s, view := buildTasksRoot(nil, WithTasksWindowMode("tasks"))
	t.Cleanup(s.close)

	s.projects = []Project{{ID: "p1", Name: "示例项目名称", Path: "/project"}}
	longTitle := strings.Repeat("task_title_100_", 7)[:100]

	ks := getKanbanState(s)
	kanbanMu.Lock()
	ks.initialized = true
	ks.scopeID = ""
	ks.viewMode = config.KanbanViewList
	ks.board = KanbanBoard{
		Tasks: []KanbanTask{
			{
				ID:        "task-long-1",
				ProjectID: "p1",
				Title:     longTitle,
				Status:    "waiting",
				Column:    "in_progress",
				WaitKind:  "children",
				Priority:  "high",
			},
		},
	}
	kanbanMu.Unlock()

	const winW float32 = 840
	const winH float32 = 520
	tt := ui.NewTesterWith(view, int(winW), int(winH), ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
	tt.Frame()

	rowRect, ok := tt.Find("kanban-list-row-task-long-1")
	if !ok {
		t.Fatalf("未找到列表任务行，texts=%v", tt.Texts())
	}
	winBounds := ui.Rect{X: 0, Y: 0, W: winW, H: winH}
	assertInsideBounds(t, "kanban-list-row", rowRect, winBounds)

	sigRect, ok := tt.Find("等待子任务")
	if !ok {
		t.Fatalf("未找到行尾信号，texts=%v", tt.Texts())
	}
	assertInsideBounds(t, "listRow[等待子任务]", sigRect, rowRect)
	assertInsideBounds(t, "listRowWin[等待子任务]", sigRect, winBounds)

	if titleRect, ok := tt.Find(longTitle); ok {
		assertInsideBounds(t, "listRow.Title", titleRect, rowRect)
	}

	// 同宽度下单独渲染列表行，核对项目名称与信号同时不越界。
	rowTT := ui.NewTesterWith(func(c *ui.Context) {
		renderKanbanListRow(c, s, ks.board.Tasks[0])
	}, int(rowRect.W), 80, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
	rowTT.Frame()
	standaloneRow, ok := rowTT.Find("kanban-list-row-task-long-1")
	if !ok {
		t.Fatal("未找到单独渲染的列表任务行")
	}
	for _, label := range []string{"等待子任务", "示例项目名称"} {
		r, ok := rowTT.Find(label)
		if !ok {
			t.Fatalf("单独渲染列表行未找到 %q，texts=%v", label, rowTT.Texts())
		}
		assertInsideBounds(t, fmt.Sprintf("standaloneRow[%s]", label), r, standaloneRow)
	}
}
