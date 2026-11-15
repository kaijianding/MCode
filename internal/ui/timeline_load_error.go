package mcodeui

import "github.com/egoist/mygo/ui"

func (s *chatState) withTimelineLoadError(items []timelineItem) []timelineItem {
	s.mu.Lock()
	tid := s.selectedThreadID
	message := s.timelineLoadErrors[tid]
	s.mu.Unlock()
	if tid == "" || message == "" {
		return items
	}
	return append(append([]timelineItem(nil), items...), timelineItem{kind: timelineItemLoadError, id: "load-error-" + tid, threadID: tid, msg: Message{Text: message}})
}

func renderTimelineLoadError(c *ui.Context, s *chatState, item timelineItem) {
	ui.Row(c.Key("timeline-load-error-" + item.threadID)).Gap(Space3).AlignItems(ui.Center).Children(func() {
		ui.Text(c.Key("timeline-load-error-text"), item.msg.Text).FontSize(uiFont(TypeBody)).TextColor(ThemeDanger(c))
		Button(c.Key("timeline-load-error-retry"), ButtonOpts{Text: "重试", Label: "重试", Variant: ButtonGhost, OnClick: func() { s.selectThread(item.threadID) }})
	})
}
