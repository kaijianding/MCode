package mcodeui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func rectContains(outer, inner ui.Rect) bool {
	return inner.W > 0 && inner.H > 0 &&
		inner.X >= outer.X &&
		inner.Y >= outer.Y &&
		inner.X+inner.W <= outer.X+outer.W &&
		inner.Y+inner.H <= outer.Y+outer.H
}

func TestUIReview31_MemorySimilarLoadingEmptyPath(t *testing.T) {
	fake := NewFakeClient()
	s := newChatState(fake)
	t.Cleanup(s.close)

	st := getMemoryPanelState(s)
	memoryPanelMu.Lock()
	st.open = true
	st.projectPath = ""
	st.proposals = []MemoryProposal{{
		ID:       "mp-empty-path",
		Title:    "无路径提议",
		Content:  "内容",
		UnitType: "fact",
		Source:   "agent",
		Status:   "pending",
	}}
	memoryPanelMu.Unlock()

	tt := ui.NewTester(func(c *ui.Context) { renderMemoryModal(c, s) }, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("无路径提议") })

	if err := tt.Click("查看 / 编辑"); err != nil {
		t.Fatalf("click 查看 / 编辑 failed: %v", err)
	}
	waitFor(t, tt, func() bool { return tt.HasText("没有找到相似记忆") })

	memoryPanelMu.Lock()
	loading := st.similarLoading
	memoryPanelMu.Unlock()
	if loading {
		t.Fatal("项目路径为空时 similarLoading 不应卡在 true")
	}
	if tt.HasText("正在检索相似记忆…") {
		t.Fatal("项目路径为空时界面不应显示加载指示")
	}
}

func TestUIReview31_CreateDialogErrorsInsideCard(t *testing.T) {
	t.Run("context", func(t *testing.T) {
		fake := NewFakeClient()
		s, view := buildRoot(fake)
		t.Cleanup(s.close)

		tt := ui.NewTester(view, 1000, 700)
		waitFor(t, tt, func() bool { return tt.HasText("测试会话1") })

		tt.Key(ui.Cmd|ui.Shift, ui.KeyK)
		waitFor(t, tt, func() bool {
			_, ok := tt.Find("context-modal-card")
			return ok
		})

		if err := tt.Click("新建自定义文档"); err != nil {
			t.Fatalf("click 新建自定义文档 failed: %v", err)
		}
		waitFor(t, tt, func() bool {
			_, ok := tt.Find("context-create-card")
			return ok
		})

		if err := tt.Click("创建"); err != nil {
			t.Fatalf("click 创建 failed: %v", err)
		}
		waitFor(t, tt, func() bool { return tt.HasText("文档名称不能为空") })

		cardBox, ok := tt.Find("context-create-card")
		if !ok {
			t.Fatal("未找到 context-create-card")
		}
		errBox, ok := tt.Find("文档名称不能为空")
		if !ok {
			t.Fatal("未找到错误提示")
		}
		if !rectContains(cardBox, errBox) {
			t.Fatalf("新建文档错误提示 %+v 不在弹窗卡片 %+v 内", errBox, cardBox)
		}

		if err := tt.Click("取消"); err != nil {
			t.Fatalf("click 取消 failed: %v", err)
		}
		waitFor(t, tt, func() bool {
			_, ok := tt.Find("context-create-card")
			return !ok
		})

		if err := tt.Click("新建自定义文档"); err != nil {
			t.Fatalf("reopen 新建自定义文档 failed: %v", err)
		}
		waitFor(t, tt, func() bool {
			_, ok := tt.Find("context-create-card")
			return ok
		})
		if tt.HasText("文档名称不能为空") {
			t.Fatal("重新打开新建文档弹窗后应清空旧错误")
		}
	})

	t.Run("memory", func(t *testing.T) {
		fake := NewFakeClient()
		fake.memoryCreateErr = errors.New("创建记忆失败")
		s, view := buildRoot(fake)
		t.Cleanup(s.close)

		tt, _ := openMemoryPanel(t, fake, s, view)
		waitFor(t, tt, func() bool { return tt.HasText("新建记忆") })

		if err := tt.Click("新建记忆"); err != nil {
			t.Fatalf("click 新建记忆 failed: %v", err)
		}
		waitFor(t, tt, func() bool {
			_, ok := tt.Find("memory-create-card")
			return ok
		})

		if err := tt.Click("标题"); err != nil {
			t.Fatalf("focus 标题 failed: %v", err)
		}
		tt.Type("新记忆标题")
		if err := tt.Click("加入待审"); err != nil {
			t.Fatalf("click 加入待审 failed: %v", err)
		}
		waitFor(t, tt, func() bool { return tt.HasText("创建记忆失败") })

		cardBox, ok := tt.Find("memory-create-card")
		if !ok {
			t.Fatal("未找到 memory-create-card")
		}
		errBox, ok := tt.Find("创建记忆失败")
		if !ok {
			t.Fatal("未找到错误提示")
		}
		if !rectContains(cardBox, errBox) {
			t.Fatalf("新建记忆错误提示 %+v 不在弹窗卡片 %+v 内", errBox, cardBox)
		}

		if err := tt.Click("取消"); err != nil {
			t.Fatalf("click 取消 failed: %v", err)
		}
		waitFor(t, tt, func() bool {
			_, ok := tt.Find("memory-create-card")
			return !ok
		})

		if err := tt.Click("新建记忆"); err != nil {
			t.Fatalf("reopen 新建记忆 failed: %v", err)
		}
		waitFor(t, tt, func() bool {
			_, ok := tt.Find("memory-create-card")
			return ok
		})
		if tt.HasText("创建记忆失败") {
			t.Fatal("重新打开新建记忆弹窗后应清空旧错误")
		}
	})
}

