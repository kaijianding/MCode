package mcodeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// taskTimingTurn 描述测试里的一个回合：id 非空时带 TurnID 与权威起止（turnID 为空的旧消息走
// TurnIndex + CreatedAt 回退）；status 写入同轮全部消息的 TurnStatus；
// assistantAt 非空时覆盖助手消息的 CreatedAt，用于构造与权威时间不一致的场景。
type taskTimingTurn struct {
	id          string
	index       int
	user        Message
	started     string
	done        string
	status      string
	assistantAt string
}

func taskTimingMsgs(turns ...taskTimingTurn) []Message {
	var msgs []Message
	for _, t := range turns {
		apply := func(m Message) Message {
			m.TurnIndex = t.index
			m.TurnID = t.id
			if t.id != "" {
				m.TurnStartedAt = t.started
				m.TurnCompletedAt = t.done
				m.TurnStatus = t.status
			}
			return m
		}
		msgs = append(msgs, apply(t.user))
		at := t.done
		if t.assistantAt != "" {
			at = t.assistantAt
		}
		msgs = append(msgs, apply(Message{
			ID: t.id + "-a", Role: "assistant", Text: "答复", IsFinal: true, CreatedAt: at,
		}))
	}
	return msgs
}

// 真实回归数据：turn15 普通请求 + turn16 自动汇报续跑，任务总跨度 385.319665s。
const (
	taskTimingOriginID  = "4a52da7d-647f-43ce-a44b-4d234ee35db2"
	taskTimingReportID  = "19c965c8-b746-4e39-9816-e72dd76758fc"
	taskTimingOriginAt  = "2026-11-15T04:32:55.388649Z"
	taskTimingOriginEnd = "2026-11-15T04:34:54.695740Z"
	taskTimingReportAt  = "2026-11-15T04:37:40.917579Z"
	taskTimingReportEnd = "2026-11-15T04:39:20.708314Z"
)

func taskTimingRealTurns() []taskTimingTurn {
	return []taskTimingTurn{
		{id: taskTimingOriginID, index: 15, started: taskTimingOriginAt, done: taskTimingOriginEnd, status: "completed",
			user: Message{ID: "u15", Role: "user", Text: "修复完成行用时", CreatedAt: taskTimingOriginAt}},
		{id: taskTimingReportID, index: 16, started: taskTimingReportAt, done: taskTimingReportEnd, status: "completed",
			user: Message{ID: "u16", Role: "user", Text: swarmReportPrefix + "Aspen 已完成", CreatedAt: taskTimingReportAt}},
	}
}

