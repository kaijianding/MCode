package mcodeui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestTurnTimingAuthoritativeAndZeroIndex(t *testing.T) {
	start := "2026-11-06T08:00:00.123Z"
	end := "2026-11-06T08:00:12.123Z"
	msgs := []Message{
		{ID: "a", Role: "assistant", TurnID: "first", TurnIndex: 0, CreatedAt: "2026-11-06T08:00:02Z", TurnStartedAt: start, TurnCompletedAt: end},
		{ID: "other", Role: "user", TurnID: "other", TurnIndex: 0, CreatedAt: "2026-11-06T07:00:00Z"},
	}
	s := &chatState{messages: msgs}
	a, b := turnFinishedSpanFor(s, msgs[0])
	if b.Sub(a) != 12*time.Second || a.Format(time.RFC3339Nano) != start || b.Format(time.RFC3339Nano) != end {
		t.Fatalf("权威起止时间: %v %v", a, b)
	}
	if got := turnStartTime(msgs[:1], 0); !got.Equal(a) {
		t.Fatalf("运行起点: %v", got)
	}
	legacy := []Message{{Role: "user", TurnIndex: 0, CreatedAt: start}, {Role: "assistant", TurnIndex: 0, CreatedAt: end}}
	a, b = turnSpanFromMessages(legacy, 0)
	if b.Sub(a) != 12*time.Second {
		t.Fatalf("旧消息回退: %v %v", a, b)
	}
}

// turnHeaderFinishedMessages 造一个已结束、可折叠的回合：用户提问 + 思考 + 一条命令 + 最终答复。
// 时间戳让折叠头显示「用时 12s」，展开体尾部才能核对完成详情。
func turnHeaderFinishedMessages() []Message {
	start := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	at := func(offset time.Duration) string { return start.Add(offset).Format(time.RFC3339) }
	return []Message{
		{ID: "u1", Role: "user", Text: "跑一下构建", TurnIndex: 1, IsFinal: true, CreatedAt: at(0)},
		{ID: "r1", Role: "assistant", Kind: "reasoning", Text: "先确认仓库\n再看构建脚本", TurnIndex: 1, CreatedAt: at(2 * time.Second)},
		{ID: "t1", Role: "tool", Text: `Bash {"command":"go build ./..."}`, TurnIndex: 1, ToolStatus: "completed", CreatedAt: at(4 * time.Second)},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "构建通过", TurnIndex: 1, IsFinal: true, CreatedAt: at(12 * time.Second)},
	}
}

// newTurnHeaderUsageTester 渲染带单轮用量的会话：有用量才会画完成行或折叠头尾部的详情行。
func newTurnHeaderUsageTester(t *testing.T, msgs []Message, turns map[int]TokenUsage, ready string) *ui.Tester {
	t.Helper()
	fake := NewFakeClient()
	fake.messages["t1"] = msgs
	fake.SetThreadUsage("t1", ThreadUsage{Turns: turns})
	tt := ui.NewTester(Root(fake), 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText(ready) })
	return tt
}

// newTurnHeaderRunningTester 渲染一个仍在运行的会话，用来核对运行态回合头。
func newTurnHeaderRunningTester(t *testing.T, msgs []Message, ready string) *ui.Tester {
	t.Helper()
	running := Thread{ID: "t1", ProjectID: "p1", Title: "运行中", Provider: "claude", UpdatedAt: time.Now(), IsRunning: true}
	fake := NewFakeClient()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "Proj1", Path: "/workspace"}},
		map[string][]Thread{"p1": {running}},
	)
	fake.messages["t1"] = msgs
	tt := ui.NewTester(Root(fake), 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText(ready) })
	return tt
}

// TestReasoningHeaderTitle 验证思考行标题的「标题 · 要旨」格式、清洗规则与空正文兜底。
func TestReasoningHeaderTitle(t *testing.T) {
	long := strings.Repeat("要", reasoningHeadlineRunes+5)
	cases := []struct {
		name      string
		text      string
		streaming bool
		want      string
	}{
		{"首行做要旨", "先确认仓库结构\n再看构建脚本", false, "思考过程 · 先确认仓库结构"},
		{"流式取最后一行", "先确认仓库结构\n再看构建脚本", true, "思考中… · 再看构建脚本"},
		{"清洗加粗与反引号", "**先确认 `go.mod`**", false, "思考过程 · 先确认 go.mod"},
		{"剥掉引用与列表序号", "> 1. 先看仓库", false, "思考过程 · 先看仓库"},
		{"小数不当列表", "1.5 秒后重试", false, "思考过程 · 1.5 秒后重试"},
		{"过长截断", long, false, "思考过程 · " + string([]rune(long)[:reasoningHeadlineRunes]) + "…"},
		{"空正文只留标题", " \n\t", false, "思考过程"},
		{"空正文流式只留标题", "", true, "思考中…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reasoningHeaderTitle(tc.text, tc.streaming); got != tc.want {
				t.Fatalf("reasoningHeaderTitle(%q, %v) = %q, want %q", tc.text, tc.streaming, got, tc.want)
			}
		})
	}
}

