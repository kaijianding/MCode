package mcodeui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func TestPRReviewCardParseAndTitle(t *testing.T) {
	items, ok := parsePRReviewItems(`[{"path":"internal/a.go","line":12,"body":"空指针风险"},{"path":"internal/b.go","line":0,"body":"缺少锁保护"}]`)
	if !ok || len(items) != 2 {
		t.Fatalf("期望解析出 2 条问题，得到 ok=%v len=%d", ok, len(items))
	}
	if items[0].Path != "internal/a.go" || items[0].Line != 12 || items[0].Body != "空指针风险" {
		t.Fatalf("第 1 条解析异常：%+v", items[0])
	}

	empty, ok := parsePRReviewItems(`[]`)
	if !ok || len(empty) != 0 {
		t.Fatalf("期望 [] 解析成功且为空，得到 ok=%v len=%d", ok, len(empty))
	}

	for _, raw := range []string{"", "null", "{", `{"path":"a.go"}`} {
		if got, ok := parsePRReviewItems(raw); ok {
			t.Fatalf("输入 %q 应解析失败，却得到 %+v", raw, got)
		}
	}

	if got := prReviewCardTitle(0); got != "没有发现问题" {
		t.Fatalf("空数组标题 = %q", got)
	}
	if got := prReviewCardTitle(2); got != "2 个问题" {
		t.Fatalf("两条问题标题 = %q", got)
	}
	if got := prReviewLocationText("internal/a.go", 12); got != "internal/a.go:12" {
		t.Fatalf("带行号定位 = %q", got)
	}
	if got := prReviewLocationText("internal/b.go", 0); got != "internal/b.go" {
		t.Fatalf("无行号定位 = %q", got)
	}
	if got := prReviewLocationText("   ", 5); got != "" {
		t.Fatalf("空路径定位 = %q", got)
	}
}

func TestPRReviewCardTwoIssuesInAssistantMessage(t *testing.T) {
	resetRuntimeTheme(t)
	setAppReduceMotion(t, true)

	fake := NewFakeClient()
	fake.SetThreadWorkDir(t.TempDir())
	fake.SetFileContents(map[string]FileContent{
		"internal/a.go": {Path: "internal/a.go", Kind: "text", Text: "package a\n"},
	})
	s := newChatState(fake)
	s.selectedThreadID = "thread-1"
	s.threads = []Thread{{ID: "thread-1"}}
	s.messages = []Message{{
		ID:        "assistant-1",
		Role:      "assistant",
		Kind:      "text",
		TurnIndex: 1,
		IsFinal:   true,
		Text: "审查完成。\n\n```pr-review\n" +
			`[{"path":"internal/a.go","line":12,"body":"未检查 nil 返回值可能导致崩溃"},{"path":"internal/b.go","line":0,"body":"循环内重复加锁存在死锁风险"}]` +
			"\n```\n",
	}}
	s.markRevealBaselineLocked(s.messages)

	tt := ui.NewTester(func(c *ui.Context) { renderChat(c, s) }, 1000, 700)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()

	allText := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{
		"2 个问题",
		"internal/a.go:12",
		"未检查 nil 返回值可能导致崩溃",
		"internal/b.go",
		"循环内重复加锁存在死锁风险",
	} {
		if !strings.Contains(allText, want) {
			t.Fatalf("渲染输出缺少 %q：%v", want, tt.Texts())
		}
	}
	if strings.Contains(allText, `"path"`) {
		t.Fatalf("审查卡片不应露出原始 JSON 字段：%v", tt.Texts())
	}

	if err := tt.Click("internal/a.go:12"); err != nil {
		t.Fatalf("点击定位行失败：%v", err)
	}
	if !isFileManagerOpen(s) {
		t.Fatal("点击定位行后应打开文件面板")
	}
	st := getFileManagerState(s)
	fileManagerMu.Lock()
	active := st.active
	tab := st.findTabLocked("internal/a.go")
	var hlFirst, hlLast int
	var reveal bool
	if tab != nil {
		hlFirst, hlLast, reveal = tab.hlFirst, tab.hlLast, tab.reveal
	}
	fileManagerMu.Unlock()
	if active != "internal/a.go" || hlFirst != 12 || hlLast != 12 || !reveal {
		t.Fatalf("打开的文件或定位行号不符：active=%q first=%d last=%d reveal=%v", active, hlFirst, hlLast, reveal)
	}
}