// TestTaskTimingSpanFromMessages 覆盖任务跨度推断的边界：真实两轮、连续多次汇报、
// 新请求切断、单轮不推断、缺历史、分割/系统唤醒/编排通知切断、压缩不断、中断不推断、
// 旧前缀、权威缺失回退、权威起止优先于 CreatedAt 与重复消息不双计。
func TestTaskTimingSpanFromMessages(t *testing.T) {
	origin := taskTimingRealTurns()[0]
	report := taskTimingRealTurns()[1]
	stamp := func(s string) time.Time {
		at, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	cases := []struct {
		name  string
		turns []taskTimingTurn
		want  time.Duration // 0 表示不推断（ok=false）
		start string        // 非空时额外校验起点等于该权威时间
	}{
		{
			name:  "真实两轮任务总跨度",
			turns: taskTimingRealTurns(),
			want:  385319665 * time.Microsecond,
		},
		{
			name: "原始 CreatedAt 早于权威起点仍取 TurnStartedAt",
			turns: []taskTimingTurn{
				{id: taskTimingOriginID, index: 15, started: taskTimingOriginAt, done: taskTimingOriginEnd, status: "completed",
					user: Message{ID: "u15", Role: "user", Text: "修复完成行用时", CreatedAt: "2026-11-15T04:32:45Z"}},
				taskTimingRealTurns()[1],
			},
			want:  385319665 * time.Microsecond,
			start: taskTimingOriginAt,
		},
		{
			name: "原始回合缺 TurnCompletedAt 仍取有效权威起点",
			turns: []taskTimingTurn{
				{id: taskTimingOriginID, index: 15, started: taskTimingOriginAt, done: "", status: "completed",
					assistantAt: taskTimingOriginEnd,
					user:        Message{ID: "u15", Role: "user", Text: "修复完成行用时", CreatedAt: "2026-11-15T04:32:45Z"}},
				taskTimingRealTurns()[1],
			},
			want:  385319665 * time.Microsecond,
			start: taskTimingOriginAt,
		},
		{
			name: "目标 CreatedAt 晚于权威完成仍取 TurnCompletedAt",
			turns: []taskTimingTurn{
				taskTimingRealTurns()[0],
				{id: taskTimingReportID, index: 16, started: taskTimingReportAt, done: taskTimingReportEnd, status: "completed",
					assistantAt: "2026-11-15T04:40:00Z",
					user:        Message{ID: "u16", Role: "user", Text: swarmReportPrefix + "Aspen 已完成", CreatedAt: taskTimingReportAt}},
			},
			want: 385319665 * time.Microsecond,
		},
		{
			name: "连续多次汇报都从原始起点计",
			turns: append(taskTimingRealTurns(), taskTimingTurn{
				id: "r2", index: 17, started: "2026-11-15T04:41:00Z", done: "2026-11-15T04:42:00Z", status: "completed",
				user: Message{ID: "u17", Role: "user", Text: legacySwarmReportPrefix + "Birch 已完成", CreatedAt: "2026-11-15T04:41:00Z"},
			}),
			want: stamp("2026-11-15T04:42:00Z").Sub(stamp(taskTimingOriginAt)),
		},
		{
			name: "新普通请求重开任务边界",
			turns: []taskTimingTurn{
				taskTimingRealTurns()[0],
				{id: "u-new", index: 16, started: "2026-11-15T04:36:00Z", done: "2026-11-15T04:36:30Z", status: "completed",
					user: Message{ID: "uN", Role: "user", Text: "另一个问题", CreatedAt: "2026-11-15T04:36:00Z"}},
				taskTimingRealTurns()[1],
			},
			want: stamp(taskTimingReportEnd).Sub(stamp("2026-11-15T04:36:00Z")),
		},
		{
			name:  "普通单轮不推断",
			turns: []taskTimingTurn{origin},
			want:  0,
		},
		{
			name:  "缺原始历史不推断",
			turns: []taskTimingTurn{report},
			want:  0,
		},
		{
			name: "分割会话切断",
			turns: append([]taskTimingTurn{origin},
				taskTimingTurn{index: 99, user: Message{ID: "split", Role: "system", Kind: splitSessionKind}},
				report),
			want: 0,
		},
		{
			name: "独立系统唤醒切断",
			turns: append([]taskTimingTurn{origin},
				taskTimingTurn{index: 99, user: Message{ID: "wake", Role: "user", Kind: stewardWakeKind, Text: "宿主唤醒"}},
				report),
			want: 0,
		},
		{
			name: "宿主事件切断",
			turns: append([]taskTimingTurn{origin},
				taskTimingTurn{index: 99, user: Message{ID: "evt", Role: "user", Kind: hostEventKind, Text: "宿主转来事件"}},
				report),
			want: 0,
		},
		{
			name: "非报告编排通知切断",
			turns: append([]taskTimingTurn{origin},
				taskTimingTurn{index: 99, user: Message{ID: "notice", Role: "user", Text: swarmNoticePrefix + "子代理 Aspen 已派发"}},
				report),
			want: 0,
		},
		{
			name: "压缩边界不切断",
			turns: append([]taskTimingTurn{origin},
				taskTimingTurn{index: 99, user: Message{ID: "compact", Role: "system", Kind: contextCompactedKind}},
				report),
			want: 385319665 * time.Microsecond,
		},
		{
			name: "原始回合被中断不推断",
			turns: []taskTimingTurn{
				{id: origin.id, index: origin.index, started: origin.started, done: origin.done, status: "interrupted", user: origin.user},
				report,
			},
			want: 0,
		},
		{
			name: "Hydra 旧前缀汇报",
			turns: []taskTimingTurn{origin, {
				id: report.id, index: report.index, started: report.started, done: report.done, status: "completed",
				user: Message{ID: "u16", Role: "user", Text: legacySwarmReportPrefix + "Aspen 已完成", CreatedAt: taskTimingReportAt},
			}},
			want: 385319665 * time.Microsecond,
		},
		{
			name: "旧消息无权威时间走消息时间回退",
			turns: []taskTimingTurn{
				{index: 15, user: Message{ID: "u15", Role: "user", Text: "修复完成行用时", CreatedAt: taskTimingOriginAt}},
				{index: 16, done: taskTimingReportEnd, user: Message{ID: "u16", Role: "user", Text: swarmReportPrefix + "Aspen 已完成", CreatedAt: taskTimingReportAt}},
			},
			want: stamp(taskTimingReportEnd).Sub(stamp(taskTimingOriginAt)),
		},
		{
			name: "终点不取目标回合之后的消息",
			turns: append(taskTimingRealTurns(), taskTimingTurn{
				id: "later", index: 17, started: "2026-11-15T04:50:00Z", done: "2026-11-15T04:55:00Z", status: "completed",
				user: Message{ID: "uL", Role: "user", Text: "之后的独立请求", CreatedAt: "2026-11-15T04:50:00Z"},
			}),
			want: 385319665 * time.Microsecond,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := taskTimingMsgs(tc.turns...)
			target := msgs[len(msgs)-1]
			if tc.name == "新普通请求重开任务边界" {
				// 新请求自己成为原始起点：任务只从它计到报告完成，不串更早的 turn15；
				// 而 turn15 自己的完成行仍是单轮口径。
				if got := taskSpanFromMessages(msgs, target); !got.ok ||
					!got.start.Equal(stamp("2026-11-15T04:36:00Z")) ||
					got.end.Sub(got.start) != stamp(taskTimingReportEnd).Sub(stamp("2026-11-15T04:36:00Z")) {
					t.Fatalf("新请求应重开任务边界，got %+v", got)
				}
				if got := taskSpanFromMessages(msgs, msgs[1]); got.ok {
					t.Fatalf("普通单轮不应推断任务跨度: %+v", got)
				}
				return
			}
			if tc.name == "终点不取目标回合之后的消息" {
				target = msgs[3] // 目标是报告回合，不是其后独立请求的最后一条。
			}
			got := taskSpanFromMessages(msgs, target)
			if tc.want == 0 {
				if got.ok {
					t.Fatalf("不应推断任务跨度，got %+v", got)
				}
				return
			}
			if !got.ok {
				t.Fatalf("应推断任务跨度，got %+v", got)
			}
			if d := got.end.Sub(got.start); d != tc.want {
				t.Fatalf("任务跨度 = %v, want %v", d, tc.want)
			}
			if tc.start != "" && !got.start.Equal(stamp(tc.start)) {
				t.Fatalf("起点 = %v, want %v", got.start, stamp(tc.start))
			}
			// 重复消息（重载/增量重放可能出现）不延长也不双计。
			dup := append(append([]Message(nil), msgs...), msgs...)
			if again := taskSpanFromMessages(dup, target); !again.ok || again.end.Sub(again.start) != tc.want {
				t.Fatalf("重复消息后跨度变了: %+v", again)
			}
		})
	}
}

