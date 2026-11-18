package mcodeui

import (
	"bytes"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mcode/internal/testenv"
)

// hoverRow 把指针移到 title 所在会话行的左侧，并渲染一帧。
func hoverRow(t *testing.T, tt *ui.Tester, title string) {
	t.Helper()
	rect, ok := tt.Find(title)
	if !ok {
		t.Fatalf("expected to find thread title %q, texts: %q", title, tt.Texts())
	}
	tt.Move(rect.X+8, rect.Y+rect.H/2)
	tt.Frame()
}

// cloneImage 复制当前帧像素：Tester.Image 复用的是同一块缓冲。
func cloneImage(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

// bandDiffers 比较两帧在矩形区域内的像素是否不同（区域先做边界裁剪）。
func bandDiffers(a, b *image.RGBA, r image.Rectangle) bool {
	r = r.Intersect(a.Bounds()).Intersect(b.Bounds())
	if r.Empty() {
		return false
	}
	row := r.Dx() * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		ao, bo := a.PixOffset(r.Min.X, y), b.PixOffset(r.Min.X, y)
		if !bytes.Equal(a.Pix[ao:ao+row], b.Pix[bo:bo+row]) {
			return true
		}
	}
	return false
}

// titleBand 返回标题所占的横向条带：从侧边栏左缘到标题被裁剪后的右缘。
func titleBand(tt *ui.Tester, title string) (image.Rectangle, bool) {
	rect, ok := tt.Find(title)
	if !ok {
		return image.Rectangle{}, false
	}
	b := tt.Image().Bounds()
	return image.Rect(b.Min.X, int(rect.Y), int(rect.X+rect.W), int(rect.Y+rect.H)), true
}

// TestSidebarHoverCardAppearsAfterDelay 验证悬停卡：不足 650ms 预热不出现，
// 预热结束后出现并带上标题、项目、模型、状态、最近活动等字段，移开后消失。
func TestSidebarHoverCardAppearsAfterDelay(t *testing.T) {
	resetSidebarHoverCardForTest()
	fake := NewFakeClient()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "mcode-test", Path: "/path/to/hovercard"}},
		map[string][]Thread{"p1": {{
			ID:        "t1",
			ProjectID: "p1",
			Title:     "悬停卡会话",
			Provider:  "claude",
			Model:     "claude-3-7-sonnet",
			UpdatedAt: time.Now().Add(-2 * time.Hour),
		}}},
	)
	_, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("悬停卡会话") })

	hoverRow(t, tt, "悬停卡会话")

	// 预热时长之内不出现卡片。
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if tt.HasText("最近活动") || tt.HasText("Claude · claude-3-7-sonnet") {
		t.Fatalf("hover card should not show before the warmup, texts: %q", tt.Texts())
	}

	// 越过 650ms 预热后出现，内容来自会话已有字段。
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("最近活动") {
		t.Fatalf("expected hover card after the warmup, texts: %q", tt.Texts())
	}
	for _, want := range []string{"悬停卡会话", "hovercard", "Claude · claude-3-7-sonnet", "空闲", "2小时前", "模型"} {
		if !tt.HasText(want) {
			t.Fatalf("expected hover card to contain %q, texts: %q", want, tt.Texts())
		}
	}
	if tt.HasText("待审批") {
		t.Fatalf("idle thread should not show 待审批")
	}

	// 指针移开后卡片消失（宽限期 180ms 之后）。
	tt.Move(900, 600)
	tt.Frame()
	if !tt.HasText("最近活动") {
		t.Fatalf("hover card should stay during the grace period, texts: %q", tt.Texts())
	}
	tt.Advance(250 * time.Millisecond)
	tt.Frame()
	if tt.HasText("最近活动") {
		t.Fatalf("hover card should be gone after leaving, texts: %q", tt.Texts())
	}
}

