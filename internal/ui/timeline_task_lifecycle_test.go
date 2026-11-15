package mcodeui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 真实回归数据（均 UTC）：turn19 普通请求「按方案落盘吧」→ turn20 / turn21 两次自动汇报续跑。
// 原请求到最后报告完成共 1816.470660s = 30m16s；截图二时刻 06:07:59 距原起点 838.033830s = 13m58s。
const (
	taskLifecycleRootID     = "6fc5245a-b448-4173-9521-fb53b5ec410c"
	taskLifecycleReport1ID  = "322a8a4f-0f42-47c7-a501-d5002bcaa745"
	taskLifecycleReport2ID  = "9ad548e6-bf5d-4fc0-9570-aa6874dbe8c5"
	taskLifecycleRootAt     = "2026-11-15T05:54:00.966170Z"
	taskLifecycleRootEnd    = "2026-11-15T05:56:00.521466Z"
	taskLifecycleReport1At  = "2026-11-15T06:06:02.765607Z"
	taskLifecycleReport1End = "2026-11-15T06:08:48.541355Z"
	taskLifecycleReport2At  = "2026-11-15T06:20:43.427968Z"
	taskLifecycleReport2End = "2026-11-15T06:24:17.436830Z"
)

// taskLifecycleStamp 解析 fixture 的 RFC3339Nano 时间戳。
func taskLifecycleStamp(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// taskLifecycleTurn 按真实元数据造一个回合：权威起止写在每条消息上，done 为空表示尚未完成。
func taskLifecycleTurn(id string, index int, started, done, status string, msgs ...Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		m.TurnID = id
		m.TurnIndex = index
		m.TurnStartedAt = started
		m.TurnCompletedAt = done
		m.TurnStatus = status
		out = append(out, m)
	}
	return out
}

// taskLifecycleRootMsgs 造原 root 回合：起点取 given（真实时间或按 Tester 时钟平移），
// 本轮跨度保持真实的 119.555296s，消息间隔按真实数据等比摆放。
func taskLifecycleRootMsgs(start time.Time) []Message {
	end := start.Add(119*time.Second + 555296*time.Microsecond)
	at := func(offset time.Duration) string { return start.Add(offset).Format(time.RFC3339Nano) }
	return taskLifecycleTurn(taskLifecycleRootID, 19, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), "completed",
		Message{ID: "u19", Role: "user", Text: "按方案落盘吧", CreatedAt: at(0)},
		Message{ID: "r19", Role: "assistant", Kind: "reasoning", Text: "先确认要改的文件", CreatedAt: at(20 * time.Second)},
		Message{ID: "t19", Role: "tool", ToolName: "Bash", ToolInput: `{"command":"go build ./internal/ui"}`,
			Text: `Bash {"command":"go build ./internal/ui"}`, ToolStatus: "completed", CreatedAt: at(40 * time.Second)},
		Message{ID: "a19", Role: "assistant", Kind: "text", Text: "已派发 Aspen 与 Birch，等它们的汇报。", IsFinal: true, CreatedAt: end.Format(time.RFC3339Nano)},
	)
}

// taskLifecycleReport1Msgs 造第一份自动汇报回合：done 为空表示报告未完成（运行态关联也必须成立）。
func taskLifecycleReport1Msgs(start time.Time, done time.Time) []Message {
	doneText, status := "", "running"
	if !done.IsZero() {
		doneText, status = done.Format(time.RFC3339Nano), "completed"
	}
	at := func(offset time.Duration) string { return start.Add(offset).Format(time.RFC3339Nano) }
	return taskLifecycleTurn(taskLifecycleReport1ID, 20, start.Format(time.RFC3339Nano), doneText, status,
		Message{ID: "u20", Role: "user", Text: swarmReportPrefix + "Aspen 已完成", CreatedAt: at(0)},
		Message{ID: "t20", Role: "tool", ToolName: "Bash", ToolInput: `{"command":"go test ./internal/ui -timeout 60s"}`,
			Text: `Bash {"command":"go test ./internal/ui -timeout 60s"}`, ToolStatus: "completed", CreatedAt: at(40 * time.Second)},
		Message{ID: "a20", Role: "assistant", Kind: "text", Text: "已核对 Aspen 的改动，继续整合。", IsFinal: true, CreatedAt: at(80 * time.Second)},
	)
}

