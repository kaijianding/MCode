package mcodeui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// turnWorkSplitToolMessages 造一个「工具调用被中途正文切断」的已结束回合：
// 思考与第一条命令之后插入一段中途正文，再跑第二条命令，最后才是最终答复。
// 这种回合在折叠前会被切成两个工具组，折叠后必须合成同一个折叠项。
func turnWorkSplitToolMessages() []Message {
	return []Message{
		{ID: "u1", Role: "user", Text: "先看看仓库再改", TurnIndex: 1, IsFinal: true},
		{ID: "r1", Role: "assistant", Kind: "reasoning", Text: "先确认仓库结构\n再看构建脚本\n最后核对测试入口", TurnIndex: 1},
		{ID: "t1", Role: "tool", Text: `Bash {"command":"git status"}`, TurnIndex: 1, ToolStatus: "completed"},
		{ID: "amid", Role: "assistant", Kind: "text", Text: "MID-STATUS-TEXT", TurnIndex: 1},
		{ID: "t2", Role: "tool", Text: `Bash {"command":"go build ./..."}`, TurnIndex: 1, ToolStatus: "completed"},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "FOLD-ANSWER-ONE", TurnIndex: 1, IsFinal: true},
		{ID: "u2", Role: "user", Text: "再跑一遍测试", TurnIndex: 2, IsFinal: true},
		{ID: "a2", Role: "assistant", Kind: "text", Text: "FOLD-ANSWER-TWO", TurnIndex: 2, IsFinal: true},
	}
}

// turnWorkFoldHeaderText 是第一回合折叠项的组头：耗时拿不到时只写「过程」，
// 计数与唤醒原因移到 Tooltip，不再占组头。
const turnWorkFoldHeaderText = "过程"

func TestTimelineRows_TurnWorkFoldReplacesReusedRunningHeaderWithIntermediateText(t *testing.T) {
	msgs := turnWorkSplitToolMessages()[:6]
	s := &chatState{threads: []Thread{{ID: "t1", IsRunning: true}}, messages: msgs}
	running := applyTurnWorkFold(s, "t1", buildTimelineItems(msgs))
	s.threads[0].IsRunning = false
	got := applyTurnWorkFold(s, "t1", running)
	want := applyTurnWorkFold(s, "t1", buildTimelineItems(msgs))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("复用运行态头后的折叠与全量构建不同:\ngot: %+v\nwant: %+v", got, want)
	}
}

// TestTimelineRows_TurnWorkFoldCollapsesFinishedTurn 验证一轮里的思考与工具调用被收进一个折叠项，
// 最终正文留在折叠项之外，点开后折叠区内的内容重新出现。
func TestTimelineRows_TurnWorkFoldCollapsesFinishedTurn(t *testing.T) {
	tt := newTimelineTester(t, turnWorkSplitToolMessages(), "FOLD-ANSWER-ONE")

	if !tt.HasText(turnWorkFoldHeaderText) {
		t.Fatalf("缺少回合折叠组头 %q，当前文本 %v", turnWorkFoldHeaderText, tt.Texts())
	}
	if !tt.HasText("FOLD-ANSWER-ONE") {
		t.Fatalf("最终正文应当留在折叠项之外: %v", tt.Texts())
	}
	if tt.HasText("MID-STATUS-TEXT") || tt.HasText("git status") {
		t.Fatalf("折叠区内的工作过程默认应收起: %v", tt.Texts())
	}

	clickText(t, tt, turnWorkFoldHeaderText)

	if !tt.HasText("git status") || !tt.HasText("go build ./...") {
		t.Fatalf("展开后应看到折叠区内的命令: %v", tt.Texts())
	}
	if !tt.HasText("MID-STATUS-TEXT") {
		t.Fatalf("展开后应看到被切断的中途正文: %v", tt.Texts())
	}

	// 再点一次收起，回到只剩组头
	clickText(t, tt, turnWorkFoldHeaderText)
	if tt.HasText("git status") {
		t.Fatalf("再次点击应收起折叠区: %v", tt.Texts())
	}
}

