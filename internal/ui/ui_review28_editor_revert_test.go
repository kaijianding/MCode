package mcodeui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type hangingRevertPreviewClient struct {
	*FakeClient
}

func (h *hangingRevertPreviewClient) PreviewRevert(ctx context.Context, threadID, turnID string) (*RevertPreview, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestUIReview28MiniEditorConcurrentSaveAndRender 验证迷你编辑器渲染与后台保存并发执行时无数据竞争。
func TestUIReview28MiniEditorConcurrentSaveAndRender(t *testing.T) {
	oldDelay := miniEditorSaveDelay
	miniEditorSaveDelay = time.Millisecond
	t.Cleanup(func() { miniEditorSaveDelay = oldDelay })

	path := filepath.Join(t.TempDir(), "concurrent.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newMiniEditorState(path)
	enabled := true
	st.workbench.EditorAutoSave = &enabled

	tt := ui.NewTesterWith(func(c *ui.Context) {
		applyAppTheme(c)
		st.view(c)
	}, MiniEditorDefaultWidth, MiniEditorDefaultHeight, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})

	// 焦点落入编辑区，后续输入会通过 TextArea 写回文本。
	tt.ClickAt(200, 140)
	tt.Frame()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				st.saveNow()
				time.Sleep(time.Millisecond)
			}
		}
	}()

	for i := 0; i < 20; i++ {
		tt.Type("x")
		tt.Frame()
		time.Sleep(time.Millisecond)
	}

	close(stop)
	wg.Wait()

	st.mu.Lock()
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.mu.Unlock()
	st.saveNow()
}

