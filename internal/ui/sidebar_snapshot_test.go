package mcodeui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mcode/internal/testenv"
)

func waitSidebarSnapshotSettled(t *testing.T, tt *ui.Tester, state *chatState, threadCount int, ready func() bool) {
	t.Helper()
	wt := getAIWorktreeState(state)
	waitFor(t, tt, func() bool {
		state.mu.Lock()
		pathsReady := state.projectPathsMissing != nil
		state.mu.Unlock()
		wt.mu.Lock()
		wtReady := len(wt.found) >= threadCount && len(wt.pending) == 0 && wt.active == 0
		wt.mu.Unlock()
		return pathsReady && wtReady && ready()
	})
	tt.Frame()
}

func TestUIReview6SidebarSnapshotCachedAcrossFrames(t *testing.T) {
	testenv.TempHome(t)
	resetSidebarHoverCardForTest()
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "对话"},
			{ID: "p1", Name: "项目一"},
		},
		map[string][]Thread{
			"chats-builtin": {{ID: "c1", ProjectID: "chats-builtin", Title: "闲聊一", UpdatedAt: now}},
			"p1": {
				{ID: "t1", ProjectID: "p1", Title: "会话一", UpdatedAt: now},
				{ID: "t2", ProjectID: "p1", Title: "会话二", UpdatedAt: now.Add(-time.Minute)},
			},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitSidebarSnapshotSettled(t, tt, state, 3, func() bool {
		return tt.HasText("闲聊一") && tt.HasText("会话一") && tt.HasText("会话二")
	})

	before := sidebarSnapshotBuilds.Load()

	// 不调用 invalidate 连续推进多帧（悬停、移动指针、推进时间），快照计数必须保持不变。
	row1, ok := tt.Find("打开会话 会话一")
	if !ok {
		t.Fatalf("找不到会话一: %q", tt.Texts())
	}
	tt.Move(row1.X+row1.W/2, row1.Y+row1.H/2)
	tt.Frame()

	row2, ok := tt.Find("打开会话 会话二")
	if !ok {
		t.Fatalf("找不到会话二: %q", tt.Texts())
	}
	tt.Move(row2.X+row2.W/2, row2.Y+row2.H/2)
	tt.Frame()

	tt.Advance(20 * time.Millisecond)
	tt.Frame()

	if got := sidebarSnapshotBuilds.Load(); got != before {
		t.Fatalf("未修改数据连续推进帧时快照不应重建: before=%d, got=%d", before, got)
	}

	// 通过真实业务写入口置顶「会话二」，快照应重建且顺序更新到侧栏。
	if err := tt.RightClick("会话二"); err != nil {
		t.Fatalf("右键会话二失败: %v", err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("置顶"); err != nil {
		t.Fatalf("选择置顶失败: %v", err)
	}

	waitFor(t, tt, func() bool {
		r2, ok2 := tt.Find("打开会话 会话二")
		r1, ok1 := tt.Find("打开会话 会话一")
		return ok1 && ok2 && r2.Y < r1.Y
	})

	afterPin := sidebarSnapshotBuilds.Load()
	if afterPin <= before {
		t.Fatalf("置顶会话后快照应重建: before=%d, afterPin=%d", before, afterPin)
	}

	// 再通过真实业务写入口重命名「会话一」，快照应再次重建且新标题出现在侧栏。
	if err := tt.RightClick("会话一"); err != nil {
		t.Fatalf("右键会话一失败: %v", err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("重命名…"); err != nil {
		t.Fatalf("选择重命名失败: %v", err)
	}
	tt.Frame()
	state.mu.Lock()
	state.renameThreadTitle = "重命名后的会话"
	state.mu.Unlock()
	state.submitRename()

	waitFor(t, tt, func() bool {
		return tt.HasText("重命名后的会话")
	})

	afterRename := sidebarSnapshotBuilds.Load()
	if afterRename <= afterPin {
		t.Fatalf("重命名会话后快照应重建: afterPin=%d, afterRename=%d", afterPin, afterRename)
	}

	// 切换动态视图后也应复用快照，悬停不重复重建。
	state.toggleActivityView()
	tt.Frame()
	afterActivity := sidebarSnapshotBuilds.Load()
	if afterActivity <= afterRename {
		t.Fatalf("切换动态视图后快照应重建: afterRename=%d, afterActivity=%d", afterRename, afterActivity)
	}
	if renamedRow, ok := tt.Find("打开会话 重命名后的会话"); ok {
		tt.Move(renamedRow.X+renamedRow.W/2, renamedRow.Y+renamedRow.H/2)
		tt.Frame()
	}
	if got := sidebarSnapshotBuilds.Load(); got != afterActivity {
		t.Fatalf("动态视图悬停帧快照不应重建: afterActivity=%d, got=%d", afterActivity, got)
	}
}

func TestUIReview6SidebarSnapshotFilterChange(t *testing.T) {
	testenv.TempHome(t)
	resetSidebarHoverCardForTest()
	fake := NewFakeClient()
	fake.SetChatsProjectID("chats-builtin")
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{
			{ID: "chats-builtin", Name: "对话"},
			{ID: "p1", Name: "项目一"},
		},
		map[string][]Thread{
			"chats-builtin": {
				{ID: "c-run", ProjectID: "chats-builtin", Title: "对话运行中", IsRunning: true, UpdatedAt: now},
				{ID: "c-idle", ProjectID: "chats-builtin", Title: "对话空闲", UpdatedAt: now},
			},
			"p1": {
				{ID: "t-run", ProjectID: "p1", Title: "项目运行中", IsRunning: true, UpdatedAt: now},
				{ID: "t-idle", ProjectID: "p1", Title: "项目空闲", UpdatedAt: now},
			},
		},
	)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitSidebarSnapshotSettled(t, tt, state, 4, func() bool {
		return tt.HasText("对话运行中") && tt.HasText("对话空闲") && tt.HasText("项目运行中") && tt.HasText("项目空闲")
	})

	before := sidebarSnapshotBuilds.Load()

	// 走现有过滤切换入口切到「运行中」。
	if err := tt.Click("过滤对话"); err != nil {
		t.Fatalf("打开过滤下拉框失败: %v", err)
	}
	tt.Frame()
	if err := tt.Click("过滤对话：运行中"); err != nil {
		t.Fatalf("选择运行中过滤失败: %v", err)
	}
	tt.Frame()

	afterFilter := sidebarSnapshotBuilds.Load()
	if afterFilter <= before {
		t.Fatalf("切换过滤条件后快照应重建: before=%d, afterFilter=%d", before, afterFilter)
	}
	for _, want := range []string{"打开会话 对话运行中", "打开会话 项目运行中"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("过滤运行中后应显示 %q: %q", want, tt.Texts())
		}
	}
	for _, hidden := range []string{"打开会话 对话空闲", "打开会话 项目空闲"} {
		if _, ok := tt.Find(hidden); ok {
			t.Fatalf("过滤运行中后不应显示 %q: %q", hidden, tt.Texts())
		}
	}

	// 过滤后继续推进帧（无数据变更）不应重建快照。
	tt.Move(900, 600)
	tt.Frame()
	if got := sidebarSnapshotBuilds.Load(); got != afterFilter {
		t.Fatalf("过滤后空闲推进帧快照不应重建: afterFilter=%d, got=%d", afterFilter, got)
	}
}