func TestSidebarHoverCardAIDurationAndContext(t *testing.T) {
	resetSidebarHoverCardForTest()
	fake := NewFakeClient()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "mcode-test", Path: "/path/to/hovercard"}},
		map[string][]Thread{"p1": {{ID: "t1", ProjectID: "p1", Title: "统计卡会话", AIDuration: 65 * time.Second, TotalDuration: 80 * time.Second, ContextUsedTokens: 90_000, ContextWindow: 200_000}}},
	)
	_, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("统计卡会话") })
	hoverRow(t, tt, "统计卡会话")
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	for _, want := range []string{"AI 用时", "1m5s", "总用时", "1m20s", "上下文", "90k / 200k · 45%"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("expected hover card to contain %q, texts: %q", want, tt.Texts())
		}
	}

	resetSidebarHoverCardForTest()
	zero := NewFakeClient()
	zero.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "mcode-test", Path: "/path/to/hovercard"}},
		map[string][]Thread{"p1": {{ID: "t2", ProjectID: "p1", Title: "空统计会话"}}},
	)
	_, zeroView := buildRoot(zero)
	zeroTT := ui.NewTester(zeroView, 1000, 700)
	waitFor(t, zeroTT, func() bool { return zeroTT.HasText("空统计会话") })
	hoverRow(t, zeroTT, "空统计会话")
	zeroTT.Advance(700 * time.Millisecond)
	zeroTT.Frame()
	if _, ok := zeroTT.Find("状态"); !ok {
		t.Fatalf("expected zero stats hover card, texts: %q", zeroTT.Texts())
	}
	for _, label := range []string{"AI 用时", "上下文"} {
		if _, ok := zeroTT.Find(label); ok {
			t.Fatalf("zero stats should hide %q, texts: %q", label, zeroTT.Texts())
		}
	}
}

func TestSidebarHoverCardHidesEqualTotalDuration(t *testing.T) {
	resetSidebarHoverCardForTest()
	fake := NewFakeClient()
	fake.SetProjectsAndThreads([]Project{{ID: "p1", Name: "mcode-test", Path: "/path/to/hovercard"}}, map[string][]Thread{"p1": {{ID: "t1", ProjectID: "p1", Title: "相同用时", AIDuration: time.Minute, TotalDuration: time.Minute}}})
	_, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("相同用时") })
	hoverRow(t, tt, "相同用时")
	tt.Advance(700 * time.Millisecond)
	tt.Frame()
	if _, ok := tt.Find("总用时"); ok {
		t.Fatalf("equal duration should hide total duration: %q", tt.Texts())
	}
}

