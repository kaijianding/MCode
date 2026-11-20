package mcodeui

import "github.com/egoist/mygo/ui"

// menuRowProps 配置共用菜单行 menuRow 的键、文案、图标、状态与点击回调。
type menuRowProps struct {
	Key         string  // 元素 key，内部 Row 用 Key+"-row"
	Label       string  // 无障碍名，空则取 Title
	Icon        *ui.SVG // 可选左侧图标，14×14，ThemeSecondaryText
	Title       string
	Trailing    string // 可选右侧说明（快捷键、数量、时间）
	Checked     bool   // 右侧勾选
	Danger      bool   // 标题与图标用 ThemeDanger
	Disabled    bool
	Highlighted bool // 键盘选中或当前项：常亮 ThemeHover 背景
	OnClick     func()
}

// menuRow 渲染浮层菜单中的单行选项，返回 ButtonBase 元素供调用方继续链式配置或读取交互状态。
func menuRow(c *ui.Context, p menuRowProps) ui.Element {
	label := p.Label
	if label == "" {
		label = p.Title
	}
	btn := ui.ButtonBase(c.Key(p.Key)).
		Label(label).
		Padding(Space2, Space3).
		Radius(RadiusSM).
		FillWidth().
		Justify(ui.Start).
		Disabled(p.Disabled)

	if p.Highlighted || (!p.Disabled && btn.Hovered()) {
		btn.Background(ThemeHover(c))
	}
	if !p.Disabled && btn.FocusVisible() {
		btn.Shadow(0, 0, 0, 2, ThemeAccent(c)).Shadow(0, 0, 0, 1, ThemeSurfaceCard(c))
	}
	if !p.Disabled && p.OnClick != nil {
		btn.OnClick(p.OnClick)
	}

	titleColor := ThemePrimaryText(c)
	iconColor := ThemeSecondaryText(c)
	if p.Danger {
		titleColor = ThemeDanger(c)
		iconColor = ThemeDanger(c)
	}

	btn.Children(func() {
		ui.Row(c.Key(p.Key + "-row")).
			Gap(Space2).
			AlignItems(ui.Center).
			Grow(1).
			Children(func() {
				if p.Icon != nil {
					ui.Icon(c, p.Icon).Size(14, 14).TextColor(iconColor)
				}
				ui.Text(c, p.Title).
					FontSize(uiFont(TypeBody)).
					TextColor(titleColor).
					Grow(1).
					SingleLine()
				if p.Trailing != "" {
					ui.Text(c, p.Trailing).
						FontSize(uiFont(TypeCaption)).
						TextColor(ThemeSecondaryText(c)).
						Shrink(0).
						SingleLine()
				}
				if p.Checked {
					ui.Icon(c, IconCheck()).Size(14, 14).TextColor(ThemeAccent(c))
				}
			})
	})
	return btn
}