func taskLifecycleReport2Msgs(start, done time.Time) []Message {
	return taskLifecycleTurn(taskLifecycleReport2ID, 21, start.Format(time.RFC3339Nano), done.Format(time.RFC3339Nano), "completed",
		Message{ID: "u21", Role: "user", Text: swarmReportPrefix + "Birch 已完成", CreatedAt: start.Format(time.RFC3339Nano)},
		Message{ID: "a21", Role: "assistant", Kind: "text", Text: "全部完成。", IsFinal: true, CreatedAt: done.Format(time.RFC3339Nano)},
	)
}

// taskLifecycleRealMsgs 用真实时间戳拼出完整的 root + 两份报告历史。
func taskLifecycleRealMsgs(t *testing.T) []Message {
	t.Helper()
	root := taskLifecycleRootMsgs(taskLifecycleStamp(t, taskLifecycleRootAt))
	r1 := taskLifecycleReport1Msgs(taskLifecycleStamp(t, taskLifecycleReport1At), taskLifecycleStamp(t, taskLifecycleReport1End))
	r2 := taskLifecycleReport2Msgs(taskLifecycleStamp(t, taskLifecycleReport2At), taskLifecycleStamp(t, taskLifecycleReport2End))
	return append(append(root, r1...), r2...)
}

// taskLifecycleState 构造带消息与运行标志的主会话状态（不依赖 fake client 推送）。
func taskLifecycleState(msgs []Message, isRunning, swarmBusy bool) *chatState {
	s := newChatState(NewFakeClient())
	s.selectedThreadID = "lead"
	s.threads = []Thread{{ID: "lead", IsRunning: isRunning, SwarmBusy: swarmBusy}}
	s.messages = msgs
	s.markRevealBaselineLocked(msgs)
	return s
}

func taskLifecycleSetFlags(s *chatState, isRunning, swarmBusy bool) {
	s.mu.Lock()
	s.threads[0].IsRunning = isRunning
	s.threads[0].SwarmBusy = swarmBusy
	s.mu.Unlock()
}

func taskLifecycleSetMessages(s *chatState, msgs []Message) {
	s.mu.Lock()
	s.messages = msgs
	s.messagesRev++
	s.mu.Unlock()
}

// taskLifecycleRunningHeader 找出唯一的运行态折叠头。
func taskLifecycleRunningHeader(t *testing.T, items []timelineItem) timelineItem {
	t.Helper()
	var header timelineItem
	n := 0
	for _, it := range items {
		if it.kind == timelineItemTurnWork && it.workRunning {
			header = it
			n++
		}
	}
	if n != 1 {
		t.Fatalf("应只有一个运行态头，got %d: %s", n, timelineItemIDs(items))
	}
	return header
}

// TestTaskLifecycleTurnHeaderRunningLabel 运行态组头统一整秒时分秒：负值按 0，整分整时省略零秒。
func TestTaskLifecycleTurnHeaderRunningLabel(t *testing.T) {
	cases := []struct {
		elapsed time.Duration
		want    string
	}{
		{-3 * time.Second, "已处理 0s"},
		{0, "已处理 0s"},
		{16*time.Second + 900*time.Millisecond, "已处理 16s"},
		{59*time.Second + 900*time.Millisecond, "已处理 59s"},
		{60 * time.Second, "已处理 1m"},
		{179 * time.Second, "已处理 2m59s"},
		{180 * time.Second, "已处理 3m"},
		{293 * time.Second, "已处理 4m53s"},
		{3600 * time.Second, "已处理 1h"},
		{3723 * time.Second, "已处理 1h2m3s"},
	}
	for _, tc := range cases {
		if got := turnHeaderRunningLabel(tc.elapsed); got != tc.want {
			t.Fatalf("turnHeaderRunningLabel(%v) = %q, want %q", tc.elapsed, got, tc.want)
		}
	}
}