// TestSidebarCompactTitleMarquee 验证紧凑模式下超长标题悬停后横向滚动、
// 短标题不滚动，且未悬停时不挂动画。
func TestSidebarCompactTitleMarquee(t *testing.T) {
	longTitle := strings.Repeat("紧凑模式超长会话标题", 4)
	shortTitle := "短标题"

	build := func(t *testing.T, title string) (*ui.Tester, *chatState) {
		resetSidebarHoverCardForTest()
		fake := NewFakeClient()
		fake.threads["p1"] = []Thread{{
			ID:        "t1",
			ProjectID: "p1",
			Title:     title,
			UpdatedAt: time.Now(),
		}}
		state, view := buildRoot(fake)
		st := NewSettingsState(settingsTestDir(t), nil, state)
		st.cfg.Appearance.SidebarCompact = true
		setChatSettings(state, st)
		tt := ui.NewTester(view, 1000, 700)
		waitFor(t, tt, func() bool { return tt.HasText(title) })
		return tt, state
	}

	t.Run("超长标题悬停后滚动", func(t *testing.T) {
		tt, _ := build(t, longTitle)

		bandBefore, ok := titleBand(tt, longTitle)
		if !ok {
			t.Fatalf("expected to find the title, texts: %q", tt.Texts())
		}
		// 未悬停：同一帧重复渲染像素不变，说明没有常驻动画。
		first := cloneImage(tt.Image())
		tt.Advance(800 * time.Millisecond)
		tt.Frame()
		if bandDiffers(first, tt.Image(), bandBefore) {
			t.Fatalf("title should not scroll while the pointer is outside the row")
		}

		hoverRow(t, tt, longTitle)
		tt.Advance(time.Second)
		tt.Frame()
		band, ok := titleBand(tt, longTitle)
		if !ok {
			t.Fatalf("expected to find the title after hover, texts: %q", tt.Texts())
		}
		first = cloneImage(tt.Image())
		tt.Advance(800 * time.Millisecond)
		tt.Frame()
		if !bandDiffers(first, tt.Image(), band) {
			t.Fatalf("expected the long title to scroll on hover")
		}
	})

	t.Run("短标题不滚动", func(t *testing.T) {
		tt, _ := build(t, shortTitle)
		hoverRow(t, tt, shortTitle)
		tt.Advance(time.Second)
		tt.Frame()
		band, ok := titleBand(tt, shortTitle)
		if !ok {
			t.Fatalf("expected to find the title, texts: %q", tt.Texts())
		}
		first := cloneImage(tt.Image())
		tt.Advance(800 * time.Millisecond)
		tt.Frame()
		if bandDiffers(first, tt.Image(), band) {
			t.Fatalf("short title should not scroll")
		}
	})
}

// TestSidebarCompactTitleEllipsis 验证紧凑标题与框架的省略号文本渲染一致。
func TestSidebarCompactTitleEllipsis(t *testing.T) {
	for _, tc := range []struct {
		name  string
		title string
	}{
		{"长标题", strings.Repeat("很长的会话标题", 8)},
		{"短标题", "短标题"},
		{"无空格英文", strings.Repeat("abcdefgh", 20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := ui.NewTester(func(c *ui.Context) {
				ui.Row(c).Width(100).Children(func() {
					renderSidebarScrollingTitle(c, "title", tc.title, false, 0, FontSizeThreadTitle, 0, ThemePrimaryText(c))
				})
			}, 120, 40)
			expected := ui.NewTester(func(c *ui.Context) {
				ui.Row(c).Width(100).Children(func() {
					ui.Row(c.Key("title")).Grow(1).Shrink(1).MinWidth(0).ClipX().Children(func() {
						ui.Text(c, tc.title).SingleLine().Grow(1).Shrink(1).MinWidth(0).Ellipsis("…").
							FontSize(sidebarFont(FontSizeThreadTitle)).TextColor(ThemePrimaryText(c))
					})
				})
			}, 120, 40)
			actual.Frame()
			expected.Frame()
			if !bytes.Equal(actual.Image().Pix, expected.Image().Pix) {
				t.Fatal("标题渲染与省略号文本不一致")
			}
		})
	}
}

// TestSidebarHoverCardFullTitle 验证悬停卡标题可显示超过两行。
func TestSidebarHoverCardFullTitle(t *testing.T) {
	testenv.TempHome(t)
	state, _ := buildRoot(NewFakeClient())
	title := strings.Repeat("这是可以换行的完整会话标题 ", 12)
	th := Thread{ID: "title-test", Title: title}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(sidebarHoverCardWidth).Children(func() {
			renderSidebarHoverCardBody(c, state, th)
		})
	}, 400, 900)
	tt.Frame()
	rect, ok := tt.Find(title)
	if !ok || rect.H <= scaledFont(FontSizeThreadTitle)*2 {
		t.Fatalf("完整标题应超过两行，尺寸: %+v，文本: %q", rect, tt.Texts())
	}
}

