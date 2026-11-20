package mcodeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestUIReview32PreviewAnnotateDrawerNarrow(t *testing.T) {
	setAppReduceMotion(t, true)
	s := &chatState{}
	defer inspectorDisposeAllSessions(s)

	tab := getPreviewState(s)
	selector := "div#app>main.panel>section.content>ul.list>li.item:nth-child"
	if len(selector) != 60 {
		t.Fatalf("选择器长度 = %d，期望 60", len(selector))
	}
	previewMu.Lock()
	tab.annotations = []previewAnnotation{{id: 1, PageID: 1, Selector: selector}}
	previewMu.Unlock()

	tester := ui.NewTesterWith(func(c *ui.Context) {
		previewMu.Lock()
		snap := previewSnapshotLocked(tab)
		previewMu.Unlock()
		renderPreviewAnnotateDrawer(c, s, snap)
	}, int(inspectorMinWidth), 300, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tester.Frame()
	tester.Advance(time.Second)
	tester.Frame()

	drawer := ui.Rect{X: 0, Y: 0, W: inspectorMinWidth, H: previewAnnotateDrawerHeight}
	const eps float32 = 0.5
	checkInDrawer := func(name string, rect ui.Rect) {
		t.Helper()
		if rect.W <= 0 || rect.H <= 0 {
			t.Fatalf("%s 尺寸无效: %+v", name, rect)
		}
		if rect.X < drawer.X-eps || rect.Y < drawer.Y-eps ||
			rect.X+rect.W > drawer.X+drawer.W+eps ||
			rect.Y+rect.H > drawer.Y+drawer.H+eps {
			t.Fatalf("%s 超出抽屉范围: rect=%+v, drawer=%+v", name, rect, drawer)
		}
	}

	commentRect, ok := tester.Find("标注 1 备注")
	if !ok {
		t.Fatal("未找到备注输入框")
	}
	removeRect, ok := tester.Find("删除")
	if !ok {
		t.Fatal("未找到删除按钮")
	}
	checkInDrawer("备注输入框", commentRect)
	checkInDrawer("删除按钮", removeRect)
	if commentRect.X+commentRect.W > removeRect.X+eps {
		t.Fatalf("备注输入框与删除按钮横向重叠: comment=%+v, remove=%+v", commentRect, removeRect)
	}
}

func TestUIReview32PreviewTabsNarrow(t *testing.T) {
	setAppReduceMotion(t, true)
	s := &chatState{}
	defer inspectorDisposeAllSessions(s)

	longTitle := func(prefix string) string {
		return prefix + strings.Repeat("-LongWebPageTitleWithoutSpaces", 3)
	}
	_, pageA := browserAttachTestPage(s, "https://example.test/a")
	inspectorSelect(s, sidePanePreviewID, false)
	browserNewTab(s)
	_, pageB := browserAttachTestPage(s, "https://example.test/b")
	browserNewTab(s)
	_, pageC := browserAttachTestPage(s, "https://example.test/c")
	pageA.title(&mygo.TitleEvent{Title: longTitle("TabOne")})
	pageB.title(&mygo.TitleEvent{Title: longTitle("TabTwo")})
	pageC.title(&mygo.TitleEvent{Title: longTitle("TabThree")})

	tester := ui.NewTesterWith(func(c *ui.Context) {
		renderPreviewPanel(c, s)
	}, int(inspectorMinWidth), 600, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tester.Frame()
	tester.Advance(time.Second)
	tester.Frame()

	const eps float32 = 0.5
	var tabListFound bool
	var tabCount, closeCount int
	for _, a := range tester.AccessAll() {
		switch {
		case a.Role == ui.RoleTabList:
			tabListFound = true
			if a.Bounds.X < -eps || a.Bounds.X+a.Bounds.W > inspectorMinWidth+eps {
				t.Fatalf("标签栏超出面板范围: %+v (width=%.1f)", a.Bounds, inspectorMinWidth)
			}
		case a.Role == ui.RoleTab:
			tabCount++
			if a.Bounds.W <= 0 || a.Bounds.X < -eps || a.Bounds.X+a.Bounds.W > inspectorMinWidth+eps {
				t.Fatalf("标签项[%d] 宽度或右缘异常: %+v", tabCount, a.Bounds)
			}
		case a.Role == ui.RoleButton && a.Name == "关闭网页标签":
			closeCount++
			if a.Bounds.W <= 0 || a.Bounds.H <= 0 || a.Bounds.X+a.Bounds.W > inspectorMinWidth+eps {
				t.Fatalf("关闭按钮[%d] 被压扁或越界: %+v", closeCount, a.Bounds)
			}
		}
	}
	if !tabListFound {
		t.Fatal("未找到标签栏 RoleTabList")
	}
	if tabCount != 3 || closeCount != 3 {
		t.Fatalf("标签或关闭按钮数量错误: tabs=%d closeButtons=%d", tabCount, closeCount)
	}
}
