package mcodeui

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 面板关闭时没有可关闭的预览。
func TestFileManagerShortcutPanelClosed(t *testing.T) {
	_, s, tt := openFileManagerForTest(t)
	waitFor(t, tt, func() bool { return tt.HasText("a.txt") })

	closeFileManager(s)
	waitFor(t, tt, func() bool { return !isFileManagerOpen(s) })

	if fileManagerHasClosableTab(s) {
		t.Fatal("面板关闭时不应报告可关闭的预览")
	}
	if fileManagerCloseActiveTab(s) {
		t.Fatal("面板关闭时不应关闭任何内容")
	}
}

// 面板打开但停在目录列表时，⌘W 没有预览可关，应落回关闭窗口。
func TestFileManagerShortcutDirectoryListNotClosable(t *testing.T) {
	_, s, tt := openFileManagerForTest(t)
	waitFor(t, tt, func() bool { return tt.HasText("a.txt") })

	if !isFileManagerOpen(s) {
		t.Fatal("面板应处于打开状态")
	}
	if fileManagerHasClosableTab(s) {
		t.Fatal("目录列表不应报告可关闭的预览")
	}
	if fileManagerCloseActiveTab(s) {
		t.Fatal("目录列表不应关闭任何内容")
	}
}

// 外部入口（聊天卡片、终端路径）打开的最后一个预览被 ⌘W 关掉后回到文件树，侧栏保持打开。
func TestFileManagerShortcutExternalTabReturnsToTree(t *testing.T) {
	_, s, tt := openFileManagerForTest(t)
	waitFor(t, tt, func() bool { return tt.HasText("a.txt") })

	openTestFileManagerFileExternal(t, s, "a.txt")
	waitForFileManagerText(t, tt, s, "a.txt", "hello")

	if !fileManagerHasClosableTab(s) {
		t.Fatal("预览文件时应报告可关闭的预览")
	}
	if !fileManagerCloseActiveTab(s) {
		t.Fatal("预览文件时应关闭预览")
	}
	if !isFileManagerOpen(s) {
		t.Fatal("关掉最后一个预览后侧栏应保持打开")
	}
	if _, _, _, _, _, showList := fileManagerTabsForTest(s); !showList {
		t.Fatal("关掉最后一个预览后应回到文件树")
	}
}

// 从侧栏预览文件时 ⌘W 只关闭预览 tab：回到目录列表，面板保持打开。
func TestFileManagerShortcutClosesPreviewOnly(t *testing.T) {
	_, s, tt := openFileManagerForTest(t)
	waitFor(t, tt, func() bool { return tt.HasText("a.txt") })

	if err := tt.Click("a.txt"); err != nil {
		t.Fatalf("click file row failed: %v", err)
	}
	waitForFileManagerText(t, tt, s, "a.txt", "hello")

	if !fileManagerHasClosableTab(s) {
		t.Fatal("预览文件时应报告可关闭的预览")
	}
	if !fileManagerCloseActiveTab(s) {
		t.Fatal("预览文件时应关闭预览")
	}

	dir, _, active := fileManagerSnapshot(s)
	if active != "" {
		t.Fatalf("关闭预览后应没有激活的 tab，得到 %q", active)
	}
	if paths := fileManagerTabPaths(s); len(paths) != 0 {
		t.Fatalf("关闭最后一个 tab 后不应剩下 tab，得到 %v", paths)
	}
	if dir != "" {
		t.Fatalf("关闭预览后应回到文件所在目录，得到 %q", dir)
	}
	if !isFileManagerOpen(s) {
		t.Fatal("关闭预览后文件面板应保持打开")
	}
	waitFor(t, tt, func() bool { return tt.HasText("sub") })
}

// macOS 下文件树选中文件按空格触发快速查看，目录、非 macOS 与输入框聚焦时不触发。
func TestFileManagerShortcutQuickLookSpace(t *testing.T) {
	oldGOOS, oldPreview := fileManagerGOOS, fileManagerPreviewItem
	t.Cleanup(func() {
		fileManagerGOOS = oldGOOS
		fileManagerPreviewItem = oldPreview
	})

	var previewed []string
	fileManagerPreviewItem = func(abs string) error {
		previewed = append(previewed, abs)
		return nil
	}
	fileManagerGOOS = "darwin"

	fake, s, tt := openFileManagerForTest(t)
	waitFor(t, tt, func() bool { return tt.HasText("a.txt") && tt.HasText("sub") })
	workDir, ok := fake.ThreadWorkDir(fileManagerThreadID(s))
	if !ok {
		t.Fatal("thread workDir missing")
	}

	// 先点目录让文件树获得焦点，再按空格：目录不触发快速查看。
	if err := tt.Click("sub"); err != nil {
		t.Fatalf("click sub failed: %v", err)
	}
	waitFor(t, tt, func() bool { return tt.HasText("b.go") })
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if len(previewed) != 0 {
		t.Fatalf("选中目录按空格不应触发快速查看，得到 %v", previewed)
	}

	// 收起 sub 后下移选中 a.txt，按空格触发快速查看。
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	want := []string{filepath.Join(workDir, "a.txt")}
	if !slices.Equal(previewed, want) {
		t.Fatalf("选中文件按空格后快速查看 = %v，期望 %v", previewed, want)
	}

	// 焦点在搜索输入框时，空格必须正常输入，不触发快速查看。
	previewed = nil
	if err := tt.Click("搜索文件"); err != nil {
		t.Fatalf("click search input failed: %v", err)
	}
	tt.Type("a")
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if len(previewed) != 0 {
		t.Fatalf("输入框聚焦时按空格不应触发快速查看，得到 %v", previewed)
	}

	// 非 macOS 下文件树选中文件按空格不触发快速查看。
	fileManagerGOOS = "linux"
	if err := tt.Click("清除搜索"); err != nil {
		t.Fatalf("click clear search failed: %v", err)
	}
	waitFor(t, tt, func() bool { return tt.HasText("sub") })
	if err := tt.Click("sub"); err != nil {
		t.Fatalf("click sub failed: %v", err)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if len(previewed) != 0 {
		t.Fatalf("非 macOS 下按空格不应触发快速查看，得到 %v", previewed)
	}
}
