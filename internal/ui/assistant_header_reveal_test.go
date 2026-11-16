package mcodeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func testHeaderInsertKeepsReveal(t *testing.T, s *chatState, render func(*ui.Context)) {
	t.Helper()
	tt := ui.NewTester(render, 900, 700)
	setLeadRevealText(s, "第一段正文")
	tt.Frame()
	setLeadRevealText(s, "第一段正文，第二段末尾文字")
	tt.Frame()
	for i := 0; i < 8; i++ {
		tt.Advance(500 * time.Millisecond)
	}
	tt.Frame()
	if !s.assistantRevealed("lead-answer") || !strings.Contains(leadTexts(tt), "第二段末尾文字") {
		t.Fatalf("正文应在插入头部前完全揭示：%v", tt.Texts())
	}
	s.mu.Lock()
	s.messages[0].IsFinal = true
	s.messagesRev++
	s.mu.Unlock()
	tt.Frame()
	if !strings.Contains(leadTexts(tt), "第二段末尾文字") {
		t.Fatalf("插入头部后正文不应重新揭示：%v", tt.Texts())
	}
}

func TestAssistantHeaderInsertKeepsReveal(t *testing.T) {
	s := newLeadRevealState()
	s.threads[0].Provider = "claude"
	testHeaderInsertKeepsReveal(t, s, func(c *ui.Context) { renderChat(c, s) })
}

func TestBeeFloatHeaderInsertKeepsReveal(t *testing.T) {
	s := newLeadRevealState()
	s.threads[0].Provider = "claude"
	bee := SwarmBee{ThreadID: "lead", Name: "Birch", Status: "running"}
	testHeaderInsertKeepsReveal(t, s, func(c *ui.Context) { renderBeeTimeline(c, s, bee) })
}
