package mcodeui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestLiveReasoningClickDoesNotChangeState(t *testing.T) {
	s := newChatState(NewFakeClient())
	m := Message{ID: "live-reasoning", Role: "assistant", Kind: "reasoning", Text: "正在推理的正文"}
	tt := ui.NewTester(func(c *ui.Context) {
		renderReasoningMessage(c, s, m, m.ID, false, false, TokenUsage{}, 0)
	}, 800, 600)
	clickText(t, tt, "收起思考过程")
	s.mu.Lock()
	open := s.expandedReasoning[m.ID]
	s.mu.Unlock()
	if open {
		t.Fatal("流式思考点击不应改变展开状态")
	}
	if !tt.HasText("正在推理的正文") {
		t.Fatal("流式思考正文应继续显示")
	}
}

// TestThinkLabelLiveNoFramePending 验证流式思考标签不再通过 c.After 持续请求整窗重建。
func TestThinkLabelLiveNoFramePending(t *testing.T) {
	setAppReduceMotion(t, false)
	cases := []struct {
		name string
		view func(*ui.Context, *chatState, Message)
	}{
		{"reasoning", func(c *ui.Context, s *chatState, m Message) {
			renderReasoningMessage(c, s, m, m.ID, false, false, TokenUsage{}, 0)
		}},
		{"tool_beat", func(c *ui.Context, s *chatState, m Message) {
			renderToolBeatReasoning(c, s, m, m.ID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newChatState(NewFakeClient())
			t.Cleanup(s.close)
			m := Message{ID: "live-reasoning", Role: "assistant", Kind: "reasoning", Text: "正在推理的正文"}
			builds := 0
			tt := ui.NewTester(func(c *ui.Context) {
				builds++
				tc.view(c, s, m)
			}, 800, 300)
			tt.Frame()
			// 等待一次性的展开过渡与流式正文揭示收敛，之后流式标签静止时不应再请求帧或重建视图。
			tt.Advance(2 * time.Second)
			beforeBuilds := builds
			tt.Advance(time.Second)
			if builds != beforeBuilds {
				t.Errorf("流式思考标签不应持续重建组件树：%d → %d", beforeBuilds, builds)
			}
			if tt.FramePending() {
				t.Error("流式思考标签静止时不应请求下一帧")
			}
		})
	}
}
