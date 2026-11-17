package mcodeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTabularDigits(t *testing.T) {
	for _, overlay := range []bool{false, true} {
		name := "window"
		if overlay {
			name = "popover"
		}
		t.Run(name, func(t *testing.T) {
			open := true
			tt := ui.NewTester(func(c *ui.Context) {
				applyAppTheme(c)
				ui.Column(c).AlignItems(ui.Start).Children(func() {
					if overlay {
						anchor := ui.Box(c).Width(20).Height(20)
						appPopover(c, anchor, &open, nil, func() {
							ui.Column(c).AlignItems(ui.Start).Children(func() {
								ui.Text(c, "111").FontSize(32)
								ui.Text(c, "888").FontSize(32)
							})
						})
					} else {
						ui.Text(c, "111").FontSize(32)
						ui.Text(c, "888").FontSize(32)
					}
				})
			}, 400, 300)
			tt.Frame()
			one, ok := tt.Find("111")
			if !ok {
				t.Fatal("111 not found")
			}
			eight, ok := tt.Find("888")
			if !ok {
				t.Fatal("888 not found")
			}
			if one.W != eight.W {
				t.Errorf("digit widths differ: 111=%v, 888=%v", one.W, eight.W)
			}
		})
	}
}
