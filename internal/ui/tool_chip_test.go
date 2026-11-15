package mcodeui

import (
	"bytes"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestConversationArrowsReducedMotion(t *testing.T) {
	setAppReduceMotion(t, true)
	options := ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}}
	for _, name := range []string{"group", "queue"} {
		t.Run(name, func(t *testing.T) {
			s := newChatState(NewFakeClient())
			s.embeddedBee = true
			view := func(c *ui.Context) {
				if name == "group" {
					renderToolGroupItem(c, s, []Message{{ID: "tool", ToolName: "read", ToolStatus: "completed"}}, "group", "", false)
				} else {
					renderComposerTabSlot(c, s, "thread", []QueuedFollowUp{{ID: "item", Text: "排队内容"}}, false, nil)
				}
			}
			tester := ui.NewTesterWith(view, 800, 600, options)
			tester.Frame()
			if name == "group" {
				s.setToolGroupOpen("group", true)
			} else {
				toggleQueueExpanded(s, "thread")
			}
			tester.Frame()
			fresh := ui.NewTesterWith(view, 800, 600, options)
			fresh.Frame()
			if !bytes.Equal(tester.Image().Pix, fresh.Image().Pix) || tester.FramePending() {
				t.Fatal("减少动效时展开应立即到达终态且不请求帧")
			}
		})
	}
}

func TestToolDetailUnfoldMotion(t *testing.T) {
	setAppReduceMotion(t, false)
	s := newChatState(NewFakeClient())
	m := Message{ID: "motion-tool", Role: "tool", ToolName: "read", ToolStatus: "completed", ToolInput: `{"path":"motion-detail-file"}`}
	tt := ui.NewTester(func(c *ui.Context) { renderSingleToolItem(c, s, m) }, 800, 600)
	tt.Frame()
	s.toggleToolChip(m, []Message{m})
	tt.Frame()
	tt.Advance(motionUnfold / 2)
	if !tt.FramePending() {
		t.Fatal("展开时应请求动画帧")
	}
	tt.Advance(motionUnfold + time.Millisecond)
	s.toggleToolChip(m, []Message{m})
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("收起时应请求动画帧")
	}
}

func TestChatFindReducedMotion(t *testing.T) {
	setAppReduceMotion(t, true)
	s := newChatState(NewFakeClient())
	tester := ui.NewTesterWith(func(c *ui.Context) {
		renderChatFindBar(c, s, "thread", 0, nil, nil)
	}, 800, 600, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tester.Frame()
	s.openChatFind()
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("减少动效时查找条出现不应请求动画帧")
	}
	s.closeChatFind()
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("减少动效时查找条消失不应请求动画帧")
	}
}

func TestToolMessageState(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   ToolState
	}{
		{"pending", ToolPending}, {"running", ToolRunning}, {"background", ToolRunning},
		{"completed", ToolDone}, {"failed", ToolError}, {"blocked", ToolError},
	} {
		if got := toolMessageState(Message{ToolStatus: tc.status}); got != tc.want {
			t.Errorf("状态 %q = %v，期望 %v", tc.status, got, tc.want)
		}
	}
}

func TestPendingToolInputClosesOnCompletion(t *testing.T) {
	s := newChatState(NewFakeClient())
	m := Message{ID: "pending-input", Role: "tool", ToolName: "read", ToolStatus: "pending", ToolInput: `{"path":"unique-pending-file"}`}
	tt := ui.NewTester(func(c *ui.Context) { renderSingleToolItem(c, s, m) }, 800, 600)
	if !tt.HasText("unique-pending-file") {
		t.Fatal("等待时应显示入参")
	}
	m.ToolStatus = "completed"
	tt = ui.NewTester(func(c *ui.Context) { renderSingleToolItem(c, s, m) }, 800, 600)
	if tt.HasText(m.ToolInput) {
		t.Fatal("完成后应收起入参")
	}
	s.toggleToolChip(m, []Message{m})
	if !s.toolMessageExpanded(m) {
		t.Fatal("手动展开应保留")
	}
}