// TestTaskLifecycleOriginIndex 运行态关联不依赖报告完成：报告刚开始也能拿到 origin 与 lastReportTurn；
// 完成显示用的 span 只在目标回合有完成端点时有效；后面的报告覆盖前面的。
func TestTaskLifecycleOriginIndex(t *testing.T) {
	msgs := append(taskLifecycleRealMsgs(t)[:4], taskLifecycleReport1Msgs(
		taskLifecycleStamp(t, taskLifecycleReport1At), time.Time{})...)
	index := taskTimingIndexFromMessages(msgs)
	entry, ok := index[19]
	if !ok || entry.lastReportTurn != 20 || entry.origin.ID != "u19" {
		t.Fatalf("未完成的报告也应建立运行态关联: %+v", index)
	}
	if entry.span.ok {
		// 未完成报告的 span 至多推到同轮消息时间（回退口径），绝不能拿到完成端点；
		// 运行态关联不依赖它，完成显示只在有效且不超过真实完成时才用。
		if entry.span.end.After(taskLifecycleStamp(t, taskLifecycleReport1At).Add(80 * time.Second)) {
			t.Fatalf("报告未完成时不应拿到完成端点: %+v", entry.span)
		}
	}
	if origin, ok := taskOriginFromMessages(msgs, msgs[len(msgs)-1]); !ok || origin.ID != "u19" {
		t.Fatalf("origin 解析失败: %+v %v", origin, ok)
	}

	full := taskLifecycleRealMsgs(t)
	index = taskTimingIndexFromMessages(full)
	entry, ok = index[19]
	if !ok || entry.lastReportTurn != 21 || !entry.span.ok {
		t.Fatalf("第二份报告应覆盖并给出有效跨度: %+v", index)
	}
	if d := entry.span.end.Sub(entry.span.start); formatLiveStatsDuration(d) != "30m16s" {
		t.Fatalf("任务总跨度 = %v (%s), want 30m16s", d, formatLiveStatsDuration(d))
	}
	if len(index) != 1 {
		t.Fatalf("同一任务只应有一条索引: %+v", index)
	}
	// 无报告历史不分配 map。
	if got := taskTimingIndexFromMessages(full[:4]); got != nil {
		t.Fatalf("无报告历史不应分配索引: %+v", got)
	}
	// 新的普通用户请求切断关联：更晚的报告归到新请求，不回取 turn19。
	newRoot := taskLifecycleTurn("new-root", 22, "2026-11-15T06:30:00Z", "2026-11-15T06:31:00Z", "completed",
		Message{ID: "u22", Role: "user", Text: "另一个问题", CreatedAt: "2026-11-15T06:30:00Z"},
		Message{ID: "a22", Role: "assistant", Kind: "text", Text: "好的", IsFinal: true, CreatedAt: "2026-11-15T06:31:00Z"},
	)
	newReport := taskLifecycleTurn("new-report", 23, "2026-11-15T06:32:00Z", "2026-11-15T06:33:00Z", "completed",
		Message{ID: "u23", Role: "user", Text: swarmReportPrefix + "Cove 已完成", CreatedAt: "2026-11-15T06:32:00Z"},
		Message{ID: "a23", Role: "assistant", Kind: "text", Text: "收尾", IsFinal: true, CreatedAt: "2026-11-15T06:33:00Z"},
	)
	index = taskTimingIndexFromMessages(append(append(full, newRoot...), newReport...))
	if entry := index[19]; entry.lastReportTurn != 21 {
		t.Fatalf("旧任务不应吞掉新请求后的报告: %+v", entry)
	}
	if entry, ok := index[22]; !ok || entry.lastReportTurn != 23 {
		t.Fatalf("新请求应重开任务边界: %+v", index)
	}
}