// TestSidebarMarqueeDecision 验证跑马灯的判定：未悬停不滚、溢出不足 8px 不滚、
// 溢出足够且悬停才滚，尺寸还没量到时也不滚。
func TestSidebarMarqueeDecision(t *testing.T) {
	cases := []struct {
		name     string
		natural  float32
		avail    float32
		reserved float32
		hovered  bool
		want     bool
	}{
		{"未悬停", 200, 100, 0, false, false},
		{"溢出不足 8px", 107, 100, 0, true, false},
		{"刚好超过 8px", 109, 100, 0, true, true},
		{"溢出很多", 400, 100, 0, true, true},
		{"还没量到尺寸", 0, 0, 0, true, false},
		// 预留宽度（行尾状态位/操作按钮）会吞掉可用宽度：100-60=40，150 溢出 110，应滚。
		{"扣掉预留宽度后溢出", 150, 100, 60, true, true},
		// 同一对尺寸：有预留时溢出 30px 应滚，无预留时正好装得下不滚。
		{"加上预留后才溢出", 120, 120, 30, true, true},
		{"无预留时不溢出", 120, 120, 0, true, false},
	}
	for _, tc := range cases {
		if got := sidebarMarqueeActive(tc.natural, tc.avail, tc.reserved, tc.hovered); got != tc.want {
			t.Fatalf("%s: sidebarMarqueeActive(%v, %v, %v, %v) = %v, want %v",
				tc.name, tc.natural, tc.avail, tc.reserved, tc.hovered, got, tc.want)
		}
	}
}

// TestSidebarHoverCardNoShowWithoutHover 回归测试：没有任何鼠标移动时，即使包级状态里
// 残留着「同一会话行刚离开」的记录（上一个窗口/上一个测试留下的），新窗口翻页推进 2 秒
// 也不能把悬停卡显示出来——卡片只在真实悬停满预热后出现。
func TestSidebarHoverCardNoShowWithoutHover(t *testing.T) {
	resetSidebarHoverCardForTest()
	// 伪造上一个窗口的残留状态：threadID 与下面构造的会话相同，离开时刻就在刚刚。
	seedSidebarHoverLeftForTest("t1", "p1", time.Now())

	fake := NewFakeClient()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "mcode-test", Path: "/path/to/hovercard"}},
		map[string][]Thread{"p1": {{
			ID:        "t1",
			ProjectID: "p1",
			Title:     "未悬停会话",
			Provider:  "claude",
			Model:     "claude-3-7-sonnet",
			UpdatedAt: time.Now().Add(-2 * time.Hour),
		}}},
	)
	_, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("未悬停会话") })

	// 不移动鼠标，只推进时间：卡片内容不得出现。
	// 判据只能用只在悬停卡里出现的字段文案；「2小时前」这类相对时间也会作为会话行行尾状态位显示。
	tt.Advance(2 * time.Second)
	tt.Frame()
	for _, unwanted := range []string{"最近活动", "状态"} {
		if tt.HasText(unwanted) {
			t.Fatalf("hover card content %q should not appear without a real hover, texts: %q", unwanted, tt.Texts())
		}
	}
}

// seedSidebarHoverLeftForTest 写入「某个会话行刚刚失去悬停」的包级状态，
// 用来模拟上一个窗口残留的悬停记录（新窗口必须无视它）。
func seedSidebarHoverLeftForTest(threadID, projectID string, leftAt time.Time) {
	sidebarHover.mu.Lock()
	sidebarHover.threadID = threadID
	sidebarHover.projectID = projectID
	sidebarHover.enteredAt = leftAt.Add(-time.Second)
	sidebarHover.leftAt = leftAt
	sidebarHover.mu.Unlock()
}

