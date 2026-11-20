package mcodeui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestUIReview7SidebarKeyboard(t *testing.T) {
	fake := NewFakeClient()
	now := time.Now()
	fake.threads["p1"] = []Thread{
		{ID: "t1", ProjectID: "p1", Title: "第一会话", UpdatedAt: now},
		{ID: "t2", ProjectID: "p1", Title: "第二会话", UpdatedAt: now.Add(-time.Minute)},
	}
	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool {
		return tt.HasText("第一会话") && tt.HasText("第二会话")
	})

	for i := 0; i < 64 && !tt.Focused("打开会话 第二会话"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("打开会话 第二会话") {
		t.Fatal("侧栏第二个会话行无法通过 Tab 聚焦")
	}
	tt.Key(0, ui.KeyEnter)
	waitFor(t, tt, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.selectedThreadID == "t2"
	})

	for i := 0; i < 64 && !tt.Focused("收起项目 mcode-test"); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("收起项目 mcode-test") {
		t.Fatal("项目折叠区无法通过 Tab 聚焦")
	}
	tt.Key(0, ui.KeyEnter)
	waitFor(t, tt, func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.collapsedProj["p1"]
	})
}
