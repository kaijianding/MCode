package mcodeui

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

// searchListItem 是 searchList 中的单条可选项。
type searchListItem struct {
	ID       string
	Title    string
	Trailing string // Trailing 是行右侧的次要文字（如相对时间），为空则不画
	Checked  bool
}

// searchListState 保存搜索列表的查询词、选中行与悬停指针状态。
type searchListState struct {
	mu        sync.Mutex
	query     string
	selected  int
	scroll    bool
	pointerOn bool
	pointerX  float32
	pointerY  float32
}

// reset 清空搜索列表除锁以外的所有交互状态。
func (st *searchListState) reset() {
	st.mu.Lock()
	st.query = ""
	st.selected = 0
	st.scroll = false
	st.pointerOn = false
	st.pointerX = 0
	st.pointerY = 0
	st.mu.Unlock()
}

// searchListProps 配置 searchList 的文案、数据源、状态与回调。
type searchListProps struct {
	Key         string
	Label       string
	Placeholder string
	Empty       string
	Items       []searchListItem
	State       *searchListState
	Disabled    bool
	MaxHeight   float32
	OnSelect    func(id string)
	OnDismiss   func()
	Invalidate  func()
}

// searchList 渲染浮层卡片内部的搜索框与可键盘/指针导航的滚动列表。
func searchList(c *ui.Context, p searchListProps) {
	st := p.State
	if st == nil {
		return
	}
	st.mu.Lock()
	query := st.query
	selected := st.selected
	scrollSelected := st.scroll
	prevPointerOn := st.pointerOn
	prevPointerX := st.pointerX
	prevPointerY := st.pointerY
	st.mu.Unlock()

	filtered := make([]searchListItem, 0, len(p.Items))
	for _, item := range p.Items {
		if branchMatches(item.Title, query) {
			filtered = append(filtered, item)
		}
	}
	if selected < 0 || selected >= len(filtered) {
		selected = 0
	}

	invalidate := func() {
		if p.Invalidate != nil {
			p.Invalidate()
		}
	}

	SearchField(c, TextFieldProps{
		Key:         p.Key + "-search",
		Value:       &query,
		Label:       p.Label,
		Placeholder: p.Placeholder,
		FillWidth:   true,
		AutoFocus:   true,
		OnChange: func(v string) {
			st.mu.Lock()
			st.query = v
			st.selected = 0
			st.scroll = false
			st.mu.Unlock()
			invalidate()
		},
		OnKeyDown: func(k ui.TextKey) bool {
			switch k.Key {
			case ui.KeyEscape:
				if p.OnDismiss != nil {
					p.OnDismiss()
				}
				return true
			case ui.KeyUp:
				st.mu.Lock()
				if selected > 0 {
					st.selected = selected - 1
				} else {
					st.selected = 0
				}
				st.scroll = true
				st.mu.Unlock()
				invalidate()
				return true
			case ui.KeyDown:
				st.mu.Lock()
				if selected+1 < len(filtered) {
					st.selected = selected + 1
				} else if len(filtered) > 0 {
					st.selected = selected
				}
				st.scroll = true
				st.mu.Unlock()
				invalidate()
				return true
			case ui.KeyEnter:
				if len(filtered) > 0 && !p.Disabled {
					st.mu.Lock()
					idx := st.selected
					st.mu.Unlock()
					if idx < 0 || idx >= len(filtered) {
						idx = selected
					}
					if p.OnSelect != nil {
						p.OnSelect(filtered[idx].ID)
					}
				}
				return true
			}
			return false
		},
	})

	maxHeight := p.MaxHeight
	if maxHeight <= 0 {
		maxHeight = 360
	}
	scrollEl := ui.Scroll(c.Key(p.Key + "-scroll")).FillWidth().MaxHeight(maxHeight)
	px, py, pointerOver := scrollEl.PointerPosition()
	pointerMoved := pointerOver && (!prevPointerOn || px != prevPointerX || py != prevPointerY)
	scrollEl.Children(func() {
		ui.Column(c.Key(p.Key + "-list")).FillWidth().Children(func() {
			hoveredIdx := -1
			if len(filtered) == 0 {
				ui.Row(c.Key(p.Key+"-empty")).FillWidth().Padding(Space2, Space3).Children(func() {
					ui.Text(c, p.Empty).
						FontSize(uiFont(TypeBody)).
						TextColor(ThemeSecondaryText(c)).
						SingleLine()
				})
			} else {
				for i, entry := range filtered {
					item := ui.ButtonBase(c.Key(p.Key+"-item-"+entry.ID)).
						Label(entry.Title).
						Padding(Space2, Space3).
						Radius(RadiusSM).
						FillWidth().
						Justify(ui.Start).
						Disabled(p.Disabled)
					if item.Hovered() {
						hoveredIdx = i
					}
					if i == selected {
						item.Background(ThemeHover(c))
						if scrollSelected {
							item.ScrollIntoView()
						}
					}
					if !p.Disabled && item.Clicked() && p.OnSelect != nil {
						p.OnSelect(entry.ID)
					}
					item.Children(func() {
						ui.Row(c.Key(p.Key+"-item-row-"+entry.ID)).
							Gap(Space2).
							AlignItems(ui.Center).
							Grow(1).
							Children(func() {
								ui.Text(c, entry.Title).
									FontSize(uiFont(TypeBody)).
									TextColor(ThemePrimaryText(c)).
									Grow(1).
									SingleLine()
								if entry.Trailing != "" {
									ui.Text(c, entry.Trailing).
										FontSize(uiFont(TypeCaption)).
										TextColor(ThemeSecondaryText(c)).
										Shrink(0).
										SingleLine()
								}
								if entry.Checked {
									ui.Icon(c, IconCheck()).Size(14, 14).TextColor(ThemeAccent(c))
								}
							})
					})
				}
			}
			if pointerOver != prevPointerOn || pointerMoved || scrollSelected {
				needInvalidate := false
				st.mu.Lock()
				st.pointerOn = pointerOver
				st.pointerX = px
				st.pointerY = py
				// 仅在指针实际进入或移动时按悬停切换选中，防止静止悬停在滚动或键盘导航时抢回选中。
				if pointerMoved && hoveredIdx >= 0 && hoveredIdx != st.selected {
					st.selected = hoveredIdx
					st.scroll = false
					needInvalidate = true
				} else if scrollSelected && st.selected == selected {
					st.scroll = false
				}
				st.mu.Unlock()
				if needInvalidate {
					invalidate()
				}
			}
		})
	})
}