// TestTimelineTurnHeader_ReasoningShowsGist 验证思考结束后标题显示清洗后的首行要旨，正文只在展开后出现。
func TestTimelineTurnHeader_ReasoningShowsGist(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "你好", TurnIndex: 1, IsFinal: true},
		{ID: "r1", Role: "assistant", Kind: "reasoning", Text: "**先确认 go.mod**\n完整正文-REASONING", TurnIndex: 1, IsFinal: true},
	}
	tt := newTimelineTester(t, msgs, "思考过程 · 先确认 go.mod")
	if tt.HasText("完整正文-REASONING") {
		t.Fatalf("未展开时不应显示思考正文: %v", tt.Texts())
	}
	clickText(t, tt, "思考过程 · 先确认 go.mod")
	if !tt.HasText("完整正文-REASONING") {
		t.Fatalf("展开后应看到完整思考正文: %v", tt.Texts())
	}
}

// TestToolGroupHeaderSummaryRunningCommands 验证工具组头在运行中追加正在运行的命令片段，
// 并行时只显示最后一条并补「等 N 条」，跑完后自动恢复成「用时 …」。
func TestToolGroupHeaderSummaryRunningCommands(t *testing.T) {
	one := []Message{
		{ID: "t1", Role: "tool", Text: `Bash {"command":"npm test"}`, ToolStatus: "running"},
	}
	got := toolGroupHeaderSummaryText(countToolGroupCategories(one), true, "", one)
	if !strings.Contains(got, "· 正在运行 npm test") {
		t.Fatalf("单条运行命令应追加命令片段，实际 %q", got)
	}
	if strings.Contains(got, "等 ") {
		t.Fatalf("只有一条命令时不应显示并行条数，实际 %q", got)
	}

	parallel := []Message{
		{ID: "t1", Role: "tool", Text: `Bash {"command":"npm test"}`, ToolStatus: "running"},
		{ID: "t2", Role: "tool", Text: `Bash {"command":"go build"}`, ToolStatus: "running"},
	}
	got = toolGroupHeaderSummaryText(countToolGroupCategories(parallel), true, "", parallel)
	if !strings.Contains(got, "· 正在运行 go build") {
		t.Fatalf("并行时应显示最后一条运行命令，实际 %q", got)
	}
	if !strings.Contains(got, "等 2 条") {
		t.Fatalf("并行多条时应补「等 2 条」，实际 %q", got)
	}
	if strings.Contains(got, "npm test") {
		t.Fatalf("并行时不应把前一条命令也塞进组头，实际 %q", got)
	}

	done := []Message{
		{ID: "t1", Role: "tool", Text: `Bash {"command":"npm test"}`, ToolStatus: "completed"},
	}
	got = toolGroupHeaderSummaryText(countToolGroupCategories(done), false, "3.2s", done)
	if strings.Contains(got, "正在运行") {
		t.Fatalf("命令跑完后组头不应再显示运行提示，实际 %q", got)
	}
	if want := "用时 3.2s · 运行 1 条命令"; got != want {
		t.Fatalf("跑完后组头 = %q，want %q", got, want)
	}
}

// TestTimelineTurnHeader_FinishedTurnKeepsSingleHeader 验证结束态只有一行可折叠的「用时」头：
// 页面不再有第二条「已完成」行，展开后详情行出现在折叠体尾部。
func TestTimelineTurnHeader_FinishedTurnKeepsSingleHeader(t *testing.T) {
	msgs := turnHeaderFinishedMessages()
	usage := map[int]TokenUsage{1: {Input: 1200, Output: 356, DurationMs: 12000}}
	tt := newTurnHeaderUsageTester(t, msgs, usage, "用时 12s")

	if tt.HasText("已完成 ·") {
		t.Fatalf("折叠态不应再出现第二条完成行: %v", tt.Texts())
	}
	if tt.HasText("已工作") {
		t.Fatalf("文案应统一为「用时」，不应残留「已工作」: %v", tt.Texts())
	}
	if !tt.HasText("构建通过") {
		t.Fatalf("最终答复应留在折叠头之外: %v", tt.Texts())
	}

	clickText(t, tt, "用时 12s")
	if !tt.HasText("已完成 ·") {
		t.Fatalf("展开后应在折叠体尾部显示完成详情: %v", tt.Texts())
	}
	if !tt.HasText("输出 356") {
		t.Fatalf("展开后的详情行不应丢 token 信息: %v", tt.Texts())
	}
	if !tt.HasText("go build") {
		t.Fatalf("展开后应看到被收起的工作过程: %v", tt.Texts())
	}
}