// TestTimelineRows_TurnWorkFoldKeepsSplitToolsInOneFold 验证被中途正文切断的工具调用归入同一个折叠项，
// 而不是各自成折叠项。
func TestTimelineRows_TurnWorkFoldKeepsSplitToolsInOneFold(t *testing.T) {
	msgs := turnWorkSplitToolMessages()
	items := buildTimelineItems(msgs)
	if len(items) < 4 {
		t.Fatalf("折叠前应有用户消息、两个工具组、中途正文与最终正文，got %d 项", len(items))
	}
	s := newChatState(NewFakeClient())
	s.mu.Lock()
	s.messages = msgs
	s.mu.Unlock()

	folded := applyTurnWorkFold(s, "t1", items)
	var folds []timelineItem
	for _, it := range folded {
		if it.kind == timelineItemTurnWork {
			folds = append(folds, it)
		}
	}
	if len(folds) != 1 {
		t.Fatalf("第一回合应折叠成 1 项，got %d 项", len(folds))
	}
	fold := folds[0]
	if fold.id != turnWorkFoldID("u1") {
		t.Fatalf("折叠项 key = %q，want %q", fold.id, turnWorkFoldID("u1"))
	}
	groups, commands := 0, 0
	for _, inner := range fold.workItems {
		switch inner.kind {
		case timelineItemToolGroup:
			groups++
		case timelineItemSingle:
			if inner.msg.Role == "tool" {
				commands++
			}
		}
	}
	// 被中途正文切开的两条命令都是活动消息：前一条与思考合成工具组，后一条单独成行，
	// 但两者都必须落进同一个折叠项。
	if groups != 1 || commands != 1 {
		t.Fatalf("折叠项内应有 1 个工具组与 1 条单独命令，got groups=%d commands=%d", groups, commands)
	}
	if !containsMessageID(fold.tools, "t1") || !containsMessageID(fold.tools, "t2") {
		t.Fatalf("被正文切断的两条命令应归入同一折叠项，got %d 条", len(fold.tools))
	}
	if got := turnWorkHeaderText(countToolGroupCategories(fold.tools), fold.workElapsed); got != turnWorkFoldHeaderText {
		t.Fatalf("折叠项组头 = %q，want %q", got, turnWorkFoldHeaderText)
	}
	// 折叠区覆盖 r1 到第二条命令：msgIndex 指向折叠区内第一条消息，msgLastIndex 指向第二条命令。
	if fold.msgIndex != 1 || fold.msgLastIndex != 4 {
		t.Fatalf("折叠项下标 = [%d,%d]，want [1,4]", fold.msgIndex, fold.msgLastIndex)
	}
	// 最终正文仍在折叠项之后，与折叠前位置一致
	answerIdx := -1
	for i, it := range folded {
		if it.kind == timelineItemTurnWork {
			answerIdx = i
			continue
		}
		if answerIdx >= 0 && it.kind == timelineItemSingle && it.msg.ID == "a1" {
			break
		}
		if answerIdx >= 0 && it.kind == timelineItemSingle && it.msg.ID != "a1" {
			t.Fatalf("折叠项与最终正文之间不应插入其它条目: %q", it.id)
		}
	}
}

// TestTimelineRows_TurnWorkFoldUnfoldWithExpandedToolGroup 验证折叠区里的工具组已经处于展开状态时，
// 展开折叠项不会把同一个节拍渲染两次（同一父节点下的 Key 重复会直接 panic）。
// 真实使用中「跳转到折叠区内的消息」会同时打开折叠项与它内部的工具组，两条路径重叠就踩到这个坑。
func TestTimelineRows_TurnWorkFoldUnfoldWithExpandedToolGroup(t *testing.T) {
	fake := NewFakeClient()
	fake.messages["t1"] = turnWorkSplitToolMessages()
	state, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("FOLD-ANSWER-ONE") })

	// 折叠项与它内部的工具组同时展开：工具组 ID 由组内首条活动消息生成。
	state.setToolGroupOpen(turnWorkFoldID("u1"), true)
	state.setToolGroupOpen("toolgroup-r1", true)
	for i := 0; i < 4; i++ {
		tt.Frame()
	}

	// 每个条目只能出现一次，并且命令与中途正文都还在折叠区里。
	for _, want := range []string{"git status", "go build ./...", "MID-STATUS-TEXT"} {
		if !tt.HasText(want) {
			t.Fatalf("展开折叠项后应看到折叠区里的 %q: %v", want, tt.Texts())
		}
	}
}

