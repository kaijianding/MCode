// 自动子代理汇报续跑任务的整体历时：从原始普通用户请求到本报告回合完成的墙钟跨度。
package mcodeui

import (
	"strings"
	"time"
)

// taskTimingSummaryToken 是完成行里用时段的固定标签，跨回合任务用它做一次精确替换。
const taskTimingSummaryToken = " · 用时 "

// taskTimingSpan 是一个自动汇报续跑任务的整体跨度：start 是原始普通用户请求回合的起点，
// end 是目标报告回合的完成端点。ok 为 false 时不显示任务用时，完成行维持本轮口径。
type taskTimingSpan struct {
	start, end time.Time
	ok         bool
}

// taskDurationFor 持 s.mu 直接对 s.messages 推断目标完成消息所属任务的整体跨度；
// 锁内只调用无锁纯 helper，不复制消息，也不调用会再加锁的 turnFinishedSpanFor 等。
func taskDurationFor(s *chatState, m Message) taskTimingSpan {
	if s == nil {
		return taskTimingSpan{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return taskSpanFromMessages(s.messages, m)
}

// taskTimingEntry 是任务索引的一条记录：origin 是原始普通用户请求消息，lastReportTurn 是
// 该任务最新一份自动汇报的回合号；span 供完成显示用，报告未完成或起止解析不出时 ok=false，
// 但 origin 关联本身仍成立（运行态头只靠 origin，不等报告完成）。
type taskTimingEntry struct {
	origin         Message
	lastReportTurn int
	span           taskTimingSpan
}

// taskOriginFromMessages 沿消息顺序找目标汇报回合所属任务的原始普通用户请求：
// 目标回合的用户输入必须是自动子代理汇报（[编排]子代理汇报 / [Hydra]协作者汇报），
// 再向前找最近的普通用户请求作为原始起点。中间的回合只能是自动汇报续跑或系统边界；
// 普通新用户输入、显式分割会话、独立系统唤醒（host_event / steward_wake）与无法归属的
// 编排通知都切断关联，context_compacted 不切断。原始回合被中断（报告可能暂扣等新输入）、
// 缺原始历史时返回 false。只看归属，不看目标是否完成：运行态关联同样走这套边界。
func taskOriginFromMessages(msgs []Message, target Message) (Message, bool) {
	if target.TurnID == "" && target.TurnIndex < 0 {
		return Message{}, false
	}
	reportInput, targetIdx := false, -1
	for i, m := range msgs {
		if !sameTurn(m, target) {
			continue
		}
		targetIdx = i
		if isSwarmReportMessage(m) {
			reportInput = true
		}
	}
	if !reportInput || targetIdx < 0 {
		return Message{}, false
	}
	originIdx := -1
scan:
	for i := targetIdx - 1; i >= 0; i-- {
		m := msgs[i]
		if sameTurn(m, target) {
			continue
		}
		switch {
		case m.Role == "user" && isSwarmReportMessage(m):
			// 更早的自动汇报续跑：继续向前找原始请求。
			continue
		case m.Kind == splitSessionKind:
			break scan
		case m.Kind == contextCompactedKind || (m.Role == "system" && m.Kind != splitSessionKind):
			continue
		case m.Role == "user" && isHiddenTriggerKind(m.Kind):
			break scan
		case m.Role == "user" && !isSwarmNoticeText(strings.TrimSpace(m.Text)):
			originIdx = i
			break scan
		default:
			if m.Role == "user" {
				// 编排通知不是汇报：无法归属，切断关联。
				break scan
			}
			// 助手 / 工具等回合内消息：继续向前。
		}
	}
	if originIdx < 0 {
		return Message{}, false
	}
	origin := msgs[originIdx]
	for _, m := range msgs {
		if sameTurn(m, origin) && m.TurnStatus == "interrupted" {
			return Message{}, false
		}
	}
	return origin, true
}

// taskTimingComputeSpan 计算从原始请求到目标报告回合完成端点的整体跨度：
// 起点优先原始回合的权威 TurnStartedAt，拿不到时退回同轮消息时间；
// 终点只到目标报告回合的完成端点（权威 TurnCompletedAt 或同轮消息时间），不越到后续请求。
// 起止解析不出或次序无效时返回 ok=false。
func taskTimingComputeSpan(msgs []Message, origin, target Message) taskTimingSpan {
	start, _ := turnAuthoritativeSpan(msgs, origin)
	if start.IsZero() {
		start, _ = turnSpanForMessage(msgs, origin)
	}
	_, end := turnSpanForMessage(msgs, target)
	if _, e := turnAuthoritativeSpan(msgs, target); !e.IsZero() {
		end = e
	}
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return taskTimingSpan{}
	}
	return taskTimingSpan{start: start, end: end, ok: true}
}

// taskSpanFromMessages 推断任务跨度：归属边界见 taskOriginFromMessages，起止见 taskTimingComputeSpan。
func taskSpanFromMessages(msgs []Message, target Message) taskTimingSpan {
	origin, ok := taskOriginFromMessages(msgs, target)
	if !ok {
		return taskTimingSpan{}
	}
	return taskTimingComputeSpan(msgs, origin, target)
}

// taskTimingIndexFromMessages 把已加载消息里的自动汇报按原始请求回合归组：
// 同一任务后面的报告覆盖前面的，记录最新报告回合与到它完成端点的跨度。
// 无报告历史时不分配 map。纯函数，调用方自行持锁；只在时间线折叠重建时生成一次，不做缓存。
func taskTimingIndexFromMessages(msgs []Message) map[int]taskTimingEntry {
	var index map[int]taskTimingEntry
	// seen 把报告回合映射到已归组的原始回合：同回合的后续消息不重复扫描归属。
	var seen map[int]int
	for _, m := range msgs {
		if !isSwarmReportMessage(m) {
			continue
		}
		if originTurn, ok := seen[m.TurnIndex]; ok {
			// 同回合的重复/后续消息：只在拿到有效跨度时升级（完成端点可能落在后面的消息上）。
			entry := index[originTurn]
			if !entry.span.ok {
				if span := taskTimingComputeSpan(msgs, entry.origin, m); span.ok {
					entry.span = span
					index[originTurn] = entry
				}
			}
			continue
		}
		origin, ok := taskOriginFromMessages(msgs, m)
		if !ok {
			continue
		}
		if index == nil {
			index = make(map[int]taskTimingEntry)
			seen = make(map[int]int)
		}
		seen[m.TurnIndex] = origin.TurnIndex
		index[origin.TurnIndex] = taskTimingEntry{
			origin:         origin,
			lastReportTurn: m.TurnIndex,
			span:           taskTimingComputeSpan(msgs, origin, m),
		}
	}
	return index
}

// taskTimingIndexFor 持 s.mu 直接对 s.messages 生成任务索引；
// 锁内只调用无锁纯 helper，不复制消息，也不调用会再加锁的 turnFinishedSpanFor 等。
func taskTimingIndexFor(s *chatState) map[int]taskTimingEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return taskTimingIndexFromMessages(s.messages)
}

// taskFinishedSummary 用任务跨度生成完成行文本，并把唯一的用时段整段换成「任务用时」：
// 时分秒口径与运行态组头一致（formatLiveStatsDuration，整分整时省略零秒，超过小时不写成 62m3s）；
// 其余分段（工具、改动、输出）保持 formatter 原样。不足 1 秒没有用时段时原样返回。
func taskFinishedSummary(model string, stats turnToolStats, usage TokenUsage, span time.Duration) string {
	text := formatTurnFinishedSummary(model, stats, usage, span)
	return strings.Replace(text, taskTimingSummaryToken+formatShortDuration(span), " · 任务用时 "+formatLiveStatsDuration(span), 1)
}

// taskFinishedTooltip 用任务跨度生成悬停详情的起止时间，并补一行本轮用时保留本轮信息；
// 其余字段与 formatTurnFinishedTooltip 一致。
func taskFinishedTooltip(info turnFinishedInfo, round time.Duration) string {
	tooltip := formatTurnFinishedTooltip(info)
	line := "本轮用时 " + formatShortDuration(round)
	if tooltip == "" {
		return line
	}
	return tooltip + "\n" + line
}