// TestSidebarDeleteThreadDialogCallbacks 验证迁移后的删除会话确认弹窗：
// 文案保持原样，取消只关弹窗，确认走原有删除流程。
func TestSidebarDeleteThreadDialogCallbacks(t *testing.T) {
	fake := NewFakeClient()
	state, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("测试会话1") })

	state.openDeleteModal("t1", "测试会话1")
	tt.Frame()
	if !state.deleteModalOpen {
		t.Fatalf("expected deleteModalOpen to be true")
	}
	if !tt.HasText("删除后无法恢复，确定要删除会话 \"测试会话1\" 吗？") {
		t.Fatalf("expected the original delete warning, texts: %q", tt.Texts())
	}
	if !tt.HasText("删除会话") {
		t.Fatalf("expected the delete dialog title, texts: %q", tt.Texts())
	}

	// 取消：关闭弹窗，会话仍在。
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click 取消 failed: %v", err)
	}
	tt.Frame()
	state.mu.Lock()
	open := state.deleteModalOpen
	state.mu.Unlock()
	if open {
		t.Fatalf("expected deleteModalOpen to be false after cancel")
	}
	if !tt.HasText("测试会话1") {
		t.Fatalf("cancel should not delete the thread, texts: %q", tt.Texts())
	}

	// 确认：走原有删除流程，会话从侧边栏消失。
	state.openDeleteModal("t1", "测试会话1")
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatalf("click 删除 failed: %v", err)
	}
	waitFor(t, tt, func() bool { return !tt.HasText("测试会话1") })
}

// TestSidebarRemoveProjectDialogCallbacks 验证迁移后的移除项目确认弹窗：
// 文案保持原样，取消只关弹窗，确认走原有移除流程。
func TestSidebarRemoveProjectDialogCallbacks(t *testing.T) {
	fake := NewFakeClient()
	state, view := buildRoot(fake)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText("mcode-test") })

	openRemove := func() {
		state.mu.Lock()
		state.removeProjModalOpen = true
		state.removeProjID = "p1"
		state.removeProjName = "mcode-test"
		state.mu.Unlock()
		state.invalidate()
		tt.Frame()
	}

	openRemove()
	if !tt.HasText("确定移除项目 \"mcode-test\" 吗？该项目的会话记录会一并从 mcode 中删除，磁盘上的文件不会被删除。") {
		t.Fatalf("expected the original remove warning, texts: %q", tt.Texts())
	}
	if !tt.HasText("移除项目") {
		t.Fatalf("expected the remove dialog title, texts: %q", tt.Texts())
	}

	// 取消：关闭弹窗，项目仍在。
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click 取消 failed: %v", err)
	}
	tt.Frame()
	state.mu.Lock()
	open := state.removeProjModalOpen
	state.mu.Unlock()
	if open {
		t.Fatalf("expected removeProjModalOpen to be false after cancel")
	}
	if !tt.HasText("mcode-test") {
		t.Fatalf("cancel should not remove the project, texts: %q", tt.Texts())
	}

	// 确认：走原有移除流程，项目从侧边栏消失。
	openRemove()
	if err := tt.Click("移除"); err != nil {
		t.Fatalf("click 移除 failed: %v", err)
	}
	waitFor(t, tt, func() bool { return !tt.HasText("mcode-test") })
}