// TestTimelineRows_TurnWorkFoldKeepsRunningTurn 验证运行中的回合不折叠。
func TestTimelineRows_TurnWorkFoldKeepsRunningTurn(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "跑一下构建", TurnIndex: 1, IsFinal: true},
		{ID: "t1", Role: "tool", Text: `Bash {"command":"go build ./..."}`, TurnIndex: 1, ToolStatus: "completed"},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "FOLD-RUNNING-ANSWER", TurnIndex: 1, IsFinal: true},
	}
	running := Thread{ID: "t1", ProjectID: "p1", Title: "运行中", Provider: "claude", UpdatedAt: time.Now(), IsRunning: true}
	fake := NewFakeClient()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "Proj1", Path: "/workspace"}},
		map[string][]Thread{"p1": {running}},
	)
	fake.messages["t1"] = msgs
	tt := ui.NewTester(Root(fake), 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("go build ./...") })

	if !tt.HasText("已处理") {
		t.Fatalf("运行中的回合应显示运行态回合头: %v", tt.Texts())
	}
	if tt.HasText("用时") {
		t.Fatalf("运行中的回合不应出现可折叠的用时头: %v", tt.Texts())
	}
}

// TestTimelineRows_TurnWorkFoldEmptyHeaderGainsWorkItems 验证仅用户消息的空头在追加前置工具后
// 正确转成有 workItems 的头：重复折叠幂等，增量与全量一致，正文与后续工具仍留在头外。
func TestTimelineRows_TurnWorkFoldEmptyHeaderGainsWorkItems(t *testing.T) {
	start := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	user := Message{ID: "u1", Role: "user", Text: "跑一下构建", TurnIndex: 1, IsFinal: true, CreatedAt: start.Format(time.RFC3339)}
	msgs := []Message{user}
	s := &chatState{threads: []Thread{{ID: "t1", IsRunning: true}}, messages: msgs}
	helperBuildAndAssertIncremental(t, s, "t1", msgs, nil, "仅用户消息")
	if len(s.timelineCacheItems) != 2 || s.timelineCacheItems[1].kind != timelineItemTurnWork ||
		!s.timelineCacheItems[1].workRunning || len(s.timelineCacheItems[1].workItems) != 0 {
		t.Fatalf("仅用户消息时应有空折叠区的纯计时头: %+v", s.timelineCacheItems)
	}

	// 重复 applyTurnWorkFold 幂等：上一帧的头摊平重建后结果不变。
	twice := applyTurnWorkFold(s, "t1", s.timelineCacheItems)
	if !reflect.DeepEqual(twice, s.timelineCacheItems) {
		t.Fatalf("重复折叠应幂等:\ngot:  %+v\nwant: %+v", twice, s.timelineCacheItems)
	}

	// 用户消息之后追加思考与运行中的工具：空头转成收住它们的有 workItems 头。
	msgs = append(msgs,
		Message{ID: "r1", Role: "assistant", Kind: "reasoning", Text: "先确认构建脚本", TurnIndex: 1},
		Message{ID: "t1", Role: "tool", Text: `Bash {"command":"go build ./..."}`, TurnIndex: 1, ToolStatus: "running"},
	)
	s.messages = msgs
	helperBuildAndAssertIncremental(t, s, "t1", msgs, nil, "空头追加前置活动")
	if got := countTurnRunningHeaders(s.timelineCacheItems); got != 1 {
		t.Fatalf("追加活动后头仍应唯一，got %d: %+v", got, s.timelineCacheItems)
	}
	head := s.timelineCacheItems[1]
	if len(head.workItems) == 0 || !containsMessageID(head.tools, "t1") {
		t.Fatalf("前置活动应收进头里: %+v", head)
	}
	if head.msgIndex != 1 || head.msgLastIndex != 2 {
		t.Fatalf("有活动后下标应覆盖折叠区，got [%d,%d]", head.msgIndex, head.msgLastIndex)
	}
	if !reflect.DeepEqual(applyTurnWorkFold(s, "t1", s.timelineCacheItems), s.timelineCacheItems) {
		t.Fatalf("有活动后重复折叠仍应幂等")
	}

	// 再追加正文与其后的工具：都留在头外，顺序不变。
	msgs = append(msgs,
		Message{ID: "a1", Role: "assistant", Kind: "text", Text: "构建进行中", TurnIndex: 1},
		Message{ID: "t2", Role: "tool", Text: `Bash {"command":"go vet ./..."}`, TurnIndex: 1, ToolStatus: "running"},
	)
	s.messages = msgs
	helperBuildAndAssertIncremental(t, s, "t1", msgs, nil, "正文与后续工具")
	if got := countTurnRunningHeaders(s.timelineCacheItems); got != 1 {
		t.Fatalf("正文后头仍应唯一，got %d: %+v", got, s.timelineCacheItems)
	}
	var order []string
	for _, it := range s.timelineCacheItems {
		if it.kind == timelineItemTurnWork {
			order = append(order, "header")
			continue
		}
		order = append(order, it.msg.ID)
	}
	if want := []string{"u1", "header", "a1", "t2"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("正文与后续工具应留在头外且顺序不变: got %v want %v", order, want)
	}
}