// TestTaskTimingRealRegression 用真实数据校验任务用时文本与本轮用时的格式化：
// 385.319665s → 6m25s，本轮 99.790735s → 1m39s。
func TestTaskTimingRealRegression(t *testing.T) {
	msgs := taskTimingMsgs(taskTimingRealTurns()...)
	got := taskSpanFromMessages(msgs, msgs[len(msgs)-1])
	if !got.ok {
		t.Fatalf("真实两轮应推断出任务跨度: %+v", got)
	}
	if d := got.end.Sub(got.start); d != 385319665*time.Microsecond {
		t.Fatalf("任务总跨度 = %v, want 385.319665s", d)
	}
	if s := formatShortDuration(got.end.Sub(got.start)); s != "6m 25s" {
		t.Fatalf("任务用时文本 = %q, want 6m 25s", s)
	}
	round := stampForTaskTest(t, taskTimingReportEnd).Sub(stampForTaskTest(t, taskTimingReportAt))
	if s := formatShortDuration(round); s != "1m 39s" {
		t.Fatalf("本轮用时文本 = %q, want 1m 39s", s)
	}
}

func stampForTaskTest(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// taskTimingFixtureState 用真实两轮数据构造会话状态；同一快照重建结果应一致（重载稳定）。
func taskTimingFixtureState() (*chatState, Message) {
	s := newChatState(NewFakeClient())
	s.messages = taskTimingMsgs(taskTimingRealTurns()...)
	return s, s.messages[len(s.messages)-1]
}

// TestTaskTimingFinishedBlockUI 直接渲染完成块：任务用时 6m25s、结束时钟、tooltip 任务起点
// 与本轮用时 1m39s；输出速度仍按本轮约 100s 计（≈10 t/s），不被任务跨度稀释。
func TestTaskTimingFinishedBlockUI(t *testing.T) {
	usage := TokenUsage{Output: 1000}
	round := stampForTaskTest(t, taskTimingReportEnd).Sub(stampForTaskTest(t, taskTimingReportAt))
	wantText := "已完成 · 任务用时 6m 25s · 输出 1k · 10 t/s · 结束 " +
		stampForTaskTest(t, taskTimingReportEnd).Local().Format("01-02 15:04:05")
	var s *chatState
	var final Message
	tt := ui.NewTester(func(c *ui.Context) {
		if s == nil {
			s, final = taskTimingFixtureState()
		}
		renderTurnFinishedBlockWithDuration(c, s, final, usage, round)
	}, 900, 200)
	if !hasExactText(tt, wantText) {
		t.Fatalf("完成行文本不符，want %q，got %v", wantText, tt.Texts())
	}
	if tt.HasText("用时 1m 39s ·") {
		t.Fatalf("完成行不应再把本轮用时当任务用时: %v", tt.Texts())
	}
	// 悬停完成行，tooltip 应显示任务起止与本轮用时。
	rect, ok := tt.Find(wantText)
	if !ok {
		t.Fatalf("找不到完成行文本: %v", tt.Texts())
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Advance(time.Second)
	var tip string
	for _, text := range tt.Texts() {
		if strings.Contains(text, "本轮用时") {
			tip = text
		}
	}
	if tip == "" {
		t.Fatalf("完成行 tooltip 应含本轮用时: %v", tt.Texts())
	}
	for _, want := range []string{
		"开始 " + stampForTaskTest(t, taskTimingOriginAt).Local().Format("01-02 15:04:05"),
		"结束 " + stampForTaskTest(t, taskTimingReportEnd).Local().Format("01-02 15:04:05"),
		"本轮用时 1m 39s",
	} {
		if !strings.Contains(tip, want) {
			t.Fatalf("tooltip 缺少 %q:\n%s", want, tip)
		}
	}

	// 同一消息快照重建 state：结果一致，覆盖重载稳定。
	s2, final2 := taskTimingFixtureState()
	got1 := taskDurationFor(s, final)
	got2 := taskDurationFor(s2, final2)
	if !got1.ok || got1 != got2 {
		t.Fatalf("重载后任务跨度不一致: %+v vs %+v", got1, got2)
	}
}

// TestTaskTimingFinishedBlockOrdinary 普通单轮完成行保持原样：用时标签与 tooltip 都不变。
func TestTaskTimingFinishedBlockOrdinary(t *testing.T) {
	s := newChatState(NewFakeClient())
	s.messages = taskTimingMsgs(taskTimingRealTurns()[0])
	final := s.messages[len(s.messages)-1]
	usage := TokenUsage{Output: 1000}
	round := stampForTaskTest(t, taskTimingOriginEnd).Sub(stampForTaskTest(t, taskTimingOriginAt))
	// 1000 tokens ÷ 119.307091s ≈ 8 t/s，速度分母仍是本轮。
	wantText := "已完成 · 用时 1m 59s · 输出 1k · 8 t/s · 结束 " +
		stampForTaskTest(t, taskTimingOriginEnd).Local().Format("01-02 15:04:05")
	tt := ui.NewTester(func(c *ui.Context) {
		renderTurnFinishedBlockWithDuration(c, s, final, usage, round)
	}, 900, 200)
	if !hasExactText(tt, wantText) {
		t.Fatalf("普通完成行应保持原样，want %q，got %v", wantText, tt.Texts())
	}
	for _, info := range tt.AccessAll() {
		if strings.Contains(info.Description, "本轮用时") || strings.Contains(info.Description, "任务用时") {
			t.Fatalf("普通完成行 tooltip 不应出现任务口径: %q", info.Description)
		}
	}
}

// TestTaskTimingReportItemPath 走真实时间线条目路径：报告回合首项是 timelineItemSwarmReport，
// 不进普通用户折叠；其后的最终答复经 renderMessageItem 渲染出任务用时完成行。
func TestTaskTimingReportItemPath(t *testing.T) {
	s, _ := taskTimingFixtureState()
	items := buildTimelineItems(s.messages)
	var reportItem, finalItem *timelineItem
	for i := range items {
		it := &items[i]
		if it.kind == timelineItemSwarmReport && it.turnIndex == 16 {
			reportItem = it
		}
		if it.kind == timelineItemSingle && it.msg.Role == "assistant" && it.msg.TurnIndex == 16 && it.msg.IsFinal {
			finalItem = it
		}
		if it.kind == timelineItemTurnWork && it.turnIndex == 16 {
			t.Fatalf("报告回合不应进普通用户折叠: %+v", it)
		}
	}
	if reportItem == nil || finalItem == nil {
		t.Fatalf("时间线应含报告行与最终答复: %+v", items)
	}
	usage := TokenUsage{Output: 1000}
	round := stampForTaskTest(t, taskTimingReportEnd).Sub(stampForTaskTest(t, taskTimingReportAt))
	wantText := "已完成 · 任务用时 6m 25s · 输出 1k · 10 t/s · 结束 " +
		stampForTaskTest(t, taskTimingReportEnd).Local().Format("01-02 15:04:05")
	tt := ui.NewTester(func(c *ui.Context) {
		renderMessageItem(c, s, finalItem.msg, "", "", true, true, usage, round, false)
	}, 900, 300)
	if !hasExactText(tt, wantText) {
		t.Fatalf("真实路径完成行文本不符，want %q，got %v", wantText, tt.Texts())
	}
}
