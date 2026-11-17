package mcodeui

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestShimmerTitleRepaintsWithoutRebuild(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		name := "running"
		if reduced {
			name = "reduce motion"
		}
		t.Run(name, func(t *testing.T) {
			setAppReduceMotion(t, reduced)
			builds := 0
			view := func(c *ui.Context) {
				builds++
				renderShimmerTitle(c, "shimmer", "扫光", true, func() ui.Element {
					return ui.Text(c, "正在执行任务")
				})
			}
			tt := ui.NewTesterWith(view, 200, 100, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced}})
			tt.Frame()
			beforeBuilds := builds
			before := append([]byte(nil), tt.Image().Pix...)
			tt.Advance(300 * time.Millisecond)
			if builds != beforeBuilds {
				t.Fatalf("扫光重建了视图：%d -> %d", beforeBuilds, builds)
			}
			if tt.FramePending() {
				t.Fatal("扫光请求了整窗帧")
			}
			changed := !bytes.Equal(before, tt.Image().Pix)
			if changed == reduced {
				t.Fatalf("图像变化 = %v，减少动效 = %v", changed, reduced)
			}
		})
	}
}

func TestSecondTimerKeepsPaceWithRepaintAnimation(t *testing.T) {
	terminalFontFamily()
	var start time.Time
	view := func(c *ui.Context) {
		if start.IsZero() {
			start = c.Now().Truncate(time.Second)
		}
		ui.Column(c).Children(func() {
			StatusDot(c, nil, StatusRunning, "状态")
			ui.Text(c, fmt.Sprintf("%d", int(c.Now().Sub(start)/time.Second)))
			afterNextSecond(c)
		})
	}
	tt := ui.NewTesterWith(view, 200, 100, ui.TesterOptions{})
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	if _, ok := tt.Find("1"); !ok {
		t.Fatalf("中途重建后第 1 秒未刷新：%v", tt.Texts())
	}
	for second := 2; second <= 5; second++ {
		tt.Advance(time.Second)
		if _, ok := tt.Find(fmt.Sprint(second)); !ok {
			t.Fatalf("第 %d 秒未刷新：%v", second, tt.Texts())
		}
	}
}