// TestSidebarMarqueeDrawDrivenAndStopsWhenIdle 验证跑马灯改为 Draw 驱动：
// 1. 悬停且溢出时不挂组件级 Keyframes，推进时钟只重绘不重建组件树，并产生像素滚动；
// 2. 不悬停或开启减少动效时，推进时钟后不再请求下一帧；
// 3. sidebarMarqueePhase 保留起始延迟与往返相位。
func TestSidebarMarqueeDrawDrivenAndStopsWhenIdle(t *testing.T) {
	longTitle := strings.Repeat("侧栏超长跑马灯标题", 5)

	t.Run("悬停且溢出时不挂Keyframes且不重建组件树", func(t *testing.T) {
		resetSidebarHoverCardForTest()
		setAppReduceMotion(t, false)
		builds := 0
		settled := false
		tt := ui.NewTester(func(c *ui.Context) {
			builds++
			ui.Row(c).Width(100).Children(func() {
				el := renderSidebarScrollingTitle(c, "marquee-test", longTitle, true, 0, FontSizeThreadTitle, 0, ThemePrimaryText(c))
				settled = el.Settled("marquee")
			})
		}, 120, 40)
		// 第 2 帧拿到布局宽高并挂载 Draw 跑马灯。
		tt.Frame()
		tt.Frame()
		if !settled {
			t.Fatal("悬停且溢出时不应再挂组件级 Keyframes(\"marquee\")")
		}
		sidebarMarqueeMu.Lock()
		marqueeCount := len(sidebarMarqueeStart)
		sidebarMarqueeMu.Unlock()
		if marqueeCount == 0 {
			t.Fatal("悬停且溢出时应进入 Draw 跑马灯分支")
		}

		beforeBuilds := builds
		before := cloneImage(tt.Image())
		tt.Advance(sidebarMarqueeDelay + 800*time.Millisecond)
		if builds != beforeBuilds {
			t.Fatalf("跑马灯滚动期间不应重建组件树: builds %d -> %d", beforeBuilds, builds)
		}
		if bytes.Equal(before.Pix, tt.Image().Pix) {
			t.Fatal("越过起始延迟后跑马灯像素应发生变化")
		}
	})

	for _, tc := range []struct {
		name    string
		hovered bool
		reduced bool
	}{
		{"未悬停", false, false},
		{"减少动效", true, true},
	} {
		t.Run(tc.name+"推进时钟后不再请求下一帧", func(t *testing.T) {
			resetSidebarHoverCardForTest()
			setAppReduceMotion(t, tc.reduced)
			builds := 0
			tt := ui.NewTester(func(c *ui.Context) {
				builds++
				ui.Row(c).Width(100).Children(func() {
					renderSidebarScrollingTitle(c, "marquee-idle", longTitle, tc.hovered, 0, FontSizeThreadTitle, 0, ThemePrimaryText(c))
				})
			}, 120, 40)
			tt.Frame()
			tt.Frame()
			beforeBuilds := builds
			before := cloneImage(tt.Image())
			tt.Advance(2 * time.Second)
			if tt.FramePending() {
				t.Fatalf("%s 推进时钟后不应再请求下一帧", tc.name)
			}
			if builds != beforeBuilds {
				t.Fatalf("%s 推进时钟不应重建组件树: builds %d -> %d", tc.name, beforeBuilds, builds)
			}
			if !bytes.Equal(before.Pix, tt.Image().Pix) {
				t.Fatalf("%s 推进时钟后画面不应变化", tc.name)
			}
		})
	}

	t.Run("离开悬停后停止请求下一帧", func(t *testing.T) {
		resetSidebarHoverCardForTest()
		setAppReduceMotion(t, false)
		hovered := true
		builds := 0
		tt := ui.NewTester(func(c *ui.Context) {
			builds++
			ui.Row(c).Width(100).Children(func() {
				renderSidebarScrollingTitle(c, "marquee-leave", longTitle, hovered, 0, FontSizeThreadTitle, 0, ThemePrimaryText(c))
			})
		}, 120, 40)
		tt.Frame()
		tt.Frame()
		beforeHover := cloneImage(tt.Image())
		tt.Advance(sidebarMarqueeDelay + 200*time.Millisecond)
		if bytes.Equal(beforeHover.Pix, tt.Image().Pix) {
			t.Fatal("悬停溢出标题应通过 p.After 驱动跑马灯重画")
		}
		hovered = false
		tt.Frame()
		afterLeave := cloneImage(tt.Image())
		beforeBuilds := builds
		tt.Advance(2 * time.Second)
		if tt.FramePending() {
			t.Fatal("离开悬停并推进时钟后不应再请求下一帧")
		}
		if builds != beforeBuilds {
			t.Fatalf("离开悬停后不应再重建组件树: builds %d -> %d", beforeBuilds, builds)
		}
		if !bytes.Equal(afterLeave.Pix, tt.Image().Pix) {
			t.Fatal("离开悬停后画面不应继续滚动")
		}
	})

	t.Run("往返相位与起始延迟", func(t *testing.T) {
		dur := 4 * time.Second
		if got := sidebarMarqueePhase(0, dur); got != 0 {
			t.Fatalf("t=0: got %v, want 0", got)
		}
		if got := sidebarMarqueePhase(sidebarMarqueeDelay, dur); got != 0 {
			t.Fatalf("t=delay: got %v, want 0", got)
		}
		if got := sidebarMarqueePhase(sidebarMarqueeDelay+dur/2, dur); got != 0.5 {
			t.Fatalf("t=delay+dur/2: got %v, want 0.5", got)
		}
		if got := sidebarMarqueePhase(sidebarMarqueeDelay+dur, dur); got != 1 {
			t.Fatalf("t=delay+dur: got %v, want 1", got)
		}
		if got := sidebarMarqueePhase(sidebarMarqueeDelay+dur+dur/2, dur); got != 0.5 {
			t.Fatalf("t=delay+1.5*dur: got %v, want 0.5", got)
		}
		if got := sidebarMarqueePhase(sidebarMarqueeDelay+2*dur, dur); got != 0 {
			t.Fatalf("t=delay+2*dur: got %v, want 0", got)
		}
	})
}