// TestTaskLifecycleFoldKeepsRootHeader 原 root 已完成但任务未结束：等待子代理、报告续跑、再次等待、
// 再次报告都维持同一个原头（ID 与起点不变），从原请求起点读秒；报告的工具与正文留在头外不重复，
// 原 root 的收尾答复不出现提前的「已完成」摘要。
func TestTaskLifecycleFoldKeepsRootHeader(t *testing.T) {
	rootStart := taskLifecycleStamp(t, taskLifecycleRootAt)
	root := taskLifecycleRootMsgs(rootStart)
	s := taskLifecycleState(root, false, true)
	items := applyTurnWorkFold(s, "lead", buildTimelineItems(root))
	header := taskLifecycleRunningHeader(t, items)
	if header.id != turnWorkFoldID("u19") || !header.workStartedAt.Equal(rootStart) {
		t.Fatalf("等待子代理时头应是原请求头: %+v", header)
	}
	shot := taskLifecycleStamp(t, "2026-11-15T06:07:59Z")
	if got := runningTurnHeaderLabel(header, shot); got != "已处理 13m58s" {
		t.Fatalf("06:07:59 应从原起点读秒 = %q, want 已处理 13m58s", got)
	}
	assertRootAnswer := func(items []timelineItem, wantSuppressed bool) {
		t.Helper()
		for _, it := range items {
			if it.kind == timelineItemSingle && it.msg.ID == "a19" {
				if wantSuppressed && it.workHeaderID != turnWorkFoldID("u19") {
					t.Fatalf("原 root 收尾答复应压掉提前完成行: %+v", it)
				}
				if !wantSuppressed && it.workHeaderID != "" {
					t.Fatalf("无报告关联时不应压普通运行回合的完成行: %+v", it)
				}
				return
			}
		}
		t.Fatalf("原 root 收尾答复应留在头外: %s", timelineItemIDs(items))
	}
	// 还没有任何报告历史：无任务关联，保留普通运行回合的原行为（完成行由渲染层按运行态压制）。
	assertRootAnswer(items, false)

	// 报告 20 到达（先未完成）后，等待子代理阶段 backend 回合就是 root 自己：同样算接管。
	waiting := append(append([]Message(nil), root...), taskLifecycleReport1Msgs(taskLifecycleStamp(t, taskLifecycleReport1At), time.Time{})...)
	taskLifecycleSetMessages(s, waiting)
	items = applyTurnWorkFold(s, "lead", buildTimelineItems(waiting))
	assertRootAnswer(items, true)

	r1Done := append(append([]Message(nil), root...), taskLifecycleReport1Msgs(
		taskLifecycleStamp(t, taskLifecycleReport1At), taskLifecycleStamp(t, taskLifecycleReport1End))...)
	r2 := append(append([]Message(nil), r1Done...), taskLifecycleReport2Msgs(
		taskLifecycleStamp(t, taskLifecycleReport2At), taskLifecycleStamp(t, taskLifecycleReport2End))...)
	stages := []struct {
		name      string
		msgs      []Message
		isRunning bool
		swarmBusy bool
	}{
		{"报告20续跑（未完成）", append(append([]Message(nil), root...), taskLifecycleReport1Msgs(taskLifecycleStamp(t, taskLifecycleReport1At), time.Time{})...), true, false},
		{"报告20完成后再次等待", r1Done, false, true},
		{"报告21续跑", r2, true, false},
	}
	for _, stage := range stages {
		taskLifecycleSetMessages(s, stage.msgs)
		taskLifecycleSetFlags(s, stage.isRunning, stage.swarmBusy)
		items = applyTurnWorkFold(s, "lead", buildTimelineItems(stage.msgs))
		header = taskLifecycleRunningHeader(t, items)
		if header.id != turnWorkFoldID("u19") || !header.workStartedAt.Equal(rootStart) {
			t.Fatalf("[%s] 原头 ID/起点变了: %+v", stage.name, header)
		}
		if got := runningTurnHeaderLabel(header, shot); got != "已处理 13m58s" {
			t.Fatalf("[%s] 计时 = %q", stage.name, got)
		}
		assertRootAnswer(items, true)
		seen := map[string]int{}
		var walk func(list []timelineItem)
		walk = func(list []timelineItem) {
			for _, it := range list {
				if it.kind == timelineItemTurnWork {
					walk(it.workItems)
					continue
				}
				if it.kind == timelineItemSwarmReport {
					for _, m := range it.reportMsgs {
						seen[m.ID]++
					}
					continue
				}
				if it.msg.ID != "" {
					seen[it.msg.ID]++
				}
				for _, m := range it.tools {
					seen[m.ID]++
				}
			}
		}
		walk(items)
		for id, n := range seen {
			if n != 1 {
				t.Fatalf("[%s] 消息 %s 出现 %d 次，不应吞掉或重复", stage.name, id, n)
			}
		}
	}

	// 报告 20 的工具与助手正文都留在头外。
	taskLifecycleSetMessages(s, r1Done)
	items = applyTurnWorkFold(s, "lead", buildTimelineItems(r1Done))
	foundTool, foundAnswer := false, false
	for _, it := range items {
		if (it.kind == timelineItemToolGroup || it.kind == timelineItemSingle) &&
			(it.msg.ID == "t20" || containsMessageID(it.tools, "t20")) {
			foundTool = true
		}
		if it.kind == timelineItemSingle && it.msg.ID == "a20" {
			foundAnswer = true
		}
	}
	if !foundTool || !foundAnswer {
		t.Fatalf("报告回合的工具与正文应留在头外: %s", timelineItemIDs(items))
	}
}

