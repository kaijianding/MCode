package mcodeui

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

func newSearchListTester(items []searchListItem, propsMod func(*searchListProps)) (*ui.Tester, *searchListState) {
	st := &searchListState{}
	var ctx *ui.Context
	tt := ui.NewTester(func(c *ui.Context) {
		ctx = c
		ui.Column(c.Key("search-list-test-wrap")).
			Width(320).
			Padding(Space2).
			Gap(Space1).
			Children(func() {
				p := searchListProps{
					Key:         "test-search-list",
					Label:       "搜索条目",
					Placeholder: "输入关键词…",
					Empty:       "无匹配结果",
					Items:       items,
					State:       st,
					Invalidate: func() {
						if ctx != nil {
							ctx.Invalidate()
						}
					},
				}
				if propsMod != nil {
					propsMod(&p)
				}
				searchList(c, p)
			})
	}, 400, 500)
	tt.Frame()
	return tt, st
}

func TestSearchListFilter(t *testing.T) {
	tt, _ := newSearchListTester([]searchListItem{
		{ID: "p1", Title: "普通项目"},
		{ID: "p2", Title: "其他项目"},
	}, nil)
	if !tt.Focused("搜索条目") {
		t.Fatal("searchList 打开后应自动聚焦搜索框")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "其他项目"); !ok {
		t.Fatal("初始应显示「其他项目」")
	}
	tt.Type("普通")
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "其他项目"); ok {
		t.Fatal("输入「普通」后不应再显示「其他项目」")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "普通项目"); !ok {
		t.Fatal("输入「普通」后应保留「普通项目」")
	}
}

func TestSearchListKeyboardSelect(t *testing.T) {
	selectedID := ""
	tt, _ := newSearchListTester([]searchListItem{
		{ID: "a", Title: "第一项"},
		{ID: "b", Title: "第二项"},
		{ID: "c", Title: "第三项"},
	}, func(p *searchListProps) {
		p.OnSelect = func(id string) { selectedID = id }
	})
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if selectedID != "b" {
		t.Fatalf("按 ↓ 再 Enter 应选中 b，实际: %q", selectedID)
	}
}

func TestSearchListDismissOnEsc(t *testing.T) {
	dismissed := 0
	tt, _ := newSearchListTester([]searchListItem{
		{ID: "a", Title: "第一项"},
	}, func(p *searchListProps) {
		p.OnDismiss = func() { dismissed++ }
	})
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if dismissed != 1 {
		t.Fatalf("按 Esc 应触发一次 OnDismiss，实际: %d", dismissed)
	}
}

func countRightSlotInk(img *image.RGBA, bounds ui.Rect) int {
	b := img.Bounds()
	x0 := max(b.Min.X, int(bounds.X+bounds.W)-28)
	x1 := min(b.Max.X, int(bounds.X+bounds.W)-10)
	y0 := max(b.Min.Y, int(bounds.Y)+4)
	y1 := min(b.Max.Y, int(bounds.Y+bounds.H)-4)
	bgX := max(b.Min.X, min(b.Max.X-1, int(bounds.X)+4))
	bgY := max(b.Min.Y, min(b.Max.Y-1, int(bounds.Y)+4))
	bg := img.RGBAAt(bgX, bgY)
	ink := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if img.RGBAAt(x, y) != bg {
				ink++
			}
		}
	}
	return ink
}

func TestSearchListCheckedAndTrailing(t *testing.T) {
	tt, _ := newSearchListTester([]searchListItem{
		{ID: "i1", Title: "首项"},
		{ID: "i2", Title: "已勾选项", Trailing: "3分钟前", Checked: true},
		{ID: "i3", Title: "未勾选项", Checked: false},
	}, nil)
	if !tt.HasText("3分钟前") {
		t.Fatalf("Trailing 文字「3分钟前」应可见，实际Texts=%v", tt.Texts())
	}
	checkedRow, ok := tt.AccessByRole(ui.RoleButton, "已勾选项")
	if !ok {
		t.Fatal("未找到「已勾选项」按钮")
	}
	uncheckedRow, ok := tt.AccessByRole(ui.RoleButton, "未勾选项")
	if !ok {
		t.Fatal("未找到「未勾选项」按钮")
	}
	img := tt.Image()
	if got := countRightSlotInk(img, checkedRow.Bounds); got == 0 {
		t.Fatalf("Checked 项右侧应渲染勾选图标，实际非背景像素=%d", got)
	}
	if got := countRightSlotInk(img, uncheckedRow.Bounds); got != 0 {
		t.Fatalf("未勾选且无 Trailing 的行右侧不应有图标像素，实际=%d", got)
	}
}

func TestSearchListEmpty(t *testing.T) {
	tt, _ := newSearchListTester([]searchListItem{
		{ID: "a", Title: "Alpha"},
	}, nil)
	tt.Type("不存在")
	tt.Frame()
	if !tt.HasText("无匹配结果") {
		t.Fatalf("无匹配时应显示 Empty 文案「无匹配结果」，实际=%v", tt.Texts())
	}
}

func TestSearchListDisabled(t *testing.T) {
	selectedID := ""
	tt, _ := newSearchListTester([]searchListItem{
		{ID: "a", Title: "第一项"},
	}, func(p *searchListProps) {
		p.Disabled = true
		p.OnSelect = func(id string) { selectedID = id }
	})
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if selectedID != "" {
		t.Fatalf("Disabled 时 Enter 不应触发 OnSelect，实际=%q", selectedID)
	}
}
