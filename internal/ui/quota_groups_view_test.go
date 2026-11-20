package mcodeui

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestQuotaWarningThreshold(t *testing.T) {
	for _, tc := range []struct {
		used  float64
		reset time.Time
		pct   string
	}{
		{94.9, time.Time{}, "95%"},
		{95, time.Time{}, "95%"},
		{100, time.Date(2026, 1, 1, 13, 45, 0, 0, time.Local), "100%"},
	} {
		groups := []trayQuotaGroupModel{{Title: "测试", Accounts: []trayQuotaAccountModel{{Windows: []trayQuotaWindowModel{{ShortLabel: "5h", UsedPercent: tc.used, resetsAt: tc.reset}}}}}}
		tt := ui.NewTester(func(c *ui.Context) { renderQuotaGroupList(c, "test-", groups, quotaGroupListOptions{}) }, 500, 300)
		tt.Frame()
		if !tt.HasText("5h") || !tt.HasText(tc.pct) {
			t.Fatalf("%.1f 缺少窗口或百分比 %q: %v", tc.used, tc.pct, tt.Texts())
		}
		if tt.HasText("即将用尽") || tt.HasText("已用尽") {
			t.Fatalf("%.1f 不应额外显示重复用尽提示: %v", tc.used, tt.Texts())
		}
		if tt.HasText("重置于") {
			t.Fatalf("%.1f 不应重复拼接重置时刻: %v", tc.used, tt.Texts())
		}
	}
}

func TestQuotaBarMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprintf("reduced=%v", reduced), func(t *testing.T) {
			setAppReduceMotion(t, reduced)
			used := 0.2
			tt := ui.NewTesterWith(func(c *ui.Context) {
				ui.Row(c.Key("bar")).Width(200).Height(4).Children(func() {
					renderQuotaBarSegments(c, "motion-quota", quotaBarSegments(used, 0.5, false, false), ThemeAccent(c), "marker")
				})
			}, 220, 50, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced}})
			tt.Frame()
			used = 0.4
			tt.Frame()
			if tt.FramePending() == reduced {
				t.Fatalf("减少动效=%v 时帧排队状态不正确", reduced)
			}
			tt.Advance(DurBase + time.Second)
			tt.Frame()
			if tt.FramePending() {
				t.Fatal("动效应结束")
			}
		})
	}
}

func TestQuotaBarSettlesWhileClockAdvances(t *testing.T) {
	for _, wide := range []bool{false, true} {
		t.Run(fmt.Sprintf("wide=%v", wide), func(t *testing.T) {
			elapsed := 0.4
			used := 20.0
			tt := ui.NewTester(func(c *ui.Context) {
				win := trayQuotaWindowModel{ShortLabel: "5h", UsedPercent: used, Wide: wide, elapsedRatio: elapsed, hasElapsed: true, paceKnown: true}
				groups := []trayQuotaGroupModel{{Accounts: []trayQuotaAccountModel{{Windows: []trayQuotaWindowModel{win}}}}}
				renderQuotaGroupList(c, "test-", groups, quotaGroupListOptions{})
			}, 500, 300)
			tt.Frame()
			used = 30
			tt.Frame()
			if !tt.FramePending() {
				t.Fatal("用量变化后应启动动画")
			}
			for i := 0; i < 100; i++ {
				elapsed += 16.0 / (300 * 60 * 1000)
				tt.Advance(16 * time.Millisecond)
				tt.Frame()
			}
			if tt.FramePending() {
				t.Fatal("时间在同一像素内推进时动画应结束")
			}
		})
	}
}
