package mcodeui

import (
	"bytes"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSkeletonRepaintsWithoutRebuild(t *testing.T) {
	for _, tc := range []struct {
		name string
		view func(*ui.Context)
	}{
		{"rows", func(c *ui.Context) { renderSkeletonRows(c, "repaint", 3, 14, 8) }},
		{"timeline", renderTimelineSkeleton},
	} {
		for _, reduced := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/animated", true: "/reduced"}[reduced], func(t *testing.T) {
				setAppReduceMotion(t, reduced)
				builds := 0
				tt := ui.NewTester(func(c *ui.Context) { builds++; tc.view(c) }, 300, 200)
				phase := time.Duration(tt.Now().UnixNano() % int64(2*motionSweep))
				tt.Advance(2*motionSweep - phase)
				tt.Frame()
				beforeBuilds := builds
				before := append([]byte(nil), tt.Image().Pix...)
				tt.Advance(300 * time.Millisecond)
				if builds != beforeBuilds {
					t.Errorf("组件重建: builds %d -> %d", beforeBuilds, builds)
				}
				if reduced && tt.FramePending() {
					t.Error("减少动效时请求了下一帧")
				}
				if same := bytes.Equal(before, tt.Image().Pix); same != reduced {
					t.Errorf("图像变化不符合减少动效设置: same=%v", same)
				}
			})
		}
	}
}

func TestGitSyncSpinRepaintsWithoutRebuild(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(map[bool]string{false: "animated", true: "reduced"}[reduced], func(t *testing.T) {
			setAppReduceMotion(t, reduced)
			builds := 0
			tt := ui.NewTester(func(c *ui.Context) {
				builds++
				renderGitSyncSpin(c)
			}, 100, 100)
			tt.Frame()
			beforeBuilds := builds
			before := append([]byte(nil), tt.Image().Pix...)
			tt.Advance(300 * time.Millisecond)
			if builds != beforeBuilds {
				t.Errorf("组件重建: builds %d -> %d", beforeBuilds, builds)
			}
			if reduced && tt.FramePending() {
				t.Error("减少动效时请求了下一帧")
			}
			if same := bytes.Equal(before, tt.Image().Pix); same != reduced {
				t.Errorf("图像变化不符合减少动效设置: same=%v", same)
			}
		})
	}
}