func taskLifecycleAssertTick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if !hasExactText(tt, label) {
		// 虚拟列表/布局可能晚一帧落地：补一帧再断言（不推进时钟，读数不变）。
		tt.Frame()
	}
	if !hasExactText(tt, label) {
		t.Fatalf("应显示 %q: %v", label, tt.Texts())
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "已完成") && strings.Contains(text, "1m59s") {
			t.Fatalf("原 root 不应出现提前的完成摘要: %v", tt.Texts())
		}
	}
}

// TestTaskLifecycleChatRunningTicks 真实 renderChat 缓存路径：无新消息 Advance 跨秒与整分边界仍读秒；
// 只切运行标志（main/busy）后头仍存在；ReduceMotion 两种模式都读秒。
// Tester 时钟只前进不回拨：起点按首帧时钟对齐到整秒再减去 838s，读数与真实 13m58s 场景一致。
func TestTaskLifecycleChatRunningTicks(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(map[bool]string{true: "reduced", false: "normal"}[reduced], func(t *testing.T) {
			setAppReduceMotion(t, reduced)
			var s *chatState
			var origin time.Time
			tt := ui.NewTesterWith(func(c *ui.Context) {
				if s == nil {
					origin = c.Now().Truncate(time.Second).Add(-838 * time.Second)
					s = taskLifecycleState(taskLifecycleRootMsgs(origin), false, true)
				}
				renderChat(c, s)
			}, 1100, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced, TextScale: 1}})
			taskLifecycleAssertTick(t, tt, "已处理 13m58s")
			// settle 可能停在扫光帧上（比重建帧晚 16ms）：先补一帧让计时文字落地，
			// 余数 <100ms，后续整秒 Advance 的读数不受影响。
			tt.Frame()
			tt.Advance(time.Second)
			taskLifecycleAssertTick(t, tt, "已处理 13m59s")
			tt.Advance(time.Second)
			taskLifecycleAssertTick(t, tt, "已处理 14m")

			// 报告 20 到达并续跑：status-only 切换后原头仍在，从原起点继续。
			reportStart := origin.Add(840 * time.Second)
			msgs := append(taskLifecycleRootMsgs(origin), taskLifecycleReport1Msgs(reportStart, time.Time{})...)
			taskLifecycleSetMessages(s, msgs)
			taskLifecycleSetFlags(s, true, false)
			// 新行入场动画会逐帧推进：用 Advance 落地重建帧，不用 Frame（settle 会停在中间帧）。
			tt.Advance(time.Second)
			taskLifecycleAssertTick(t, tt, "已处理 14m1s")
			tt.Advance(time.Second)
			taskLifecycleAssertTick(t, tt, "已处理 14m2s")

			// 报告 20 完成端点落地（只更新完成元数据，不追加消息），随后再次等待子代理。
			done := append(taskLifecycleRootMsgs(origin), taskLifecycleReport1Msgs(reportStart, reportStart.Add(165*time.Second))...)
			taskLifecycleSetMessages(s, done)
			taskLifecycleSetFlags(s, false, true)
			tt.Advance(time.Second)
			taskLifecycleAssertTick(t, tt, "已处理 14m3s")
			tt.Advance(time.Second)
			taskLifecycleAssertTick(t, tt, "已处理 14m4s")

			// 报告 21 续跑同样从原起点读秒。
			r2Start := origin.Add(850 * time.Second)
			all := append(append([]Message(nil), done...), taskLifecycleReport2Msgs(r2Start, r2Start.Add(214*time.Second))...)
			taskLifecycleSetMessages(s, all)
			taskLifecycleSetFlags(s, true, false)
			tt.Advance(time.Second)
			taskLifecycleAssertTick(t, tt, "已处理 14m5s")
		})
	}
}