// TestTimelineTurnHeader_TooltipKeepsDetails 验证组头不再显示的计数、唤醒原因与 token/时间明细都在 Tooltip 里。
func TestTimelineTurnHeader_TooltipKeepsDetails(t *testing.T) {
	msgs := turnHeaderFinishedMessages()
	usage := TokenUsage{Input: 1200, Output: 356, DurationMs: 12000}
	s := &chatState{messages: msgs}
	items := applyTurnWorkFold(s, "t1", buildTimelineItems(msgs))
	var fold timelineItem
	for _, it := range items {
		if it.kind == timelineItemTurnWork {
			fold = it
		}
	}
	if fold.id == "" {
		t.Fatalf("已结束回合应折叠成一项: %v", timelineItemIDs(items))
	}
	counts := countToolGroupCategories(turnWorkSummaryTools(s, fold.tools))
	tooltip := turnWorkHeaderTooltip(s, fold, counts, usage, true, time.Now())
	for _, want := range []string{"思考 1 次", "运行 1 条命令", "输入 1.2k · 输出 356 tokens", "开始 ", "结束 "} {
		if !strings.Contains(tooltip, want) {
			t.Fatalf("折叠头 Tooltip 缺少 %q:\n%s", want, tooltip)
		}
	}

	now := time.Date(2026, 11, 3, 10, 0, 30, 0, time.UTC)
	running := timelineItem{workRunning: true, workStartedAt: now.Add(-16 * time.Second)}
	if got := turnWorkHeaderTooltip(s, running, counts, usage, true, now); got != "本回合已处理 16s" {
		t.Fatalf("运行态 Tooltip = %q, want 本回合已处理 16s", got)
	}
	if got := turnHeaderRunningLabel(16*time.Second + 900*time.Millisecond); got != "已处理 16s" {
		t.Fatalf("运行计时只取整秒，got %q", got)
	}
}

// TestTimelineTurnHeader_RunningTurnYieldsShimmerToTool 验证运行态回合头只数秒：
// 本回合有工具胶囊在扫光时头不扫光，避免同一屏出现两处同步高光。
func TestTimelineTurnHeader_RunningTurnYieldsShimmerToTool(t *testing.T) {
	start := time.Now().Add(-16 * time.Second)
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "跑一下构建", TurnIndex: 1, IsFinal: true, CreatedAt: start.Format(time.RFC3339)},
		{ID: "t1", Role: "tool", Text: `Bash {"command":"go build ./..."}`, TurnIndex: 1, ToolStatus: "running"},
	}
	tt := newTurnHeaderRunningTester(t, msgs, "已处理")

	if tt.HasText("用时") {
		t.Fatalf("运行中的回合不应出现可折叠的用时头: %v", tt.Texts())
	}
	matched := false
	re := regexp.MustCompile(`已处理 \d+s`)
	for _, text := range tt.Texts() {
		if re.MatchString(text) {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("运行态头应显示整秒计时: %v", tt.Texts())
	}
	if _, ok := tt.Find(turnHeaderShimmerLabel); ok {
		t.Fatalf("本回合已有工具在扫光，回合头不应再扫光: %v", tt.Texts())
	}
	if _, ok := tt.Find(toolShimmerBandLabel); !ok {
		t.Fatalf("运行中的工具胶囊应保留扫光: %v", tt.Texts())
	}
}

// TestTimelineTurnHeader_RunningTurnShimmersWithoutTool 验证没有工具在跑时运行态头自己扫光。
func TestTimelineTurnHeader_RunningTurnShimmersWithoutTool(t *testing.T) {
	start := time.Now().Add(-5 * time.Second)
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "想一想", TurnIndex: 1, IsFinal: true, CreatedAt: start.Format(time.RFC3339)},
		{ID: "r1", Role: "assistant", Kind: "reasoning", Text: "先列出可能的方案", TurnIndex: 1},
	}
	tt := newTurnHeaderRunningTester(t, msgs, "已处理")

	if _, ok := tt.Find(turnHeaderShimmerLabel); !ok {
		t.Fatalf("没有工具在跑时运行态头应扫光: %v", tt.Texts())
	}
}