func TestUIReview31_UpdatePopoverShowsErrMsg(t *testing.T) {
	s := newChatState(NewFakeClient())
	t.Cleanup(s.close)

	globalAboutState.mu.Lock()
	origInstalling := globalAboutState.installing
	origProgress := globalAboutState.progress
	origErrMsg := globalAboutState.errMsg
	globalAboutState.installing = false
	globalAboutState.progress = 0
	globalAboutState.errMsg = "安装失败：签名校验未通过"
	globalAboutState.mu.Unlock()
	t.Cleanup(func() {
		globalAboutState.mu.Lock()
		globalAboutState.installing = origInstalling
		globalAboutState.progress = origProgress
		globalAboutState.errMsg = origErrMsg
		globalAboutState.mu.Unlock()
	})

	status := AppUpdateStatus{
		Latest:      "v2.0.0",
		Installable: true,
		URL:         "https://example.com",
	}
	tt := ui.NewTester(func(c *ui.Context) {
		renderUpdatePopover(c, s, nil, "MCode v2.0.0", "更新内容", &status)
	}, 500, 500)

	if !tt.HasText("安装失败：签名校验未通过") {
		t.Fatal("更新浮层未显示 globalAboutState.errMsg")
	}
}

type blockingReview31Client struct {
	*FakeClient
	historyOnce    sync.Once
	historyStarted chan struct{}
}

func (c *blockingReview31Client) History(ctx context.Context, id string) ([]Message, error) {
	c.historyOnce.Do(func() { close(c.historyStarted) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *blockingReview31Client) Projects(ctx context.Context) ([]Project, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestUIReview31_BlockingClientTimeoutResetsState(t *testing.T) {
	t.Run("exportThread", func(t *testing.T) {
		orig := threadExportHistoryTimeout
		threadExportHistoryTimeout = 20 * time.Millisecond
		t.Cleanup(func() { threadExportHistoryTimeout = orig })

		client := &blockingReview31Client{
			FakeClient:     NewFakeClient(),
			historyStarted: make(chan struct{}),
		}
		s := newChatState(client)
		t.Cleanup(s.close)

		s.exportThread("t1")
		<-client.historyStarted
		if got := recallTestToast(s); got != "正在导出…" {
			t.Fatalf("初始 toast = %q, want 正在导出…", got)
		}

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if got := recallTestToast(s); strings.HasPrefix(got, "导出失败: ") {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("超时后导出状态未复位，toast = %q", recallTestToast(s))
	})

	t.Run("copyThreadMarkdown", func(t *testing.T) {
		orig := threadExportHistoryTimeout
		threadExportHistoryTimeout = 20 * time.Millisecond
		t.Cleanup(func() { threadExportHistoryTimeout = orig })

		client := &blockingReview31Client{
			FakeClient:     NewFakeClient(),
			historyStarted: make(chan struct{}),
		}
		s := newChatState(client)
		t.Cleanup(s.close)

		var triggered bool
		ui.NewTester(func(c *ui.Context) {
			if !triggered {
				triggered = true
				s.copyThreadMarkdown(c, "t2")
			}
		}, 400, 300)

		<-client.historyStarted
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if got := recallTestToast(s); strings.HasPrefix(got, "复制失败：") {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("超时后复制状态未复位，toast = %q", recallTestToast(s))
	})

	t.Run("tasksWindowProjects", func(t *testing.T) {
		orig := settingsRPCTimeout
		settingsRPCTimeout = 20 * time.Millisecond
		t.Cleanup(func() { settingsRPCTimeout = orig })

		client := &blockingReview31Client{
			FakeClient:     NewFakeClient(),
			historyStarted: make(chan struct{}),
		}
		s, view := buildTasksRoot(client)
		t.Cleanup(s.close)

		tt := ui.NewTester(view, 800, 600)
		waitFor(t, tt, func() bool {
			if strings.HasPrefix(recallTestToast(s), "获取项目失败: ") {
				return true
			}
			for _, txt := range tt.Texts() {
				if strings.HasPrefix(txt, "获取项目失败: ") {
					return true
				}
			}
			for _, ann := range tt.Announcements() {
				if strings.HasPrefix(ann, "获取项目失败: ") {
					return true
				}
			}
			return false
		})
	})
}
