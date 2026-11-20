package mcodeui

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// TestUIReview19_OpenLinkAllowlist 验证仅 http(s) 且 Host 非空的链接放行给系统打开，
// file://、smb://、javascript:、自定义协议及含嵌套 URL 的非 http(s) 链接均被吞掉。
func TestUIReview19_OpenLinkAllowlist(t *testing.T) {
	s := newChatState(NewFakeClient())
	cwd := t.TempDir()

	blocked := []string{
		"file:///etc/passwd",
		"smb://x/y",
		"javascript:alert(1)",
		"myapp://x",
		"file:///x?https://a",
		"https:///missing-host",
		"http:opaque",
		"",
	}
	for _, link := range blocked {
		if !handleTerminalOpenLink(s, cwd, link) {
			t.Fatalf("link %q 应返回 true 被吞掉，实际返回 false", link)
		}
	}

	allowed := []string{
		"https://example.com",
		"HTTP://Example.COM/path?q=1",
	}
	for _, link := range allowed {
		if handleTerminalOpenLink(s, cwd, link) {
			t.Fatalf("link %q 应返回 false 放行给系统打开，实际返回 true", link)
		}
	}

	if kind, ok := classifyTerminalLink("file:///x?https://a"); ok && kind == terminalLinkURL {
		t.Fatalf("file:///x?https://a 不应被分类为 URL")
	}
}

// TestUIReview19_ExitedTabKeepsLastOutput 验证标签在进程退出后 term 置为 nil 时，
// capture 与 persistTerminalState 仍保留 lastOutput，不会抹掉退出前的输出。
func TestUIReview19_ExitedTabKeepsLastOutput(t *testing.T) {
	t.Setenv("MCODE_DATA", t.TempDir())
	s := newChatState(NewFakeClient())
	t.Cleanup(func() { closeTerminals(s) })

	const wantOutput = "build ok\ndone"
	snap := captureTerminalThreadSnapshot("t1", []terminalTabInput{
		{dir: "/project", id: 1, term: nil, lastOutput: wantOutput},
	}, 1)
	if len(snap.Tabs) != 1 || snap.Tabs[0].Output != wantOutput {
		t.Fatalf("capture 输出 = %+v，想要 Output=%q", snap.Tabs, wantOutput)
	}

	st := getTerminalState(s)
	terminalMu.Lock()
	st.tabs["t1"] = []*terminalTab{
		{id: 1, dir: "/project", term: nil, err: "终端已退出", lastOutput: wantOutput},
	}
	st.active["t1"] = 1
	terminalMu.Unlock()

	persistTerminalState(s, "t1")
	loaded, ok := loadTerminalThreadSnapshot("t1")
	if !ok || len(loaded.Tabs) != 1 || loaded.Tabs[0].Output != wantOutput {
		t.Fatalf("落盘读回 = %+v (ok=%v)，想要 Output=%q", loaded.Tabs, ok, wantOutput)
	}
}