// TestTimelineTurnHeader_PlainAnswerTurnKeepsFinishedLine 验证纯答复回合没有可折叠过程，
// 仍保留原来的「已完成 · …」行，而不是硬造一个折叠头。
func TestTimelineTurnHeader_PlainAnswerTurnKeepsFinishedLine(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "你好", TurnIndex: 1, IsFinal: true},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "你好呀", TurnIndex: 1, IsFinal: true},
	}
	tt := newTurnHeaderUsageTester(t, msgs, map[int]TokenUsage{1: {Input: 1200, Output: 356}}, "已完成 · 输出 356")

	if tt.HasText("用时") {
		t.Fatalf("纯答复回合不应出现折叠用时头: %v", tt.Texts())
	}
	if !tt.HasText("已完成 · 输出 356") {
		t.Fatalf("纯答复回合应保留完成行: %v", tt.Texts())
	}
}

// TestTimelineTurnHeader_AutoCollapseFollowsSetting 验证历史回合的默认折叠值沿用自动折叠设置，
// 没有另改默认值：开启时收起，关闭时展开并显示尾部详情。
func TestTimelineTurnHeader_AutoCollapseFollowsSetting(t *testing.T) {
	for _, tc := range []struct {
		name         string
		autoCollapse bool
		wantFooter   bool
	}{
		{"默认自动折叠时收起", true, false},
		{"关闭自动折叠时展开", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := turnHeaderFinishedMessages()
			s := newAutoCollapseTestState(t, tc.autoCollapse)
			s.messages = msgs
			items := applyTurnWorkFold(s, "t1", buildTimelineItems(msgs))
			var fold timelineItem
			for _, it := range items {
				if it.kind == timelineItemTurnWork {
					fold = it
				}
			}
			if fold.id == "" {
				t.Fatalf("已结束回合应折叠成一项: %v", timelineItemIDs(items))
			}
			usage := TokenUsage{Input: 1200, Output: 356, DurationMs: 12000}
			tt := ui.NewTester(func(c *ui.Context) {
				renderTimelineItem(c, s, fold, "", "", false, true, usage, 0)
			}, 800, 600)
			for i := 0; i < 4; i++ {
				tt.Frame()
			}
			if !tt.HasText("用时 12s") {
				t.Fatalf("结束态组头应显示用时: %v", tt.Texts())
			}
			if got := tt.HasText("已完成 ·"); got != tc.wantFooter {
				t.Fatalf("自动折叠=%v 时尾部详情可见性 = %v，want %v: %v", tc.autoCollapse, got, tc.wantFooter, tt.Texts())
			}
		})
	}
}

// TestTimelineTurnHeader_IncrementalRunningToFinished 验证增量重建在「运行中 → 追加工具 → 回合结束」的
// 每一步都与全量构建完全一致：运行态头只有一处，结束后换成唯一一个可折叠的用时头。
func TestTimelineTurnHeader_IncrementalRunningToFinished(t *testing.T) {
	threadID := "t1"
	start := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "跑一下构建", TurnIndex: 1, IsFinal: true, CreatedAt: start.Format(time.RFC3339)},
		{ID: "r1", Role: "assistant", Kind: "reasoning", Text: "先确认仓库结构", TurnIndex: 1},
	}
	s := &chatState{
		threads:  []Thread{{ID: threadID, ProjectID: "p1", IsRunning: true}},
		messages: msgs,
	}
	helperBuildAndAssertIncremental(t, s, threadID, msgs, nil, "运行中首帧")

	countRunningHeader := func() int {
		n := 0
		for _, it := range s.timelineCacheItems {
			if it.kind == timelineItemTurnWork && it.workRunning {
				n++
			}
		}
		return n
	}
	if got := countRunningHeader(); got != 1 {
		t.Fatalf("运行中的回合应有且只有一个运行态头，got %d: %+v", got, s.timelineCacheItems)
	}

	// 同一回合追加一条仍在运行的命令：运行态头应把新命令一起收进去，仍然只有一处。
	msgs = append(msgs, Message{ID: "t1", Role: "tool", Text: `Bash {"command":"go build ./..."}`, TurnIndex: 1, ToolStatus: "running"})
	s.messages = msgs
	helperBuildAndAssertIncremental(t, s, threadID, msgs, nil, "运行中追加命令")
	if got := countRunningHeader(); got != 1 {
		t.Fatalf("追加命令后运行态头仍应只有一处，got %d: %+v", got, s.timelineCacheItems)
	}

	// 回合结束并落下最终答复：运行态头换成可折叠的用时头，收尾答复打上 workHeaderID 压掉第二条完成行。
	s.mu.Lock()
	s.threads[0].IsRunning = false
	s.mu.Unlock()
	msgs = append(msgs, Message{ID: "a1", Role: "assistant", Kind: "text", Text: "构建通过", TurnIndex: 1, IsFinal: true, CreatedAt: start.Add(12 * time.Second).Format(time.RFC3339)})
	s.messages = msgs
	helperBuildAndAssertIncremental(t, s, threadID, msgs, nil, "回合结束")

	folds := 0
	var fold timelineItem
	headerID := ""
	for _, it := range s.timelineCacheItems {
		if it.kind == timelineItemTurnWork {
			folds++
			fold = it
		}
		if it.workHeaderID != "" {
			headerID = it.workHeaderID
		}
	}
	if folds != 1 || fold.workRunning {
		t.Fatalf("结束后应只剩一个已结束的折叠头，got folds=%d item=%+v", folds, fold)
	}
	if headerID != fold.id {
		t.Fatalf("收尾答复应挂上折叠头 ID %q，got %q", fold.id, headerID)
	}
	if got := turnWorkHeaderText(countToolGroupCategories(fold.tools), fold.workElapsed); got != "用时 12s" {
		t.Fatalf("增量重建后的组头文案 = %q，want 用时 12s", got)
	}
}

