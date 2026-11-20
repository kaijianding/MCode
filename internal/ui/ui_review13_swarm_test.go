package mcodeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestUIReview13BeeWindowNeedsTick(t *testing.T) {
	if beeWindowNeedsTick(true, SwarmBee{Status: "completed", PendingMerge: true}, false, 0) {
		t.Fatal("已结束且仅 PendingMerge、无子会话运行与后台任务时不应每秒重绘")
	}
	if !beeWindowNeedsTick(true, SwarmBee{Status: "running"}, false, 0) {
		t.Fatal("bee 运行中应每秒重绘")
	}
	if !beeWindowNeedsTick(false, SwarmBee{}, false, 0) {
		t.Fatal("未找到 bee 时应每秒重绘以重试")
	}
	if !beeWindowNeedsTick(true, SwarmBee{Status: "completed"}, true, 0) {
		t.Fatal("子会话仍在运行时应每秒重绘")
	}
	if !beeWindowNeedsTick(true, SwarmBee{Status: "completed"}, false, 1) {
		t.Fatal("子会话存在后台任务时应每秒重绘")
	}
}

func TestUIReview13MenuOpensSwarmPanel(t *testing.T) {
	fake := NewFakeClient()
	s := newChatState(fake)
	const leadID = "lead-ui-review13-panel"
	s.selectedThreadID = leadID
	bees := []SwarmBee{
		{ThreadID: "bee-1", Index: 1, Name: "Birch", Task: "执行任务", Status: "completed"},
	}
	hp := getSwarmPanelState(s)
	swarmPanelMu.Lock()
	hp.loadedFor = leadID
	hp.setStateLocked(SwarmState{
		Available:    true,
		Enabled:      true,
		LeadThreadID: leadID,
		Bees:         bees,
	})
	swarmPanelMu.Unlock()

	reset := func() {
		swarmFloatMu.Lock()
		delete(swarmFloatPanelDismissed, leadID)
		delete(swarmFloatDismissedBees, leadID)
		delete(swarmBeeDoneSince, leadID)
		delete(swarmBeeAutoHideHandled, leadID)
		delete(swarmBeeSeenActive, leadID)
		delete(swarmBeeKeptOpen, leadID)
		delete(swarmMenuOpen, leadID+"|"+string(teamPanelID))
		delete(swarmSelectedBeeID, leadID)
		swarmFloatMu.Unlock()
	}
	reset()
	swarmFloatMu.Lock()
	swarmBeeSeenActive[leadID] = map[string]bool{"bee-1": true}
	swarmFloatMu.Unlock()
	t.Cleanup(func() {
		closeEmbeddedBees(s)
		reset()
	})

	tt := ui.NewTesterWith(func(c *ui.Context) {
		renderSwarmBeeFloats(c, s)
	}, 1600, 900, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
	tt.Frame()

	if err := tt.Click("子代理列表，共 1 个"); err != nil {
		t.Fatalf("打开子代理菜单失败: %v", err)
	}
	tt.Frame()

	if err := tt.Click("汇总面板"); err != nil {
		t.Fatalf("点击汇总面板入口失败: %v", err)
	}
	tt.Frame()

	if !getSwarmPanelState(s).IsOpen() {
		t.Fatal("点击汇总面板后 getSwarmPanelState(s).open 应变为 true")
	}

	if err := tt.Click("关闭汇总面板"); err != nil {
		t.Fatalf("点击关闭汇总面板失败: %v", err)
	}
	tt.Frame()

	if getSwarmPanelState(s).IsOpen() {
		t.Fatal("点击关闭汇总面板后 getSwarmPanelState(s).open 应变为 false")
	}
}

func TestUIReview13QuestionAutoForwardCountdown(t *testing.T) {
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	q := &SwarmBeeQuestion{
		RequestID:     "req-1",
		AutoForwardAt: base.Add(30 * time.Second),
	}
	h1 := swarmQuestionAutoForwardHint(q, base)
	h2 := swarmQuestionAutoForwardHint(q, base.Add(1*time.Second))
	if h1 == "" || h2 == "" {
		t.Fatalf("倒计时提示不应为空: h1=%q, h2=%q", h1, h2)
	}
	if h1 == h2 {
		t.Fatalf("相隔 1 秒的倒计时提示应不同: h1=%q, h2=%q", h1, h2)
	}
}

func TestUIReview13ReportCardChineseAndScroll(t *testing.T) {
	s := newChatState(NewFakeClient())
	entry := swarmReportEntry{
		Name:         "Birch",
		Task:         "检查报告正文滚动与中文单位",
		Status:       "已完成",
		ToolCalls:    3,
		ChangedFiles: []string{"a.go", "b.go"},
		Summary:      strings.Repeat("这是一段较长的汇报正文内容。\n", 80) + "汇报末尾标记",
	}
	entry.statsRecorded = true
	entry.filesRecorded = true

	tt := ui.NewTester(func(c *ui.Context) {
		renderSwarmReportEntryCard(c, s, nil, "msg-r13", 0, entry)
	}, 800, 600)
	tt.Frame()

	if !tt.HasText("3 次工具调用") {
		t.Fatalf("期望显示 '3 次工具调用'，实际文本: %v", tt.Texts())
	}
	if !tt.HasText("2 个文件") {
		t.Fatalf("期望显示 '2 个文件'，实际文本: %v", tt.Texts())
	}
	scrollRect, ok := tt.Find("swarm-report-card-msg-r13-0-body-scroll")
	if !ok {
		t.Fatal("未找到报告正文滚动容器")
	}
	maxExpected := (600 - Space8) * 3 / 4
	if scrollRect.H > maxExpected+1 {
		t.Fatalf("非原生 flyout 下报告正文高度 = %v，应受限于 %v", scrollRect.H, maxExpected)
	}
}
