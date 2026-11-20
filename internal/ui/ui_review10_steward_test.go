package mcodeui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mcode/internal/testenv"
)

func TestUIReview10BoardGroupsCached(t *testing.T) {
	task := KanbanTask{ID: "t1", ThreadID: "worker", Title: "看板任务", Origin: "owner", Status: "todo"}
	tt, s, fake := newStewardBoardTester(t, 700, "steward", []KanbanTask{task})
	waitFor(t, tt, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.stewardBoard.active && !s.stewardBoard.loading
	})
	tt.Frame()

	before := stewardBoardGroupsBuildCount.Load()
	oldRev := s.stewardBoardRev()
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if got := stewardBoardGroupsBuildCount.Load(); got != before {
		t.Fatalf("stewardBoard.rev 未变化时构建计数不应增长: before=%d, got=%d", before, got)
	}

	task2 := KanbanTask{ID: "t2", ThreadID: "worker", Title: "新任务", Origin: "owner", Status: "in_progress"}
	fake.SetKanbanBoard(KanbanBoard{Tasks: []KanbanTask{task, task2}})
	fake.TriggerKanbanChange()
	waitFor(t, tt, func() bool { return s.stewardBoardRev() > oldRev })
	tt.Frame()

	after := stewardBoardGroupsBuildCount.Load()
	if after <= before {
		t.Fatalf("stewardBoard.rev 增长后构建计数应增长: before=%d, after=%d", before, after)
	}
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if got := stewardBoardGroupsBuildCount.Load(); got != after {
		t.Fatalf("再次静止渲染时构建计数不应增长: want=%d, got=%d", after, got)
	}
}

func TestUIReview10DecisionResultCached(t *testing.T) {
	testenv.TempHome(t)
	s := newChatState(NewFakeClient())
	t.Cleanup(s.close)

	m := Message{
		ID:   "m1",
		Kind: stewardDecisionKind,
		Text: `{"id":"D-10","question":"选哪一个？","options":[{"key":"A","text":"方案一"},{"key":"B","text":"方案二"}]}`,
	}
	s.mu.Lock()
	s.messages = []Message{m}
	s.messagesRev = 1
	s.mu.Unlock()

	tt := ui.NewTesterWith(func(c *ui.Context) {
		renderStewardDecisionCard(c, s, m)
	}, 840, 520, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tt.Frame()

	before := stewardDecisionResultBuildCount.Load()
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if got := stewardDecisionResultBuildCount.Load(); got != before {
		t.Fatalf("messagesRev 未变化时决策结果不应重算: before=%d, got=%d", before, got)
	}

	s.mu.Lock()
	s.messages = append(s.messages, Message{
		ID:   "a1",
		Kind: stewardDecisionAnswerKind,
		Role: "user",
		Text: `{"id":"D-10","answer":"A"}`,
	})
	s.messagesRev++
	s.mu.Unlock()
	tt.Frame()

	after := stewardDecisionResultBuildCount.Load()
	if after <= before {
		t.Fatalf("messagesRev 增长后应重算决策结果: before=%d, after=%d", before, after)
	}
	if !tt.HasText("已选 A · 方案一") {
		t.Fatalf("新增回答消息后应显示 answered 结果，当前文本: %v", tt.Texts())
	}
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if got := stewardDecisionResultBuildCount.Load(); got != after {
		t.Fatalf("answered 后再次静止渲染不应重算: want=%d, got=%d", after, got)
	}
}

func TestUIReview10QuotaWarningText(t *testing.T) {
	resetAt := time.Date(2026, 1, 1, 13, 45, 0, 0, time.Local)

	groups96 := []trayQuotaGroupModel{{
		Title: "测试",
		Accounts: []trayQuotaAccountModel{{
			Windows: []trayQuotaWindowModel{{
				ShortLabel:  "5 小时",
				UsedPercent: 96,
				resetsAt:    resetAt,
			}},
		}},
	}}
	tt96 := ui.NewTester(func(c *ui.Context) {
		renderQuotaGroupList(c, "warn96-", groups96, quotaGroupListOptions{})
	}, 500, 300)
	tt96.Frame()
	if !tt96.HasText("5 小时") || !tt96.HasText("96%") {
		t.Fatalf("96%% 应包含窗口名与百分比: %v", tt96.Texts())
	}
	if tt96.HasText("即将用尽") || tt96.HasText("已用尽") || tt96.HasText("重置于") || tt96.HasText("13:45") {
		t.Fatalf("96%% 不应重复拼接用尽警告或重置时刻: %v", tt96.Texts())
	}

	groups100 := []trayQuotaGroupModel{{
		Title: "测试",
		Accounts: []trayQuotaAccountModel{{
			Windows: []trayQuotaWindowModel{{
				ShortLabel:  "每周",
				UsedPercent: 100,
				resetsAt:    resetAt,
			}},
		}},
	}}
	tt100 := ui.NewTester(func(c *ui.Context) {
		renderQuotaGroupList(c, "warn100-", groups100, quotaGroupListOptions{})
	}, 500, 300)
	tt100.Frame()
	if !tt100.HasText("每周") || !tt100.HasText("100%") {
		t.Fatalf("100%% 应包含窗口名与百分比: %v", tt100.Texts())
	}
	if tt100.HasText("即将用尽") || tt100.HasText("已用尽") || tt100.HasText("重置于") {
		t.Fatalf("100%% 不应包含重复用尽警告或重置时刻: %v", tt100.Texts())
	}
}