func turnHeaderSplitMessages46s() []Message {
	msgs := turnWorkSplitToolMessages()[:6]
	start := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	msgs[0].CreatedAt = start.Format(time.RFC3339)
	msgs[5].CreatedAt = start.Add(46 * time.Second).Format(time.RFC3339)
	return msgs
}

func assertNoRunningTurnHeader(t *testing.T, items []timelineItem) {
	t.Helper()
	for _, item := range items {
		if item.workRunning {
			t.Fatalf("不应残留运行态头: %+v", item)
		}
		assertNoRunningTurnHeader(t, item.workItems)
	}
}

func TestTimelineTurnHeader_IncrementalFinishedAfterIntermediateTextWithoutMessageChanges(t *testing.T) {
	for _, tc := range []struct {
		name      string
		swarm     bool
		reasoning bool
		batch     bool
	}{
		{"线程结束", false, true, false},
		{"子代理结束", true, true, false},
		{"纯命令", false, false, false},
		{"答复与空闲同时到达", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := turnHeaderSplitMessages46s()
			if !tc.reasoning {
				msgs = append(msgs[:1:1], msgs[2:]...)
			}
			s := &chatState{threads: []Thread{{ID: "t1", IsRunning: !tc.swarm, SwarmBusy: tc.swarm}}}
			build := func(n int, label string) {
				s.messages = msgs[:n]
				s.messagesRev++
				helperBuildAndAssertIncremental(t, s, "t1", s.messages, nil, label)
			}
			build(len(msgs)-3, "首段工具")
			build(len(msgs)-1, "中途正文和第二段工具")
			if !tc.batch {
				build(len(msgs), "运行中最终答复")
				var running, middle, secondTool, answer bool
				for _, item := range s.timelineCacheItems {
					running = running || item.workRunning
					middle = middle || item.msg.ID == "amid"
					secondTool = secondTool || item.msg.ID == "t2" || containsMessageID(item.tools, "t2")
					answer = answer || (item.msg.ID == "a1" && item.workHeaderID == "")
				}
				if !running || !middle || !secondTool || !answer {
					t.Fatalf("运行态条目不完整: %+v", s.timelineCacheItems)
				}
			}
			s.threads[0].IsRunning = false
			s.threads[0].SwarmBusy = false
			build(len(msgs), "回合结束")
			assertNoRunningTurnHeader(t, s.timelineCacheItems)
			var fold timelineItem
			var answer timelineItem
			folds := 0
			for _, item := range s.timelineCacheItems {
				if item.kind == timelineItemTurnWork {
					folds++
					fold = item
				}
				if item.msg.ID == "a1" {
					answer = item
				}
			}
			middle := false
			for _, item := range fold.workItems {
				middle = middle || item.msg.ID == "amid"
			}
			if folds != 1 || !middle || !containsMessageID(fold.tools, "t1") || !containsMessageID(fold.tools, "t2") || answer.workHeaderID != fold.id || answer.msg.ID != "a1" {
				t.Fatalf("结束态折叠结果不完整: %+v", s.timelineCacheItems)
			}
			if got := turnWorkHeaderText(countToolGroupCategories(fold.tools), fold.workElapsed); got != "用时 46s" {
				t.Fatalf("结束态组头 = %q，want 用时 46s", got)
			}
		})
	}
}