// TestTimelineTurnWork_RunningTimerTicksWithoutMessages 验证无新消息、无 rev 改变时，
// 仅靠 fake clock 推进，运行态头的计时文字与 Tooltip 逐秒增长；ReduceMotion 开关两种状态都读秒。
// 时间戳按 Tester 时钟对齐到整秒，避免帧格边界漂移；全程不用真实 sleep。
func TestTimelineTurnWork_RunningTimerTicksWithoutMessages(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(map[bool]string{true: "reduced", false: "normal"}[reduced], func(t *testing.T) {
			setAppReduceMotion(t, reduced)
			var s *chatState
			var item timelineItem
			tt := ui.NewTesterWith(func(c *ui.Context) {
				if s == nil {
					s = newChatState(NewFakeClient())
					// 起点按真实精度对齐到整秒：Advance 整秒后秒数正好落在整秒边界内，不漂移。
					start := c.Now().Truncate(time.Second).Add(-5 * time.Second)
					s.messages = []Message{{ID: "u1", Role: "user", Text: "想一想", TurnIndex: 1, CreatedAt: start.Format(time.RFC3339Nano)}}
					item = timelineItem{kind: timelineItemTurnWork, id: turnWorkFoldID("u1"), turnIndex: 1, workRunning: true, workStartedAt: start}
				}
				renderTurnWorkItem(c, s, item, TokenUsage{}, false)
			}, 900, 300, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced}})
			assertTick := func(secs int) {
				t.Helper()
				label := fmt.Sprintf("已处理 %ds", secs)
				if !hasExactText(tt, label) {
					t.Fatalf("应显示 %q: %v", label, tt.Texts())
				}
				info, ok := tt.Access(label)
				if !ok || info.Description != "本回合"+label {
					t.Fatalf("Tooltip 应为 本回合%s，got %+v found=%v", label, info, ok)
				}
			}
			assertTick(5)
			for secs := 6; secs <= 9; secs++ {
				tt.Advance(time.Second)
				assertTick(secs)
			}
		})
	}
}

// TestTimelineTurnWork_SwarmBusyKeepsTickingAfterCompletion 验证 SwarmBusy 等子代理时，
// 主轮已有 TurnCompletedAt 也仍按 TurnStartedAt 继续读秒。
func TestTimelineTurnWork_SwarmBusyKeepsTickingAfterCompletion(t *testing.T) {
	var s *chatState
	var item timelineItem
	tt := ui.NewTester(func(c *ui.Context) {
		if s == nil {
			s = newChatState(NewFakeClient())
			s.threads = []Thread{{ID: "t1", SwarmBusy: true}}
			start := c.Now().Truncate(time.Second).Add(-12 * time.Second)
			s.messages = []Message{
				{ID: "u1", Role: "user", Text: "派工", TurnIndex: 1, CreatedAt: start.Format(time.RFC3339Nano)},
				{ID: "a1", Role: "assistant", Kind: "text", Text: "已派发", TurnIndex: 1, IsFinal: true,
					TurnStartedAt: start.Format(time.RFC3339Nano), TurnCompletedAt: start.Add(6 * time.Second).Format(time.RFC3339Nano)},
			}
			items := applyTurnWorkFold(s, "t1", buildTimelineItems(s.messages))
			if len(items) < 2 || !items[1].workRunning {
				panic("SwarmBusy 时主轮应仍在运行")
			}
			item = items[1]
			if !item.workStartedAt.Equal(start) {
				panic("计时起点应取权威 TurnStartedAt")
			}
		}
		renderTurnWorkItem(c, s, item, TokenUsage{}, false)
	}, 900, 300)
	if !hasExactText(tt, "已处理 12s") {
		t.Fatalf("已有 TurnCompletedAt 也应从起点读秒: %v", tt.Texts())
	}
	tt.Advance(3 * time.Second)
	if !hasExactText(tt, "已处理 15s") {
		t.Fatalf("SwarmBusy 等待子代理时计时应继续增长: %v", tt.Texts())
	}
}