// TestTaskLifecycleChatCompletedFreezes 任务真正结束（main=false && busy=false）后原头固定为
// 任务用时 30m16s，不再读秒；展开体尾部同源显示任务用时与任务结束时钟，速度仍按本轮分母。
func TestTaskLifecycleChatCompletedFreezes(t *testing.T) {
	setAppReduceMotion(t, true)
	var s *chatState
	var origin, report2End time.Time
	tt := ui.NewTesterWith(func(c *ui.Context) {
		if s == nil {
			origin = c.Now().Truncate(time.Second).Add(-838 * time.Second)
			root := taskLifecycleRootMsgs(origin)
			r1 := taskLifecycleReport1Msgs(origin.Add(722*time.Second), origin.Add(888*time.Second))
			report2End = origin.Add(1816*time.Second + 470660*time.Microsecond)
			msgs := append(append(root, r1...), taskLifecycleReport2Msgs(origin.Add(1582*time.Second), report2End)...)
			s = taskLifecycleState(msgs, true, false)
			s.threadUsage = ThreadUsage{Turns: map[int]TokenUsage{19: {Output: 1000}}}
		}
		renderChat(c, s)
	}, 1100, 800, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
	taskLifecycleAssertTick(t, tt, "已处理 13m58s")

	taskLifecycleSetFlags(s, false, false)
	tt.Frame()
	if !hasExactText(tt, "任务用时 30m16s") {
		t.Fatalf("完成后原头应显示任务用时 30m16s: %v", tt.Texts())
	}
	for _, it := range s.timelineCacheItems {
		if it.kind == timelineItemTurnWork && it.workRunning {
			t.Fatalf("完成后不应再有运行态头: %+v", it)
		}
	}
	tt.Advance(3 * time.Second)
	if !hasExactText(tt, "任务用时 30m16s") || hasExactText(tt, "任务用时 30m19s") {
		t.Fatalf("完成后不应继续读秒: %v", tt.Texts())
	}

	// 展开后尾部详情与头同源：任务用时 + 任务结束时钟；速度分母仍是本轮 119s（1000/119≈8 t/s）。
	clickText(t, tt, "任务用时 30m16s")
	wantEnd := report2End.Local().Format("01-02 15:04:05")
	found := false
	for _, text := range tt.Texts() {
		if strings.Contains(text, "已完成") && strings.Contains(text, "任务用时 30m16s") && strings.Contains(text, "结束 "+wantEnd) {
			found = true
			if !strings.Contains(text, "8 t/s") {
				t.Fatalf("速度应按本轮 119s 计: %q", text)
			}
			if strings.Contains(text, "0 t/s") || strings.Contains(text, "1 t/s") {
				t.Fatalf("速度不应被任务等待稀释: %q", text)
			}
		}
	}
	if !found {
		t.Fatalf("展开体尾部应显示任务用时与任务结束时钟: %v", tt.Texts())
	}
}

// TestTaskLifecycleReloadAndNewUser 完成后重载全量与追加新的普通用户：旧 root 保持 30m16s，
// 新 root 自己计时、不串旧任务；增量与全量一致（含 idle 历史尾追加报告时已折叠前缀的 span 刷新，
// 以及完成端点只更新元数据的场景）。
func TestTaskLifecycleReloadAndNewUser(t *testing.T) {
	threadID := "lead"
	msgs := taskLifecycleRealMsgs(t)
	s := taskLifecycleState(msgs, false, false)
	helperBuildAndAssertIncremental(t, s, threadID, msgs, nil, "完成后全量")
	assertRootSpan := func(items []timelineItem, label string) {
		t.Helper()
		for _, it := range items {
			if it.kind == timelineItemTurnWork && it.id == turnWorkFoldID("u19") {
				if !it.workTaskSpan.ok || formatLiveStatsDuration(it.workTaskSpan.end.Sub(it.workTaskSpan.start)) != "30m16s" {
					t.Fatalf("[%s] 旧 root 任务跨度应稳定为 30m16s: %+v", label, it.workTaskSpan)
				}
				if got := turnWorkHeaderLabel(it, toolGroupCounts{}); got != "任务用时 30m16s" {
					t.Fatalf("[%s] 旧 root 头 = %q", label, got)
				}
				return
			}
		}
		t.Fatalf("[%s] 应有旧 root 折叠头: %s", label, timelineItemIDs(items))
	}
	assertRootSpan(s.timelineCacheItems, "完成后全量")

	// 重载：同一消息快照新建 state 再全量，值稳定。
	s2 := taskLifecycleState(msgs, false, false)
	assertRootSpan(applyTurnWorkFold(s2, threadID, buildTimelineItems(msgs)), "重载")

	// 新的普通用户请求自己计时，不串旧任务。
	newRoot := taskLifecycleTurn("new-root", 22, "2026-11-15T06:30:00Z", "", "running",
		Message{ID: "u22", Role: "user", Text: "再来一个问题", CreatedAt: "2026-11-15T06:30:00Z"},
		Message{ID: "t22", Role: "tool", ToolName: "Bash", ToolInput: `{"command":"ls"}`, Text: `Bash {"command":"ls"}`,
			ToolStatus: "running", CreatedAt: "2026-11-15T06:30:10Z"},
	)
	withNew := append(append([]Message(nil), msgs...), newRoot...)
	taskLifecycleSetMessages(s, withNew)
	taskLifecycleSetFlags(s, true, false)
	helperBuildAndAssertIncremental(t, s, threadID, withNew, nil, "新普通用户")
	assertRootSpan(s.timelineCacheItems, "新普通用户")
	header := taskLifecycleRunningHeader(t, s.timelineCacheItems)
	if header.id != turnWorkFoldID("u22") || header.workTaskSpan.ok {
		t.Fatalf("新 root 应自己计时且不串旧任务: %+v", header)
	}
	if got := runningTurnHeaderLabel(header, taskLifecycleStamp(t, "2026-11-15T06:30:30Z")); got != "已处理 30s" {
		t.Fatalf("新 root 计时 = %q, want 已处理 30s", got)
	}

	// idle 历史尾追加报告：已折叠的 root 前缀也要刷新 span；先未完成、再补完成端点元数据。
	root := taskLifecycleRootMsgs(taskLifecycleStamp(t, taskLifecycleRootAt))
	idle := taskLifecycleState(root, false, false)
	helperBuildAndAssertIncremental(t, idle, threadID, root, nil, "idle 历史")
	for _, it := range idle.timelineCacheItems {
		if it.kind == timelineItemTurnWork && it.workTaskSpan.ok {
			t.Fatalf("无报告时不应有任务跨度: %+v", it)
		}
	}
	pending := append(append([]Message(nil), root...), taskLifecycleReport1Msgs(
		taskLifecycleStamp(t, taskLifecycleReport1At), time.Time{})...)
	taskLifecycleSetMessages(idle, pending)
	helperBuildAndAssertIncremental(t, idle, threadID, pending, nil, "尾追加未完成报告")
	completed := append(append([]Message(nil), root...), taskLifecycleReport1Msgs(
		taskLifecycleStamp(t, taskLifecycleReport1At), taskLifecycleStamp(t, taskLifecycleReport1End))...)
	taskLifecycleSetMessages(idle, completed)
	helperBuildAndAssertIncremental(t, idle, threadID, completed, nil, "完成端点元数据更新")
	want := formatLiveStatsDuration(taskLifecycleStamp(t, taskLifecycleReport1End).Sub(taskLifecycleStamp(t, taskLifecycleRootAt)))
	found := false
	for _, it := range idle.timelineCacheItems {
		if it.kind == timelineItemTurnWork && it.id == turnWorkFoldID("u19") {
			found = true
			if !it.workTaskSpan.ok || formatLiveStatsDuration(it.workTaskSpan.end.Sub(it.workTaskSpan.start)) != want {
				t.Fatalf("已折叠前缀应刷新到报告完成端点: %+v want %s", it.workTaskSpan, want)
			}
		}
	}
	if !found {
		t.Fatalf("idle 历史应保留 root 折叠头: %s", timelineItemIDs(idle.timelineCacheItems))
	}
}

// TestTaskLifecycleBeeWindowOwnStart 子代理窗口按自己的 turn0 起点读秒：179→2m59s、180→3m，
// 不套主任务起点；顶部「正在回复…」指示不受影响。
func TestTaskLifecycleBeeWindowOwnStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		secs int
		want string
	}{
		{"Aspen", "h-aspen", 179, "已处理 2m59s"},
		{"Birch", "h-birch", 180, "已处理 3m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s *chatState
			bee := SwarmBee{ThreadID: tc.id, Name: tc.name, Status: "running"}
			tt := ui.NewTester(func(c *ui.Context) {
				if s == nil {
					start := c.Now().Truncate(time.Second).Add(-time.Duration(tc.secs) * time.Second)
					s = newChatState(NewFakeClient())
					s.selectedThreadID = tc.id
					s.threads = []Thread{{ID: tc.id, IsRunning: true}}
					msgs := taskLifecycleTurn("bee-turn", 0, start.Format(time.RFC3339Nano), "", "running",
						Message{ID: "u0", Role: "user", Text: "执行子任务", CreatedAt: start.Format(time.RFC3339Nano)},
					)
					s.messages = msgs
					s.markRevealBaselineLocked(msgs)
				}
				renderBeeTimeline(c, s, bee)
			}, 900, 700)
			if !hasExactText(tt, tc.want) {
				t.Fatalf("子窗口应按自身起点显示 %q: %v", tc.want, tt.Texts())
			}
			if !tt.HasText("正在回复…") {
				t.Fatalf("运行中的子窗口应保留回复中指示: %v", tt.Texts())
			}
			tt.Advance(time.Second)
			next := formatLiveStatsDuration(time.Duration(tc.secs+1) * time.Second)
			if !hasExactText(tt, "已处理 "+next) {
				t.Fatalf("子窗口应继续读秒到 已处理 %s: %v", next, tt.Texts())
			}
		})
	}
}