// TestUIReview19_DeleteThreadClosesTerminalsAndRemovesSnapshot 验证删除会话后，
// terminalStates 中不再残留该会话标签，底层终端被关闭且磁盘快照被删除；
// 归档会话则关闭存活标签并保留磁盘快照。
func TestUIReview19_DeleteThreadClosesTerminalsAndRemovesSnapshot(t *testing.T) {
	t.Setenv("MCODE_DATA", t.TempDir())
	stubTerminals(t)

	var closeCalls atomic.Int32
	closeTerminal = func(*terminal.Terminal) error {
		closeCalls.Add(1)
		return nil
	}

	s := newChatState(NewFakeClient())
	s.mu.Lock()
	s.projects = []Project{{ID: "p1", Path: "/project"}}
	s.threads = []Thread{
		{ID: "t1", ProjectID: "p1"},
		{ID: "t2", ProjectID: "p1"},
	}
	s.selectedThreadID = "t1"
	s.mu.Unlock()
	t.Cleanup(func() { closeTerminals(s) })

	st := getTerminalState(s)
	if _, err := createTerminalTab(s, st, "/project", false); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.selectedThreadID = "t2"
	s.mu.Unlock()
	if _, err := createTerminalTab(s, st, "/project", false); err != nil {
		t.Fatal(err)
	}

	// 删除 t1：释放终端并删除快照。
	s.forgetDeletedThread("t1")

	terminalMu.Lock()
	t1Tabs := len(st.tabs["t1"])
	t2Tabs := len(st.tabs["t2"])
	terminalMu.Unlock()
	if t1Tabs != 0 {
		t.Fatalf("删除会话后 t1 仍残留 %d 个标签", t1Tabs)
	}
	if t2Tabs != 1 {
		t.Fatalf("删除 t1 不应影响 t2 标签数，得到 %d", t2Tabs)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("删除会话应关闭 1 次终端，实际 %d", closeCalls.Load())
	}
	if _, err := os.Stat(terminalSnapshotPath("t1")); !os.IsNotExist(err) {
		t.Fatalf("删除会话后快照文件应已删除，Stat 返回 %v", err)
	}

	// 归档 t2：释放终端但保留快照，便于取消归档后恢复。
	closeThreadTerminals(s, "t2", false)
	terminalMu.Lock()
	t2TabsAfter := len(st.tabs["t2"])
	terminalMu.Unlock()
	if t2TabsAfter != 0 {
		t.Fatalf("归档会话后 t2 仍残留 %d 个标签", t2TabsAfter)
	}
	if closeCalls.Load() != 2 {
		t.Fatalf("归档会话应再关闭 1 次终端，实际 %d", closeCalls.Load())
	}
	if _, ok := loadTerminalThreadSnapshot("t2"); !ok {
		t.Fatal("归档会话应保留终端快照供恢复")
	}
}

// TestUIReview19_RenderDockAsyncRestore 验证 renderTerminalDock 不在渲染调用栈里同步读盘恢复，
// restoring 期间多次渲染不重复触发，后台完成后标签正常出现。
func TestUIReview19_RenderDockAsyncRestore(t *testing.T) {
	t.Setenv("MCODE_DATA", t.TempDir())
	stubTerminals(t)

	dir := t.TempDir()
	if err := saveTerminalThreadSnapshot(terminalThreadSnapshot{
		ThreadID: "t1",
		Tabs:     []terminalTabSnapshot{{Dir: dir, Output: "restored"}},
	}); err != nil {
		t.Fatal(err)
	}

	origLoad := loadTerminalSnapshotForRestore
	t.Cleanup(func() { loadTerminalSnapshotForRestore = origLoad })

	var readCalls atomic.Int32
	started := make(chan struct{})
	gate := make(chan struct{})
	var closeStartedOnce sync.Once
	loadTerminalSnapshotForRestore = func(threadID string) (terminalThreadSnapshot, bool) {
		readCalls.Add(1)
		closeStartedOnce.Do(func() { close(started) })
		<-gate
		return origLoad(threadID)
	}

	s := newChatState(NewFakeClient())
	s.mu.Lock()
	s.projects = []Project{{ID: "p1", Path: dir}}
	s.threads = []Thread{{ID: "t1", ProjectID: "p1"}}
	s.selectedThreadID = "t1"
	s.mu.Unlock()
	t.Cleanup(func() { closeTerminals(s) })

	st := getTerminalState(s)
	terminalMu.Lock()
	st.visible = true
	terminalMu.Unlock()

	// 若在渲染栈内同步读盘，NewTester 首帧就会在 <-gate 处阻塞。
	tt := ui.NewTester(func(c *ui.Context) {
		s.setUIServices(c.Services())
		renderTerminalDock(c, s)
	}, 800, 600)

	terminalMu.Lock()
	firstFrameTabs := len(st.tabs["t1"])
	restoring := st.restoring["t1"]
	terminalMu.Unlock()
	if firstFrameTabs != 0 || !restoring {
		t.Fatalf("首帧不应同步恢复标签，tabs=%d restoring=%v", firstFrameTabs, restoring)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("后台恢复 goroutine 未启动")
	}

	// restoring 期间再渲染一帧，不得重复触发恢复读盘。
	tt.Frame()
	if got := readCalls.Load(); got != 1 {
		t.Fatalf("restoring 期间重复触发恢复，readCalls=%d", got)
	}

	close(gate)
	waitFor(t, tt, func() bool {
		terminalMu.Lock()
		defer terminalMu.Unlock()
		return len(st.tabs["t1"]) == 1 && !st.restoring["t1"] && st.restored["t1"]
	})
	if got := readCalls.Load(); got != 1 {
		t.Fatalf("同一会话只应恢复一次，readCalls=%d", got)
	}
}