// TestTimelineTurnWork_BodyFirstHeaderHasNoButton 验证正文先行的空折叠区头只是纯计时：
// 没有按钮角色、没有展开箭头，正文始终可见；拿不到时间戳时只写「已处理」，不凭空取 now 作起点。
func TestTimelineTurnWork_BodyFirstHeaderHasNoButton(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "直接回答", TurnIndex: 1, IsFinal: true},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "BODY-FIRST-TEXT", TurnIndex: 1, IsFinal: true},
	}
	fake := NewFakeClient()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "Proj1", Path: "/workspace"}},
		map[string][]Thread{"p1": {{ID: "t1", ProjectID: "p1", Title: "运行中", Provider: "claude", UpdatedAt: time.Now(), IsRunning: true}}},
	)
	fake.messages["t1"] = msgs
	state, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("已处理") && tt.HasText("BODY-FIRST-TEXT") })

	// 用户消息没有时间戳：沿用「已处理」，不编造秒数。
	if hasExactText(tt, "已处理") != true {
		t.Fatalf("无时间戳时应只写「已处理」: %v", tt.Texts())
	}
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "已处理 ") {
			t.Fatalf("无时间戳时不应凭空取 now 读秒: %v", tt.Texts())
		}
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "已处理"); ok {
		t.Fatalf("纯计时头不应带按钮角色: %v", tt.Texts())
	}
	// 行的 Tooltip 挂在 Label 行上，无障碍树里是它的父节点；逐节点找带说明的那一行。
	tipOK := false
	for _, node := range tt.AccessAll() {
		if node.Description == "本回合正在处理" {
			tipOK = true
		}
	}
	if !tipOK {
		t.Fatalf("无时间戳时 Tooltip 应为「本回合正在处理」: %v", tt.Texts())
	}
	// 运行态头不画展开箭头：整棵无障碍树里不应出现带「点按展开/收起」说明的节点。
	for _, node := range tt.AccessAll() {
		if strings.Contains(node.Name, "点按展开") || strings.Contains(node.Name, "点按收起") ||
			strings.Contains(node.Description, "本回合的工作过程") {
			t.Fatalf("纯计时头不应有展开控件: %+v", node)
		}
	}
	head, ok := tt.Find("已处理")
	if !ok {
		t.Fatalf("找不到计时头")
	}
	// 流式中的正文带光标后缀：只要文本在帧里且不是零矩形，就算始终可见。
	waitFor(t, tt, func() bool {
		for _, text := range tt.Texts() {
			if strings.HasPrefix(text, "BODY-FIRST-TEXT") {
				if body, ok := tt.Find(text); ok && body.H > 0 {
					return true
				}
			}
		}
		return false
	})
	bodyText := ""
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "BODY-FIRST-TEXT") {
			bodyText = text
			break
		}
	}
	body, _ := tt.Find(bodyText)
	if body.Y <= head.Y {
		t.Fatalf("正文应紧跟在计时头下方且始终可见: head=%+v body=%+v", head, body)
	}
	emptyHeader := false
	for _, it := range state.timelineCacheItems {
		if it.kind == timelineItemTurnWork && it.workRunning && len(it.workItems) == 0 {
			emptyHeader = true
		}
	}
	if !emptyHeader {
		t.Fatalf("应存在空折叠区的纯计时头: %+v", state.timelineCacheItems)
	}
}

// TestTimelineRows_TurnWorkFoldShowsAttentionOnFailedTool 验证含失败工具的回合在组头给出警示标记。
func TestTimelineRows_TurnWorkFoldShowsAttentionOnFailedTool(t *testing.T) {
	msgs := turnWorkSplitToolMessages()
	msgs[2].ToolStatus = "failed"
	tt := newTimelineTester(t, msgs, "FOLD-ANSWER-ONE")

	if !tt.HasText(turnWorkFoldHeaderText) {
		t.Fatalf("缺少回合折叠组头 %q，当前文本 %v", turnWorkFoldHeaderText, tt.Texts())
	}
	if !tt.HasText("工具执行失败") {
		t.Fatalf("含失败工具时组头应有警示标记: %v", tt.Texts())
	}
}

