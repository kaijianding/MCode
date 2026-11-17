package mcodeui

import (
	"bytes"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestLiveDotsRepaintWithoutRebuild(t *testing.T) {
	cases := []struct {
		name   string
		period time.Duration
		view   func(*ui.Context)
	}{
		{"status", statusDotPulsePeriod, func(c *ui.Context) { StatusDot(c, nil, StatusRunning, "运行中") }},
		{"capsule", swarmDispatchPulsePeriod, func(c *ui.Context) {
			renderComposerCapsule(c, nil, composerCapsule{Key: "live", Label: "实时胶囊", Text: "运行中", Live: true})
		}},
	}
	for _, tc := range cases {
		for _, reduced := range []bool{false, true} {
			name := "animated"
			if reduced {
				name = "reduced"
			}
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				setAppReduceMotion(t, reduced)
				builds := 0
				tt := ui.NewTesterWith(func(c *ui.Context) {
					builds++
					tc.view(c)
				}, 400, 100, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced}})
				tt.Frame()
				beforeBuilds := builds
				before := append([]byte(nil), tt.Image().Pix...)
				tt.Advance(tc.period / 2)
				// 三角波在相对相位上可能恰好相等，再推进一步排除这种情况。
				same := bytes.Equal(before, tt.Image().Pix)
				if !reduced && same {
					tt.Advance(tc.period / 8)
					same = bytes.Equal(before, tt.Image().Pix)
				}
				if builds != beforeBuilds {
					t.Errorf("视图重建：%d → %d", beforeBuilds, builds)
				}
				if tt.FramePending() {
					t.Error("不应请求组件树的下一帧")
				}
				if same != reduced {
					t.Errorf("图像相同=%v，减少动效=%v", same, reduced)
				}
			})
		}
	}
}
