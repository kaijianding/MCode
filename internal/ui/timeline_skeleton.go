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
				Radius(timelineSkeletonRadius).Background(ThemeHover(c).Alpha(timelineSkeletonFillAlpha))
			if reduceMotion(c) {
				line.Opacity(1)
				continue
			}
			line.Opacity(line.Keyframes(key+"-breath", []ui.Keyframe{
				{At: 0, Value: 0.55, Ease: motionEaseStandard},
				{At: motionSweep, Value: 1, Ease: motionEaseStandard},
			}, ui.KeyframeOptions{Repeat: ui.RepeatForever, Alternate: true}))
		}
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
					Background(ThemeHover(c).Alpha(timelineSkeletonFillAlpha)).
					AlignSelf(row.align)
				// 透明度在 0.55 与 1 之间来回呼吸：Alternate 让每遍反向播放。
				// 减少动效时不注册循环关键帧，直接停在 35% 填充（透明度 1）。
				if reduceMotion(c) {
					line.Opacity(1)
					continue
				}
				breath := line.Keyframes("timeline-skeleton-breath", []ui.Keyframe{
					{At: 0, Value: 0.55, Ease: motionEaseStandard},
					{At: motionSweep, Value: 1, Ease: motionEaseStandard},
				}, ui.KeyframeOptions{Repeat: ui.RepeatForever, Alternate: true})
				line.Opacity(breath)
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