// TestTimelineRows_TurnWorkFoldSkipsTurnsWithoutBody 验证没有助手正文的回合不折叠。
func TestTimelineRows_TurnWorkFoldSkipsTurnsWithoutBody(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "跑一下构建", TurnIndex: 1, IsFinal: true},
		{ID: "t1", Role: "tool", Text: `Bash {"command":"go build ./..."}`, TurnIndex: 1, ToolStatus: "completed"},
		{ID: "t2", Role: "tool", Text: `Bash {"command":"go vet ./..."}`, TurnIndex: 1, ToolStatus: "completed"},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "   ", TurnIndex: 1, IsFinal: true},
	}
	s := newChatState(NewFakeClient())
	s.mu.Lock()
	s.messages = msgs
	s.mu.Unlock()

	folded := applyTurnWorkFold(s, "t1", buildTimelineItems(msgs))
	for _, it := range folded {
		if it.kind == timelineItemTurnWork {
			t.Fatalf("没有助手正文的回合不应折叠，got 折叠项 %q", it.id)
		}
	}
}

// TestTimelineRows_TurnWorkFoldSkippedWhenFindMatchesInside 验证查找命中落在折叠区内时不折叠，
// 否则高亮与跳转会落到看不见的行上。
func TestTimelineRows_TurnWorkFoldSkippedWhenFindMatchesInside(t *testing.T) {
	msgs := turnWorkSplitToolMessages()
	s := newChatState(NewFakeClient())
	s.mu.Lock()
	s.messages = msgs
	s.mu.Unlock()
	items := buildTimelineItems(msgs)

	st := getChatFindState(s)
	// 查找只搜用户与助手正文，命中落在折叠区内的中途正文上。
	st.query = "MID-STATUS-TEXT"
	for _, it := range applyTurnWorkFold(s, "t1", items) {
		if it.kind == timelineItemTurnWork {
			t.Fatalf("折叠区内有查找命中时不应折叠")
		}
	}

	st.query = ""
	found := false
	for _, it := range applyTurnWorkFold(s, "t1", items) {
		if it.kind == timelineItemTurnWork {
			found = true
		}
	}
	if !found {
		t.Fatalf("清除查找条件后应恢复折叠")
	}
}

// TestTimelineRows_TurnWorkUnfoldAnimatesHeight 验证展开折叠区时高度从 0 过渡到完整高度：
// 用折叠项下方最终正文相对组头的纵向距离作为高度代理，逐帧测中间值。
func TestTimelineRows_TurnWorkUnfoldAnimatesHeight(t *testing.T) {
	tt := newTimelineTester(t, turnWorkSplitToolMessages(), "FOLD-ANSWER-ONE")

	distance := func() float32 {
		t.Helper()
		header, ok := tt.Find(turnWorkFoldHeaderText)
		if !ok {
			t.Fatalf("找不到折叠组头，当前文本 %v", tt.Texts())
		}
		answer, ok := tt.Find("FOLD-ANSWER-ONE")
		if !ok {
			t.Fatalf("找不到最终正文，当前文本 %v", tt.Texts())
		}
		return answer.Y - header.Y
	}

	collapsed := distance()
	clickText(t, tt, turnWorkFoldHeaderText)
	start := distance()

	tt.Advance(motionUnfold / 2)
	middle := distance()

	tt.Advance(motionUnfold)
	done := distance()

	if !(start <= collapsed) {
		t.Fatalf("刚点开时高度应还接近收起态：start=%v collapsed=%v", start, collapsed)
	}
	if !(middle > start && middle < done) {
		t.Fatalf("展开途中高度应介于 0 与完整高度之间：start=%v middle=%v done=%v", start, middle, done)
	}
}