// TestSidebarScrollingTitleUsesCallerStyle 验证跑马灯与静态文本按调用方传入的字号、字重与颜色布局和绘制。
// ui.Tester 不直接暴露元素字号与颜色属性，因此通过布局溢出判定（dormant 12pt vs thread 13pt）
// 与 Draw 自绘像素色差验证传入样式生效。
func TestSidebarScrollingTitleUsesCallerStyle(t *testing.T) {
	resetSidebarHoverCardForTest()
	setAppReduceMotion(t, false)
	title := strings.Repeat("侧栏样式测试标题", 4)

	measureWidth := func(size float32, weight int) float32 {
		var w float32
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).Width(800).Children(func() {
				el := renderSidebarScrollingTitle(c, "measure", title, true, 0, size, weight, ThemeDanger(c))
				w = el.LayoutBounds().W
			})
		}, 820, 40)
		tt.Frame()
		tt.Frame()
		return w
	}

	dormantW := measureWidth(FontSizeDormantTitle, 500)
	threadW := measureWidth(FontSizeThreadTitle, 500)
	if dormantW <= 0 || threadW <= dormantW+8 {
		t.Fatalf("expected dormant layout width (%v) to be >8px narrower than thread width (%v)", dormantW, threadW)
	}

	// 容器宽度恰好等于 dormant 自然宽度：按 dormant 字号布局时不溢出，按 thread 字号则溢出超过 8px。
	checkOverflow := func(size float32) bool {
		resetSidebarHoverCardForTest()
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).Width(dormantW).Children(func() {
				renderSidebarScrollingTitle(c, "overflow", title, true, 0, size, 500, ThemeDanger(c))
			})
		}, int(dormantW)+20, 40)
		tt.Frame()
		tt.Frame()
		before := cloneImage(tt.Image())
		tt.Advance(sidebarMarqueeDelay + 200*time.Millisecond)
		return !bytes.Equal(before.Pix, tt.Image().Pix)
	}
	if checkOverflow(FontSizeDormantTitle) {
		t.Fatal("dormant 字号下宽度未溢出，不应触发跑马灯")
	}
	if !checkOverflow(FontSizeThreadTitle) {
		t.Fatal("thread 字号下溢出超过 8px，应触发跑马灯")
	}

	// 悬停且溢出进入 Draw 跑马灯时，自绘颜色应使用调用方传入颜色。
	renderMarqueeImage := func(pickColor func(*ui.Context) ui.Color) *image.RGBA {
		resetSidebarHoverCardForTest()
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).Width(100).Children(func() {
				renderSidebarScrollingTitle(c, "color-test", title, true, 0, FontSizeDormantTitle, 500, pickColor(c))
			})
		}, 120, 40)
		tt.Frame()
		tt.Frame()
		return cloneImage(tt.Image())
	}
	redImg := renderMarqueeImage(ThemeDanger)
	blueImg := renderMarqueeImage(ThemeAccent)
	if bytes.Equal(redImg.Pix, blueImg.Pix) {
		t.Fatal("跑马灯自绘未应用调用方传入的颜色")
	}
	hasRed := false
	b := redImg.Bounds()
	for y := b.Min.Y; y < b.Max.Y && !hasRed; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			off := redImg.PixOffset(x, y)
			r, g, bl := int(redImg.Pix[off]), int(redImg.Pix[off+1]), int(redImg.Pix[off+2])
			if r-bl > 60 && r-g > 60 {
				hasRed = true
				break
			}
		}
	}
	if !hasRed {
		t.Fatal("跑马灯自绘像素中未找到调用方传入的红色")
	}
}