func TestTimelineTurnHeader_StatusOnlyFinishReplacesCachedRunningHeader(t *testing.T) {
	for _, bumpRev := range []bool{false, true} {
		name := "仅状态变化"
		if bumpRev {
			name = "空消息通知"
		}
		t.Run(name, func(t *testing.T) {
			fake := NewFakeClient()
			fake.SetProjectsAndThreads(
				[]Project{{ID: "p1", Name: "Proj1", Path: "/workspace"}},
				map[string][]Thread{"p1": {{ID: "t1", ProjectID: "p1", Title: "运行中", Provider: "claude", UpdatedAt: time.Now(), IsRunning: true}}},
			)
			fake.messages["t1"] = turnHeaderSplitMessages46s()
			fake.SetThreadUsage("t1", ThreadUsage{Turns: map[int]TokenUsage{1: {Input: 1200, Output: 356, DurationMs: 46000}}})
			state, view := buildRoot(fake)
			settings := &SettingsState{}
			autoCollapse := true
			settings.cfg.Session.AutoCollapseReasoning = &autoCollapse
			setChatSettings(state, settings)
			t.Cleanup(func() { setChatSettings(state, nil) })
			tt := ui.NewTester(view, 1000, 700)
			waitFor(t, tt, func() bool { return tt.HasText("已处理") && tt.HasText("FOLD-ANSWER-ONE") })
			state.mu.Lock()
			for i := range state.threads {
				if state.threads[i].ID == "t1" {
					state.threads[i].IsRunning = false
				}
			}
			if bumpRev {
				state.messagesRev++
			}
			state.mu.Unlock()
			state.invalidate()
			waitFor(t, tt, func() bool { return tt.HasText("用时 46s") })
			// 结束后折叠体从运行时的强制展开回收起，等收起动画播完再断言。
			tt.Advance(motionUnfold + 40*time.Millisecond)
			if tt.HasText("已处理") || !tt.HasText("FOLD-ANSWER-ONE") || tt.HasText("已完成 ·") {
				t.Fatalf("结束态显示不正确: %v", tt.Texts())
			}
			tt.Advance(2 * time.Second)
			if !tt.HasText("用时 46s") || tt.HasText("已处理") {
				t.Fatalf("结束态计时不应继续增长: %v", tt.Texts())
			}
			assertNoRunningTurnHeader(t, state.timelineCacheItems)
			assertNoRunningTurnHeader(t, state.timelineAssembledItems)
			clickText(t, tt, "用时 46s")
			for _, text := range []string{"MID-STATUS-TEXT", "git status", "go build ./...", "已完成 ·", "FOLD-ANSWER-ONE"} {
				if !tt.HasText(text) {
					t.Fatalf("展开后缺少 %q: %v", text, tt.Texts())
				}
			}
		})
	}
}

// countTurnRunningHeaders 数缓存条目里的运行态计时头个数。
func countTurnRunningHeaders(items []timelineItem) int {
	n := 0
	for _, it := range items {
		if it.kind == timelineItemTurnWork && it.workRunning {
			n++
		}
	}
	return n
}

