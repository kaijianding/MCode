package mcodeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestUIReview4UserMessageMentions 验证不含 '@' 的用户消息跳过会话快照与拆分，
// 含 @会话标题 的用户消息仍正常渲染引用胶囊。
func TestUIReview4UserMessageMentions(t *testing.T) {
	s := newChatState(NewFakeClient())
	t.Cleanup(s.close)
	s.mu.Lock()
	s.selectedThreadID = "t-cur"
	s.threads = []Thread{
		{ID: "t-cur", ProjectID: "p1", Title: "当前会话"},
		{ID: "t-ref", ProjectID: "p1", Title: "发布计划"},
	}
	s.mu.Unlock()

	var snapshotCalls int
	orig := userMentionThreadsSnapshot
	userMentionThreadsSnapshot = func(st *chatState) []Thread {
		snapshotCalls++
		return orig(st)
	}
	t.Cleanup(func() {
		userMentionThreadsSnapshot = orig
	})

	t.Run("不含@跳过快照", func(t *testing.T) {
		snapshotCalls = 0
		msg := Message{ID: "u-plain", Role: "user", Text: "普通提问不需要查会话", TurnIndex: 1, IsFinal: true}
		tt := ui.NewTester(func(c *ui.Context) {
			renderUserMessage(c, s, msg)
		}, 700, 200)
		tt.Frame()
		if snapshotCalls != 0 {
			t.Fatalf("不含 '@' 时不应调用快照函数，实际调用 %d 次", snapshotCalls)
		}
		if !tt.HasText("普通提问不需要查会话") {
			t.Fatalf("应渲染纯文本正文，当前文案: %q", tt.Texts())
		}
	})

	t.Run("含@会话标题渲染引用胶囊", func(t *testing.T) {
		snapshotCalls = 0
		msg := Message{ID: "u-mention", Role: "user", Text: "接着 @发布计划 继续做", TurnIndex: 2, IsFinal: true}
		tt := ui.NewTester(func(c *ui.Context) {
			renderUserMessage(c, s, msg)
		}, 700, 200)
		tt.Frame()
		if snapshotCalls == 0 {
			t.Fatalf("含 '@' 时应调用快照函数")
		}
		if !tt.HasText("@发布计划") {
			t.Fatalf("应渲染 @发布计划 引用胶囊，当前文案: %q", tt.Texts())
		}
	})
}
