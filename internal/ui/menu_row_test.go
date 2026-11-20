package mcodeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func newMenuRowTester(build func(c *ui.Context)) *ui.Tester {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c.Key("menu-row-test-wrap")).
			Width(280).
			Padding(Space2).
			Gap(Space1).
			Children(func() {
				build(c)
			})
	}, 360, 240)
	tt.Frame()
	return tt
}

func TestMenuRow(t *testing.T) {
	t.Run("ClickAndLabelFallback", func(t *testing.T) {
		clicked := 0
		tt := newMenuRowTester(func(c *ui.Context) {
			menuRow(c, menuRowProps{
				Key:   "row-open",
				Title: "打开文件",
				OnClick: func() {
					clicked++
				},
			})
		})
		info, ok := tt.AccessByRole(ui.RoleButton, "打开文件")
		if !ok {
			t.Fatal("Label 为空时应回落为 Title 作为无障碍名")
		}
		if info.Name != "打开文件" {
			t.Fatalf("无障碍名 = %q，期望 %q", info.Name, "打开文件")
		}
		if info.Disabled {
			t.Fatal("正常行无障碍节点不应为 Disabled")
		}
		if err := tt.Click("打开文件"); err != nil {
			t.Fatalf("点击菜单行失败: %v", err)
		}
		tt.Frame()
		if clicked != 1 {
			t.Fatalf("点击应触发一次 OnClick，实际=%d", clicked)
		}
	})

	t.Run("Disabled", func(t *testing.T) {
		clicked := 0
		tt := newMenuRowTester(func(c *ui.Context) {
			menuRow(c, menuRowProps{
				Key:      "row-disabled",
				Label:    "禁用操作",
				Title:    "删除",
				Disabled: true,
				OnClick: func() {
					clicked++
				},
			})
		})
		info, ok := tt.AccessByRole(ui.RoleButton, "禁用操作")
		if !ok {
			t.Fatal("未找到禁用菜单行的无障碍节点")
		}
		if !info.Disabled {
			t.Fatal("Disabled 行无障碍节点应标记为 Disabled")
		}
		tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
		tt.Frame()
		if clicked != 0 {
			t.Fatalf("Disabled 时点击不应触发 OnClick，实际=%d", clicked)
		}
	})

	t.Run("TrailingVisible", func(t *testing.T) {
		tt := newMenuRowTester(func(c *ui.Context) {
			menuRow(c, menuRowProps{
				Key:      "row-trailing",
				Title:    "源码编辑器",
				Trailing: "⇧⌘E",
				Icon:     IconSettings(),
				Checked:  true,
			})
		})
		if !tt.HasText("源码编辑器") {
			t.Fatalf("标题文字应可见，实际 Texts=%v", tt.Texts())
		}
		if !tt.HasText("⇧⌘E") {
			t.Fatalf("Trailing 文字「⇧⌘E」应可见，实际 Texts=%v", tt.Texts())
		}
	})
}