// TestToolCallVerbTense_ChineseStates 验证动作词时态：运行中用「正在…」，结束后用「已…」，
// 未知动词、命令与失败状态保持原样。
func TestToolCallVerbTense_ChineseStates(t *testing.T) {
	cases := []struct {
		verb    string
		running bool
		failed  bool
		want    string
	}{
		{"读取", true, false, "正在读取"},
		{"读取", false, false, "已读取"},
		{"编辑", true, false, "正在编辑"},
		{"编辑", false, false, "已编辑"},
		{"新建", true, false, "正在新建"},
		{"新建", false, false, "已新建"},
		{"搜索", true, false, "正在搜索"},
		{"搜索", false, false, "已搜索"},
		{"读取", true, true, "已读取"},
		{"$", true, false, "$"},
		{"mcp__foo", true, false, "mcp__foo"},
	}
	for _, tc := range cases {
		if got := toolCallVerbTense(tc.verb, tc.running, tc.failed); got != tc.want {
			t.Fatalf("toolCallVerbTense(%q, running=%v, failed=%v) = %q, want %q", tc.verb, tc.running, tc.failed, got, tc.want)
		}
	}
}

// TestTimelineRows_ToolChipVerbTense 验证动作胶囊上的动词随工具状态变化。
func TestTimelineRows_ToolChipVerbTense(t *testing.T) {
	base := []Message{
		{ID: "u1", Role: "user", Text: "看一下这个文件", TurnIndex: 1, IsFinal: true},
		{ID: "t1", Role: "tool", Text: `Read {"file_path":"/workspace/main.go"}`, TurnIndex: 1, ToolStatus: "running"},
	}
	runningTT := newTimelineTester(t, base, "正在读取")
	if !runningTT.HasText("正在读取") {
		t.Fatalf("运行中的工具行动词应为「正在读取」: %v", runningTT.Texts())
	}

	done := []Message{
		{ID: "u1", Role: "user", Text: "看一下这个文件", TurnIndex: 1, IsFinal: true},
		{ID: "t1", Role: "tool", Text: `Read {"file_path":"/workspace/main.go"}`, TurnIndex: 1, ToolStatus: "completed"},
	}
	doneTT := newTimelineTester(t, done, "已读取")
	if !doneTT.HasText("已读取") {
		t.Fatalf("完成后的工具行动词应为「已读取」: %v", doneTT.Texts())
	}
	if doneTT.HasText("正在读取") {
		t.Fatalf("完成后不应残留运行中的动词: %v", doneTT.Texts())
	}
}

// TestTurnWorkLastAnswers 验证折叠用的「回合最后一条助手正文」只认非思考、非空正文的消息。
func TestTurnWorkLastAnswers(t *testing.T) {
	msgs := []Message{
		{ID: "u1", Role: "user", Text: "问题", TurnIndex: 1},
		{ID: "a1", Role: "assistant", Kind: "text", Text: "先说一句", TurnIndex: 1},
		{ID: "r1", Role: "assistant", Kind: "reasoning", Text: "想一想", TurnIndex: 1},
		{ID: "a2", Role: "assistant", Kind: "text", Text: "最终答复", TurnIndex: 1},
		{ID: "a3", Role: "assistant", Kind: "text", Text: "   ", TurnIndex: 2},
	}
	got := turnWorkLastAnswers(buildTimelineItems(msgs))
	want := map[int]string{1: "a2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("turnWorkLastAnswers() = %v, want %v", got, want)
	}
}

// TestTimelineRows_ToolChipShimmer 验证动词扫光只在工具运行时出现：
// 运行中的胶囊画出扫光条，完成后同一个胶囊不再画（高光由 ui.Keyframes 驱动，不再用 c.After 轮询重建整个时间线）。
func TestTimelineRows_ToolChipShimmer(t *testing.T) {
	running := []Message{
		{ID: "u1", Role: "user", Text: "看一下这个文件", TurnIndex: 1, IsFinal: true},
		{ID: "t1", Role: "tool", Text: `Read {"file_path":"/workspace/main.go"}`, TurnIndex: 1, ToolStatus: "running"},
	}
	runningTT := newTimelineTester(t, running, "正在读取")
	if _, ok := runningTT.Find(toolShimmerBandLabel); !ok {
		t.Fatalf("运行中的工具胶囊应渲染扫光条，当前文本 %v", runningTT.Texts())
	}

	done := []Message{
		{ID: "u1", Role: "user", Text: "看一下这个文件", TurnIndex: 1, IsFinal: true},
		{ID: "t1", Role: "tool", Text: `Read {"file_path":"/workspace/main.go"}`, TurnIndex: 1, ToolStatus: "completed"},
	}
	doneTT := newTimelineTester(t, done, "已读取")
	if _, ok := doneTT.Find(toolShimmerBandLabel); ok {
		t.Fatalf("完成后不应再渲染扫光条，当前文本 %v", doneTT.Texts())
	}
}