// TestSidebarCompactMarqueeStartsOnFirstHover 验证紧凑模式下运行中会话首次悬停后，
// 仅推进时钟并重画（不制造额外重建）即可在 sidebarMarqueeDelay 后启动跑马灯并持续请求下一帧。
func TestSidebarCompactMarqueeStartsOnFirstHover(t *testing.T) {
	resetSidebarHoverCardForTest()
	setAppReduceMotion(t, false)
	longTitle := strings.Repeat("紧凑模式运行中超长会话标题", 4)

	fake := NewFakeClient()
	fake.threads["p1"] = []Thread{{
		ID:        "t1",
		ProjectID: "p1",
		Title:     longTitle,
		IsRunning: true,
		UpdatedAt: time.Now(),
	}}
	state, view := buildRoot(fake)
	st := NewSettingsState(settingsTestDir(t), nil, state)
	st.cfg.Appearance.SidebarCompact = true
	setChatSettings(state, st)
	tt := ui.NewTester(view, 1000, 700)
	waitFor(t, tt, func() bool { return tt.HasText(longTitle) })

	// 只做一次指针移动触发悬停，不额外调 tt.Frame() 制造重建。
	rect, ok := tt.Find(longTitle)
	if !ok {
		t.Fatalf("expected to find thread title %q, texts: %q", longTitle, tt.Texts())
	}
	tt.Move(rect.X+8, rect.Y+rect.H/2)
	hoverAt := tt.Now()
	titleRect := image.Rect(int(rect.X), int(rect.Y), int(rect.X+rect.W), int(rect.Y+rect.H))
	before := cloneImage(tt.Image())

	// 悬停后只推进一次时钟到 sidebarMarqueeDelay + 100ms，只靠时钟驱动重画，不手动重建。
	tt.Advance(sidebarMarqueeDelay + 100*time.Millisecond)

	sidebarMarqueeMu.Lock()
	marqueeCount := len(sidebarMarqueeStart)
	var recordedStart time.Time
	for _, start := range sidebarMarqueeStart {
		recordedStart = start
		break
	}
	sidebarMarqueeMu.Unlock()
	if marqueeCount == 0 {
		t.Fatalf("expected sidebarMarqueeStart to record start time on first hover")
	}
	if recordedStart.Before(hoverAt) || recordedStart.Sub(hoverAt) > time.Second/30 {
		t.Fatalf("expected marquee start %v to equal hover time %v (within one frame)", recordedStart, hoverAt)
	}
	if !bandDiffers(before, tt.Image(), titleRect) {
		t.Fatalf("expected title pixels to start scrolling after sidebarMarqueeDelay")
	}
	mid := cloneImage(tt.Image())
	tt.Advance(200 * time.Millisecond)
	if !bandDiffers(mid, tt.Image(), titleRect) {
		t.Fatalf("expected marquee to keep scrolling after sidebarMarqueeDelay")
	}
}