// TestUIReview19_NoBlinkAndCopyAndNarrowSearchBar 验证减少动效关闭光标闪烁、
// 按钮文案为「收起终端」、拖拽条无 Tooltip，以及窄窗搜索条保留关闭按钮在可视区域内。
func TestUIReview19_NoBlinkAndCopyAndNarrowSearchBar(t *testing.T) {
	stubTerminals(t)
	setAppReduceMotion(t, true)

	var capturedOpts terminal.Options
	newTerminal = func(opts terminal.Options) (*terminal.Terminal, error) {
		capturedOpts = opts
		return &terminal.Terminal{}, nil
	}

	s := newChatState(NewFakeClient())
	s.mu.Lock()
	s.projects = []Project{{ID: "p1", Path: "/project"}}
	s.threads = []Thread{{ID: "t1", ProjectID: "p1"}}
	s.selectedThreadID = "t1"
	s.mu.Unlock()
	t.Cleanup(func() { closeTerminals(s) })

	st := getTerminalState(s)
	tab, err := createTerminalTab(s, st, "/project", false)
	if err != nil {
		t.Fatal(err)
	}
	if !capturedOpts.NoBlink {
		t.Fatal("开启减少动效时应传入 NoBlink: true")
	}

	terminalMu.Lock()
	st.visible = true
	st.search = st.search.opened(tab.id).withText("panic").withStatus("panic", 12, 3, false)
	terminalMu.Unlock()

	const narrowWidth = 280
	tt := ui.NewTester(func(c *ui.Context) {
		s.setUIServices(c.Services())
		renderTerminalDock(c, s)
	}, narrowWidth, 300)

	if _, ok := tt.Find("收起终端"); !ok {
		t.Fatalf("未找到「收起终端」按钮，texts=%q", tt.Texts())
	}
	if _, ok := tt.Find("关闭终端"); ok {
		t.Fatalf("不应再使用「关闭终端」作为收起按钮文案")
	}

	closeRect, ok := tt.Find("关闭搜索")
	if !ok {
		t.Fatalf("窄窗下未找到「关闭搜索」按钮")
	}
	if closeRect.X < 0 || closeRect.X+closeRect.W > narrowWidth {
		t.Fatalf("窄窗下「关闭搜索」按钮越界: rect=%+v, width=%d", closeRect, narrowWidth)
	}

	// 悬停拖拽条后推进时间，不应弹出「拖动调整终端高度」Tooltip。
	resizerRect, ok := tt.Find("拖动调整终端高度")
	if !ok {
		t.Fatalf("缺少拖拽条元素")
	}
	if info, hasAccess := tt.Access("拖动调整终端高度"); !hasAccess || info.Description != "" {
		t.Fatalf("拖拽条不应带有 Tooltip 描述，got access=%v desc=%q", hasAccess, info.Description)
	}
	tt.Move(resizerRect.X+resizerRect.W/2, resizerRect.Y+resizerRect.H/2)
	tt.Advance(time.Second)
	tt.Frame()
	labelCount := 0
	for _, txt := range tt.Texts() {
		if txt == "拖动调整终端高度" {
			labelCount++
		}
	}
	if labelCount > 1 {
		t.Fatalf("拖拽条不应再显示「拖动调整终端高度」Tooltip，出现次数=%d", labelCount)
	}
}