func TestPRReviewCardEmptyArray(t *testing.T) {
	resetRuntimeTheme(t)
	setAppReduceMotion(t, true)

	s := newChatState(NewFakeClient())
	source := "```pr-review\n[]\n```\n"
	tt := ui.NewTester(func(c *ui.Context) {
		ensureChatMarkdown(c, s)
		ensureCodeBlockContainer(s)
		markdown.View(c, source, &s.mdStaticOpts)
	}, 600, 300)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()

	allText := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(allText, "没有发现问题") {
		t.Fatalf("空数组应渲染「没有发现问题」：%v", tt.Texts())
	}
	if strings.Contains(allText, "[]") {
		t.Fatalf("空数组卡片不应渲染正文 JSON：%v", tt.Texts())
	}

	if err := tt.Click("复制代码"); err != nil {
		t.Fatalf("点击复制按钮失败：%v", err)
	}
	if strings.TrimSpace(tt.Clipboard()) != "[]" {
		t.Fatalf("复制内容 = %q，期望 []", tt.Clipboard())
	}
	if s.toastMessage != "已复制" {
		t.Fatalf("复制提示 = %q，期望 已复制", s.toastMessage)
	}
}

func TestPRReviewCardInvalidJSONAndStreamingFallback(t *testing.T) {
	resetRuntimeTheme(t)
	setAppReduceMotion(t, true)

	s := newChatState(NewFakeClient())
	invalidSource := "```pr-review\n{not-valid-json\n```\n"
	ttInvalid := ui.NewTester(func(c *ui.Context) {
		ensureChatMarkdown(c, s)
		ensureCodeBlockContainer(s)
		markdown.View(c, invalidSource, &s.mdStaticOpts)
	}, 600, 300)
	ttInvalid.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	ttInvalid.Frame()

	invalidText := strings.Join(ttInvalid.Texts(), "\n")
	if !strings.Contains(invalidText, "pr-review") || !strings.Contains(invalidText, "not-valid-json") {
		t.Fatalf("非法 JSON 应退回普通代码块显示：%v", ttInvalid.Texts())
	}
	if strings.Contains(invalidText, "没有发现问题") || strings.Contains(invalidText, "个问题") {
		t.Fatalf("非法 JSON 不应显示审查卡片标题：%v", ttInvalid.Texts())
	}

	streamingSource := "```pr-review\n[{\"path\":\"a.go\",\"line\":1,\"body\":\"流式未闭合\"}]\n"
	ttStreaming := ui.NewTester(func(c *ui.Context) {
		ensureChatMarkdown(c, s)
		ensureCodeBlockContainer(s)
		markdown.View(c, streamingSource, &s.mdOpts)
	}, 600, 300)
	ttStreaming.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	ttStreaming.Frame()

	streamingText := strings.Join(ttStreaming.Texts(), "\n")
	if !strings.Contains(streamingText, "pr-review") || strings.Contains(streamingText, "1 个问题") {
		t.Fatalf("流式未闭合代码块应原样展示，不提前渲染卡片：%v", ttStreaming.Texts())
	}
}

func TestPRReviewCardHoverMotionSettles(t *testing.T) {
	resetRuntimeTheme(t)
	setAppReduceMotion(t, false)

	s := newChatState(NewFakeClient())
	source := "```pr-review\n" + `[{"path":"internal/a.go","line":12,"body":"问题正文"}]` + "\n```\n"
	tt := ui.NewTester(func(c *ui.Context) {
		ensureChatMarkdown(c, s)
		ensureCodeBlockContainer(s)
		markdown.View(c, source, &s.mdStaticOpts)
	}, 600, 300)
	tt.SetPreferences(ui.Preferences{ReduceMotion: false, TextScale: 1})
	tt.Frame()

	rect, ok := tt.Find("internal/a.go:12")
	if !ok {
		t.Fatalf("未找到定位行元素：%v", tt.Texts())
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Advance(DurSnap + 50*time.Millisecond)
	if tt.FramePending() {
		t.Fatal("悬停过渡结束后不应持续请求重绘")
	}
	tt.Move(0, 0)
	tt.Advance(DurSnap + 50*time.Millisecond)
	if tt.FramePending() {
		t.Fatal("移出悬停并结束过渡后不应持续请求重绘")
	}
}