// TestTimelineTurnHeader_IncrementalBodyFirstKeepsSingleTimer 验证先正文后工具的回合：
// 仅用户消息与正文先行时都有纯计时头；追加正文后的工具与子代理汇报时头仍唯一，
// 正文、工具与汇报按原顺序留在头外；回合结束后没有残留运行头，结束态沿用既有行为。
func TestTimelineTurnHeader_IncrementalBodyFirstKeepsSingleTimer(t *testing.T) {
	start := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	msg := func(id, role, kind, text string, offset time.Duration) Message {
		return Message{ID: id, Role: role, Kind: kind, Text: text, TurnIndex: 1, CreatedAt: start.Add(offset).Format(time.RFC3339)}
	}
	msgs := []Message{msg("u1", "user", "", "先答复再派工", 0)}
	msgs[0].IsFinal = true
	s := &chatState{threads: []Thread{{ID: "t1", IsRunning: true}}, messages: msgs}
	build := func(n int, label string) {
		s.messages = msgs[:n]
		helperBuildAndAssertIncremental(t, s, "t1", s.messages, nil, label)
	}
	assertEmptyHeader := func(label string) {
		t.Helper()
		if got := countTurnRunningHeaders(s.timelineCacheItems); got != 1 {
			t.Fatalf("%s：应有且只有一个运行态计时头，got %d: %+v", label, got, s.timelineCacheItems)
		}
		head := s.timelineCacheItems[1]
		if len(head.workItems) != 0 || len(head.tools) != 0 {
			t.Fatalf("%s：无活动时折叠区应为空: %+v", label, head)
		}
		if head.msgIndex != 0 || head.msgLastIndex != 0 {
			t.Fatalf("%s：空折叠区下标应只覆盖用户消息，got [%d,%d]", label, head.msgIndex, head.msgLastIndex)
		}
		if !head.workStartedAt.Equal(start) {
			t.Fatalf("%s：计时起点应为用户消息时间，got %v", label, head.workStartedAt)
		}
	}

	build(1, "仅用户消息")
	assertEmptyHeader("仅用户消息")
	if len(s.timelineCacheItems) != 2 {
		t.Fatalf("仅用户消息时应只有用户行与计时头: %+v", s.timelineCacheItems)
	}

	msgs = append(msgs, msg("a1", "assistant", "text", "先看结果", time.Second))
	msgs[1].IsFinal = true
	build(2, "首条正文紧跟用户消息")
	assertEmptyHeader("正文先行")
	if len(s.timelineCacheItems) != 3 || s.timelineCacheItems[2].msg.ID != "a1" {
		t.Fatalf("正文应紧跟计时头之后: %+v", s.timelineCacheItems)
	}

	// 正文之后追加运行中的工具与子代理汇报：都留在头外，顺序不变。
	msgs = append(msgs, msg("t1", "tool", "", `Bash {"command":"go build ./..."}`, 2*time.Second))
	msgs[2].ToolStatus = "running"
	msgs = append(msgs, msg("rpt", "user", "", "[编排] 子代理汇报：Ives 已开工", 3*time.Second))
	build(4, "正文后追加工具与汇报")
	assertEmptyHeader("正文后追加活动")
	var order []string
	for _, it := range s.timelineCacheItems {
		if it.kind == timelineItemTurnWork {
			order = append(order, "header")
			continue
		}
		order = append(order, it.msg.ID)
	}
	if want := []string{"u1", "header", "a1", "t1", "rpt"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("正文、工具与汇报顺序不对: got %v want %v", order, want)
	}

	// 仅状态改变转结束：没有可折叠过程，沿用纯答复回合的完成行，不造折叠头。
	s.threads[0].IsRunning = false
	build(4, "回合结束")
	assertNoRunningTurnHeader(t, s.timelineCacheItems)
	for _, it := range s.timelineCacheItems {
		if it.kind == timelineItemTurnWork {
			t.Fatalf("无前置活动的回合结束后不应硬造折叠头: %+v", it)
		}
	}
}

// TestTimelineTurnHeader_EmptyHeaderKeepsNonActivityNotice 验证空折叠区不吞非活动提示：
// 正文前后的错误提示与后续工具都留在头外可见，不会被收进折叠区。
func TestTimelineTurnHeader_EmptyHeaderKeepsNonActivityNotice(t *testing.T) {
	start := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	t.Run("before_body", func(t *testing.T) {
		msgs := []Message{
			{ID: "u1", Role: "user", Text: "继续", TurnIndex: 1, IsFinal: true, CreatedAt: start.Format(time.RFC3339)},
			{ID: "e1", Role: "error", Text: "请求失败，请重试", TurnIndex: 1},
		}
		s := &chatState{threads: []Thread{{ID: "t1", IsRunning: true}}, messages: msgs}
		helperBuildAndAssertIncremental(t, s, "t1", msgs, nil, "未出正文的提示")
		if len(s.timelineCacheItems) != 3 {
			t.Fatalf("正文前应只有用户行、计时头与错误提示: %+v", s.timelineCacheItems)
		}
		if s.timelineCacheItems[0].msg.ID != "u1" || s.timelineCacheItems[2].msg.ID != "e1" {
			t.Fatalf("错误提示应独立留在计时头之后: %+v", s.timelineCacheItems)
		}
		head := s.timelineCacheItems[1]
		if head.kind != timelineItemTurnWork || !head.workRunning {
			t.Fatalf("用户消息后应有运行态计时头: %+v", head)
		}
		if len(head.workItems) != 0 || len(head.tools) != 0 {
			t.Fatalf("正文前无活动时折叠区应为空: %+v", head)
		}
		if head.msgIndex != 0 || head.msgLastIndex != 0 {
			t.Fatalf("空折叠区下标应只覆盖用户消息，got [%d,%d]", head.msgIndex, head.msgLastIndex)
		}
	})

	msgs := []Message{
		{ID: "u1", Role: "user", Text: "继续", TurnIndex: 1, IsFinal: true, CreatedAt: start.Format(time.RFC3339)},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "先试一次", TurnIndex: 1},
		{ID: "e1", Role: "error", Text: "请求失败，请重试", TurnIndex: 1},
	}
	s := &chatState{threads: []Thread{{ID: "t1", IsRunning: true}}, messages: msgs}
	helperBuildAndAssertIncremental(t, s, "t1", msgs, nil, "提示在运行头之后")
	if got := countTurnRunningHeaders(s.timelineCacheItems); got != 1 {
		t.Fatalf("应有且只有一个运行态头，got %d: %+v", got, s.timelineCacheItems)
	}
	if len(s.timelineCacheItems[1].workItems) != 0 {
		t.Fatalf("无前置活动时折叠区应为空: %+v", s.timelineCacheItems[1])
	}

	msgs = append(msgs, Message{ID: "t1", Role: "tool", Text: `Bash {"command":"go build ./..."}`, TurnIndex: 1, ToolStatus: "running"})
	s.messages = msgs
	helperBuildAndAssertIncremental(t, s, "t1", msgs, nil, "提示后追加活动")
	if got := countTurnRunningHeaders(s.timelineCacheItems); got != 1 {
		t.Fatalf("追加活动后头仍应唯一，got %d: %+v", got, s.timelineCacheItems)
	}
	var order []string
	for _, it := range s.timelineCacheItems {
		if it.kind == timelineItemTurnWork {
			order = append(order, "header")
			continue
		}
		order = append(order, it.msg.ID)
	}
	if want := []string{"u1", "header", "a1", "e1", "t1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("提示不能被活动收进折叠区: got %v want %v", order, want)
	}
}