// TestTaskLifecycleHeaderFooterHourFormatter 小时/整分任务：头、展开尾部与独立完成行同一可读时分秒；
// 速度分母仍是本轮（DurationMs=0 时按本轮 119s），不因任务 1h+ 被稀释。
func TestTaskLifecycleHeaderFooterHourFormatter(t *testing.T) {
	rootStart := taskLifecycleStamp(t, taskLifecycleRootAt)
	root := taskLifecycleRootMsgs(rootStart)
	reportEnd := rootStart.Add(3723 * time.Second)
	report := taskLifecycleReport1Msgs(taskLifecycleStamp(t, taskLifecycleReport1At), reportEnd)
	msgs := append(append([]Message(nil), root...), report...)
	s := taskLifecycleState(msgs, false, false)
	items := applyTurnWorkFold(s, "lead", buildTimelineItems(msgs))
	var fold timelineItem
	for _, it := range items {
		if it.kind == timelineItemTurnWork && it.id == turnWorkFoldID("u19") {
			fold = it
		}
	}
	if got := turnWorkHeaderLabel(fold, toolGroupCounts{}); got != "任务用时 1h2m3s" {
		t.Fatalf("小时任务头 = %q, want 任务用时 1h2m3s", got)
	}
	if got := turnWorkHeaderAccessLabel(fold, false); got != "工具调用，任务用时 1h2m3s，点按展开" {
		t.Fatalf("读屏名称 = %q", got)
	}
	usage := TokenUsage{Output: 1000}
	wantEnd := reportEnd.Local().Format("01-02 15:04:05")
	wantFooter := "已完成 · 任务用时 1h2m3s · 工具 1 · 输出 1.0k · 8 t/s · 结束 " + wantEnd
	if got := turnWorkFooterText(s, 19, usage, fold.workTaskSpan); got != wantFooter {
		t.Fatalf("展开尾部 = %q, want %q", got, wantFooter)
	}
	// 任务 formatter：小时、整时、整分与不足 1s 的口径。
	if got := taskFinishedSummary("", turnToolStats{}, usage, 3723*time.Second); got != "已完成 · 任务用时 1h2m3s · 输出 1.0k" {
		t.Fatalf("任务 formatter 小时文本 = %q", got)
	}
	if got := taskFinishedSummary("", turnToolStats{}, usage, 3600*time.Second); got != "已完成 · 任务用时 1h · 输出 1.0k" {
		t.Fatalf("整时任务文本 = %q", got)
	}
	if got := taskFinishedSummary("", turnToolStats{}, usage, 180*time.Second); got != "已完成 · 任务用时 3m · 输出 1.0k" {
		t.Fatalf("整分任务文本 = %q", got)
	}
	if got := taskFinishedSummary("", turnToolStats{}, usage, 500*time.Millisecond); got != "已完成 · 输出 1.0k" {
		t.Fatalf("不足 1s 不应造用时段 = %q", got)
	}
	// 无 usage：任务的起止详情也不能完全丢失。
	tip := turnWorkHeaderTooltip(s, fold, toolGroupCounts{}, TokenUsage{}, false, time.Now())
	for _, want := range []string{"开始 ", "结束 ", "本轮用时 "} {
		if !strings.Contains(tip, want) {
			t.Fatalf("无 usage 的完成 tooltip 缺少 %q:\n%s", want, tip)
		}
	}
	// 增量与全量一致（含 workTaskSpan 字段），重复折叠幂等。
	helperBuildAndAssertIncremental(t, s, "lead", msgs, nil, "小时任务全量一致")
	if !reflect.DeepEqual(applyTurnWorkFold(s, "lead", s.timelineCacheItems), s.timelineCacheItems) {
		t.Fatalf("重复折叠应幂等")
	}
}