// TestUIReview28MiniEditorOnClosePreventsOnSaveFailure 验证关窗前同步保存失败时取消关闭并显示错误条，成功时正常关闭。
func TestUIReview28MiniEditorOnClosePreventsOnSaveFailure(t *testing.T) {
	dir := t.TempDir()

	// 场景一：磁盘文件被外部改动，关闭时应拦截并展示错误条。
	conflictPath := filepath.Join(dir, "conflict.go")
	if err := os.WriteFile(conflictPath, []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newMiniEditorState(conflictPath)
	st.mu.Lock()
	st.text = "edited\n"
	st.mu.Unlock()
	if err := os.WriteFile(conflictPath, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ev := &mygo.CloseEvent{}
	st.onClose(ev)
	if !ev.DefaultPrevented() {
		t.Fatalf("外部改动冲突时应取消关闭")
	}
	if data, err := os.ReadFile(conflictPath); err != nil || string(data) != "external\n" {
		t.Fatalf("冲突时不应覆盖磁盘内容, data=%q err=%v", data, err)
	}

	tt := ui.NewTesterWith(func(c *ui.Context) {
		applyAppTheme(c)
		st.view(c)
	}, MiniEditorDefaultWidth, MiniEditorDefaultHeight, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})
	if !tt.HasText("保存失败") || !tt.HasText("重新加载") {
		t.Fatalf("保存失败后应显示错误条与重新加载按钮: %v", tt.Texts())
	}

	// 场景二：文件被删除导致保存失败，关闭时应取消关闭。
	deletedPath := filepath.Join(dir, "deleted.go")
	if err := os.WriteFile(deletedPath, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stDeleted := newMiniEditorState(deletedPath)
	stDeleted.mu.Lock()
	stDeleted.text = "v2\n"
	stDeleted.mu.Unlock()
	if err := os.Remove(deletedPath); err != nil {
		t.Fatal(err)
	}
	evDeleted := &mygo.CloseEvent{}
	stDeleted.onClose(evDeleted)
	if !evDeleted.DefaultPrevented() {
		t.Fatalf("文件被删除导致保存失败时应取消关闭")
	}

	// 场景三：正常保存成功时应允许关闭并落盘。
	cleanPath := filepath.Join(dir, "clean.go")
	if err := os.WriteFile(cleanPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stClean := newMiniEditorState(cleanPath)
	stClean.mu.Lock()
	stClean.text = "saved on close\n"
	stClean.mu.Unlock()
	evClean := &mygo.CloseEvent{}
	stClean.onClose(evClean)
	if evClean.DefaultPrevented() {
		t.Fatalf("保存成功时不应取消关闭")
	}
	if data, err := os.ReadFile(cleanPath); err != nil || string(data) != "saved on close\n" {
		t.Fatalf("关窗保存应将内容写入磁盘, data=%q err=%v", data, err)
	}
}

// TestUIReview28RevertDialogConcurrentCloseAndRender 验证撤销弹窗后台关闭与渲染并发执行时无数据竞争。
func TestUIReview28RevertDialogConcurrentCloseAndRender(t *testing.T) {
	t.Cleanup(func() { modalDragReset(modalDragRevert) })

	fake := NewFakeClient()
	s := newChatState(fake)
	t.Cleanup(s.close)
	st := getRevertDialogState(s)

	tt := ui.NewTesterWith(func(c *ui.Context) {
		applyAppTheme(c)
		renderRevertDialog(c, s)
	}, 800, 600, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})

	for i := 0; i < 20; i++ {
		st.mu.Lock()
		st.open = true
		st.threadID = "thread-1"
		st.turnID = "turn-1"
		st.loading = false
		st.executing = false
		st.err = ""
		st.preview = &RevertPreview{
			TurnID: "turn-1",
			Files:  []RevertFile{{Path: "a.go", Additions: 1}},
		}
		st.mu.Unlock()

		s.confirmRevert()
		for j := 0; j < 5; j++ {
			tt.Frame()
		}

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			st.mu.Lock()
			done := !st.open && !st.executing
			st.mu.Unlock()
			if done {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// TestUIReview28RevertPreviewTimeout 验证预览请求超时后弹窗离开加载态并显示错误。
func TestUIReview28RevertPreviewTimeout(t *testing.T) {
	t.Cleanup(func() { modalDragReset(modalDragRevert) })

	oldTimeout := revertPreviewTimeout
	revertPreviewTimeout = 20 * time.Millisecond
	t.Cleanup(func() { revertPreviewTimeout = oldTimeout })

	s := newChatState(&hangingRevertPreviewClient{FakeClient: NewFakeClient()})
	t.Cleanup(s.close)
	st := getRevertDialogState(s)

	tt := ui.NewTesterWith(func(c *ui.Context) {
		applyAppTheme(c)
		renderRevertDialog(c, s)
	}, 800, 600, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})

	s.openRevertPreview("thread-1", "turn-1")
	tt.Frame()
	if !tt.HasText("正在预览改动…") {
		t.Fatalf("刚打开预览时应处于加载态: %v", tt.Texts())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		st.mu.Lock()
		loading := st.loading
		errMsg := st.err
		st.mu.Unlock()
		if !loading && errMsg != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	tt.Frame()
	if tt.HasText("正在预览改动…") {
		t.Fatalf("超时后应离开加载态: %v", tt.Texts())
	}
	if !tt.HasText("关闭") {
		t.Fatalf("超时出错后应显示关闭按钮: %v", tt.Texts())
	}
	joined := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(joined, "加载撤销预览失败") {
		t.Fatalf("超时后应显示加载失败错误文案: %q", joined)
	}
}

// TestUIReview28LongPathTruncationAndButtonBounds 验证窄容器下超长路径截断且右侧状态与按钮仍在容器内。
func TestUIReview28LongPathTruncationAndButtonBounds(t *testing.T) {
	t.Cleanup(func() { modalDragReset(modalDragRevert) })

	longMDPath := "/workspace/" + strings.Repeat("very_long_directory_segment/", 12) + "README.md"
	st := &miniEditorState{
		path:     longMDPath,
		text:     "# title\n",
		baseline: "# title\n",
	}
	editorTester := ui.NewTesterWith(func(c *ui.Context) {
		applyAppTheme(c)
		st.view(c)
	}, 640, 420, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})

	previewBtn, ok := editorTester.Find("预览")
	if !ok {
		t.Fatalf("640 宽迷你编辑器应显示预览按钮: %v", editorTester.Texts())
	}
	statusRect, ok := editorTester.Find("已打开")
	if !ok {
		t.Fatalf("640 宽迷你编辑器应显示保存状态: %v", editorTester.Texts())
	}
	if previewBtn.W <= 0 || previewBtn.X < 0 || previewBtn.X+previewBtn.W > 640 {
		t.Fatalf("预览按钮应完整留在 640 容器内, got %+v", previewBtn)
	}
	if statusRect.W <= 0 || statusRect.X < 0 || statusRect.X+statusRect.W > previewBtn.X {
		t.Fatalf("状态文字应位于容器内且不被挤出, status=%+v preview=%+v", statusRect, previewBtn)
	}

	// 400 宽窗口减去 40 留白，撤销弹窗卡片实际宽度为 360（居于 [20, 380]）。
	s := newChatState(NewFakeClient())
	t.Cleanup(s.close)
	rst := getRevertDialogState(s)
	rst.mu.Lock()
	rst.open = true
	rst.preview = &RevertPreview{
		TurnID: "turn-1",
		Files: []RevertFile{
			{
				Path:      "/repo/" + strings.Repeat("nested_module_directory/", 10) + "target.go",
				Additions: 12,
				Deletions: 3,
				IsKept:    true,
			},
			{
				Path:      "main.go",
				Additions: 2,
			},
		},
	}
	rst.mu.Unlock()

	revertTester := ui.NewTesterWith(func(c *ui.Context) {
		applyAppTheme(c)
		renderRevertDialog(c, s)
	}, 400, 560, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})

	if revertTester.HasText("已保留（轮次结束后被修改过）") {
		t.Fatalf("不应再显示旧的冗长保留文案: %v", revertTester.Texts())
	}
	keptRect, ok := revertTester.Find("已保留")
	if !ok {
		t.Fatalf("应显示简短文案“已保留”: %v", revertTester.Texts())
	}
	addRect, ok := revertTester.Find("+12")
	if !ok {
		t.Fatalf("应显示新增行数 +12: %v", revertTester.Texts())
	}
	delRect, ok := revertTester.Find("-3")
	if !ok {
		t.Fatalf("应显示删除行数 -3: %v", revertTester.Texts())
	}
	confirmRect, ok := revertTester.Find("还原")
	if !ok {
		t.Fatalf("应显示还原按钮: %v", revertTester.Texts())
	}

	const cardLeft, cardRight float32 = 20, 380
	for name, r := range map[string]ui.Rect{
		"已保留": keptRect,
		"+12": addRect,
		"-3":  delRect,
		"还原":  confirmRect,
	} {
		if r.W <= 0 || r.X < cardLeft || r.X+r.W > cardRight {
			t.Fatalf("%s 应留在 360 宽撤销弹窗卡片 [%v, %v] 内, got %+v", name, cardLeft, cardRight, r)
		}
	}
}