// TestTimelineTurnHeader_StatusOnlyFinishUserOnlyTimer 验证仅用户消息的纯计时头在只改运行状态、
// 没有新消息时也能正确转结束：无残留运行头，也不硬造结束态折叠头。
func TestTimelineTurnHeader_StatusOnlyFinishUserOnlyTimer(t *testing.T) {
	start := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	fake := NewFakeClient()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "Proj1", Path: "/workspace"}},
		map[string][]Thread{"p1": {{ID: "t1", ProjectID: "p1", Title: "运行中", Provider: "claude", UpdatedAt: time.Now(), IsRunning: true}}},
	)
	fake.messages["t1"] = []Message{{ID: "u1", Role: "user", Text: "只有一问", TurnIndex: 1, IsFinal: true, CreatedAt: start.Format(time.RFC3339)}}
	state, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("已处理") && tt.HasText("只有一问") })
	state.mu.Lock()
	state.threads[0].IsRunning = false
	state.mu.Unlock()
	state.invalidate()
	waitFor(t, tt, func() bool { return !tt.HasText("已处理") })
	tt.Advance(2 * time.Second)
	if tt.HasText("已处理") || tt.HasText("用时") || !tt.HasText("只有一问") {
		t.Fatalf("仅状态转结束后显示不正确: %v", tt.Texts())
	}
	assertNoRunningTurnHeader(t, state.timelineCacheItems)
	assertNoRunningTurnHeader(t, state.timelineAssembledItems)
}

// TestTimelineBackgroundRow_ShowsLingeringCommand 验证回合结束后仍在跑的命令只留下工具行，
// 不再在时间线末尾冒出后台任务卡片——后台项改由输入区上方的实时胶囊展示，点开胶囊才渲染浮层。
// 胶囊正文变成「最早任务名 · 计时」，按住 Label 定位更稳。
func TestTimelineBackgroundRow_ShowsLingeringCommand(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "跑一下构建", TurnIndex: 1, IsFinal: true},
		{ID: "t1", Role: "tool", Text: `Bash {"command":"npm test"}`, TurnIndex: 1, ToolStatus: "running"},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "构建通过", TurnIndex: 1, IsFinal: true},
	}
	tt := newTimelineTester(t, msgs, "构建通过")

	// 后台任务胶囊仍按「后台任务 · 1 个运行中」的 Label 定位，正文换成了实时状态文案。
	if _, ok := tt.Find("后台任务 · 1 个运行中"); !ok {
		t.Fatalf("仍在跑的命令应计入输入区后台任务胶囊，当前文本 %v", tt.Texts())
	}
	// 浮层默认关闭：其表头计数「1 个运行中」与旧卡片都不应出现在可见文本里。
	if hasExactText(tt, "1 个运行中") {
		t.Fatalf("时间线不应再出现后台任务卡片或浮层: %v", tt.Texts())
	}
}
