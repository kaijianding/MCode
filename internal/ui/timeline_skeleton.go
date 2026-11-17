package mcodeui

import (
	"strconv"

	"github.com/egoist/mygo/ui"
)

func renderSkeletonRows(c *ui.Context, key string, rows int, rowHeight, gap float32) {
	widths := [...]float32{78, 56, 70}
	ui.Column(c.Key(key)).FillWidth().Gap(gap).Children(func() {
		for i := 0; i < rows; i++ {
			line := ui.Box(c.Key(key + "-" + strconv.Itoa(i))).
				WidthPercent(widths[i%len(widths)]).Height(rowHeight).
				Radius(timelineSkeletonRadius)
			drawSkeletonLine(c, line)
		}
	})
}

func drawSkeletonLine(c *ui.Context, line ui.Element) {
	color := ThemeHover(c).Alpha(timelineSkeletonFillAlpha)
	reduced := reduceMotion(c)
	line.Draw(func(p *ui.Painter, r ui.Rect) {
		alpha := float32(1)
		if !reduced {
			phase := float32(p.Now().UnixNano()%int64(2*motionSweep)) / float32(motionSweep)
			if phase > 1 {
				phase = 2 - phase
			}
			alpha = 0.55 + 0.45*motionEaseStandard(phase)
			p.AnimationFrameRate(30)
		}
		p.Fill(r, color.Alpha(alpha), timelineSkeletonRadius)
	})
}

// 骨架行：用户消息右对齐、助手消息左对齐、工具行居中偏左。
const (
	timelineSkeletonUserWidth      float32 = 40
	timelineSkeletonAssistantWidth float32 = 78
	timelineSkeletonToolWidth      float32 = 56
	timelineSkeletonLineHeight     float32 = 14
	timelineSkeletonToolHeight     float32 = 12
	timelineSkeletonRadius         float32 = RadiusSM
	timelineSkeletonFillAlpha      float32 = 0.35
)

// renderTimelineSkeleton 画 3 条呼吸中的骨架行，宽度分别为父容器的 40% / 78% / 56%。
// 减少动效时不呼吸，停在 35% 的静态填充上。
func renderTimelineSkeleton(c *ui.Context) {
	rows := []struct {
		key    string
		width  float32
		height float32
		align  ui.Align
	}{
		{key: "user", width: timelineSkeletonUserWidth, height: timelineSkeletonLineHeight, align: ui.End},
		{key: "assistant", width: timelineSkeletonAssistantWidth, height: timelineSkeletonLineHeight, align: ui.Start},
		{key: "tool", width: timelineSkeletonToolWidth, height: timelineSkeletonToolHeight, align: ui.Start},
	}

	ui.Column(c.Key("timeline-skeleton")).
		Gap(blockGap).
		Children(func() {
			for _, row := range rows {
				line := ui.Box(c.Key("timeline-skeleton-" + row.key)).
					WidthPercent(row.width).
					Height(row.height).
					Radius(timelineSkeletonRadius).
					AlignSelf(row.align)
				drawSkeletonLine(c, line)
			}
		})
}

// renderTimelineSkeletonLayer 在聊天区顶部铺骨架层；数据落地后用 motionFast 淡出。
// 骨架是绝对定位的 PassThrough 层，不参与布局、不改变已有滚动位置。
func renderTimelineSkeletonLayer(c *ui.Context, s *chatState, threadID string, hasMessages bool) {
	s.mu.Lock()
	loading := threadID != "" && s.timelineLoadingThread == threadID && s.timelineLoadErrors[threadID] == ""
	hasError := s.timelineLoadErrors[threadID] != ""
	s.mu.Unlock()
	target := float32(0)
	if !hasMessages && loading {
		target = 1
	}
	if hasError {
		return
	}

	layer := ui.Column(c.Key("timeline-skeleton-layer")).
		Absolute().
		Left(0).
		Top(0).
		Right(0).
		Padding(Space5, Space7).
		PassThrough().
		IgnoreInput(true)
	alpha := layer.AnimateWith("timeline-skeleton-layer/alpha", target, animateDuration(c, motionFast), motionEaseStandard)
	layer.Opacity(alpha)
	if alpha <= 0.01 {
		return
	}
	layer.Children(func() {
		renderTimelineSkeleton(c)
	})
}
