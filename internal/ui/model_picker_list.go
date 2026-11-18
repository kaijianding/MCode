package mcodeui

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"mcode/internal/config"
)

// modelPickerSideFromTab 把分段 ID 换算成侧别；空串按主导侧处理（与默认分段一致）。
func modelPickerSideFromTab(tab string) ModelPickerSide {
	if tab == "bees" {
		return ModelPickerSideBees
	}
	return ModelPickerSideLead
}

// modelIndexByID 返回指定模型 ID 在目录中的下标；未找到返回 -1。
func modelIndexByID(models []ModelInfo, id string) int {
	for idx, m := range models {
		if m.ID == id {
			return idx
		}
	}
	return -1
}

// renderModelPickerListPage 渲染模型列表选择页：顶部返回按钮与搜索框，其下为单列分组可折叠模型列表。
func renderModelPickerListPage(c *ui.Context, p ModelPickerProps, state *ModelPickerState, side ModelPickerSide) {
	scopeKey := p.ScopeKey
	// 分组保留当前会话正在使用的供应商，精选仅显示已启用供应商的模型。
	// 只影响展示，配置里的 ModelList 不做任何改动。
	disabledProviders := modelPickerDisabledProviders(p, p.Selection.Provider, p.Bees.Provider)
	providers := p.Providers
	if len(disabledProviders) > 0 {
		providers = filterProvidersByEnabled(providers, disabledProviders)
	}
	providers = filterModelPickerMagpie(providers, p.MagpieFilter)
	pinned := modelPickerPinnedEntries(p.Pins, filterProvidersByEnabled(providers, p.ProviderEnabled))
	// state 为 nil 时沿用包内按 key 复用的状态，避免搜索框绑定空指针。
	if state == nil {
		state = modelPickerStateFor(p.KeyPrefix + p.ScopeKey)
	}
	query := state.searchQuery
	searching := strings.TrimSpace(query) != ""
	// 正在为某个档位选模型：编辑状态记在面板状态里，选中模型后落到该档位而不是会话子代理模型。
	editingTier := ""
	if side == ModelPickerSideBees {
		editingTier = state.TierEditing
	}

	onBack := func() {
		state.setListOpen(scopeKey, side, false)
		// 返回后下次打开不残留旧查询与旧展开状态。
		state.searchQuery = ""
		state.agentOpen = nil
		modelPickerInvalidate(p)
	}

	onPick := func(prov ProviderInfo, m ModelInfo) {
		// 偏好保存期间模型列表整体不可选，给出原因而不是静默忽略点击。
		if effortCardLockedReason(p, side) != "" {
			return
		}
		if side == ModelPickerSideBees && editingTier != "" {
			tierID := editingTier
			state.searchQuery = ""
			state.agentOpen = nil
			if p.OnSetTierOverride != nil {
				p.OnSetTierOverride(tierID, &config.SwarmTierOverride{
					Provider: prov.ID,
					Model:    m.ID,
				})
			}
			state.TierEditing = ""
			modelPickerInvalidate(p)
			return
		}
		state.setListOpen(scopeKey, side, false)
		// 选中后清空查询与展开状态，下次打开列表不残留旧状态。
		state.searchQuery = ""
		state.agentOpen = nil
		if p.OnPickModel != nil {
			p.OnPickModel(side, prov, m)
		}
	}

	selection := p.Selection
	if side == ModelPickerSideBees {
		selection = p.Bees
	}
	// 正在为档位选模型：默认展开该档位当前模型所属 Agent，而不是会话正在用的 Agent。
	if side == ModelPickerSideBees && editingTier != "" {
		for _, tier := range p.Tiers {
			if tier.ID != editingTier {
				continue
			}
			if prov, _, _, _ := effectiveTierModel(tier, p.PairTierOverrides, p.TierOverrides); prov != "" {
				selection.Provider = prov
			}
			break
		}
	}

	body := ui.Column(c.Key(p.KeyPrefix + "model-list-column")).
		MinWidth(0).
		FillWidth()
	body.Children(func() {
		// 搜索框固定在正文顶部：列表滚动时返回按钮与搜索框都不动。
		ui.Row(c.Key(p.KeyPrefix+"model-list-search-row")).
			AlignItems(ui.Center).
			Gap(Space2).
			Padding(Space2, Space3, Space1, Space1).
			FillWidth().
			Children(func() {
				renderModelPickerBackButton(c, p.KeyPrefix, onBack)
				SearchField(c, TextFieldProps{Key: p.KeyPrefix + "model-list-search", Value: &state.searchQuery, Label: "model-list-search-input", Placeholder: "搜索模型", FillWidth: true, AutoFocus: true, OnChange: func(string) { modelPickerInvalidate(p) }})
			})
		renderModelPickerListRegion(c, p, state, side, selection, searching, query, providers, pinned, onPick)
	})
	modelPickerPageTransition(c, body, state, modelPickerPageDepth(p, state, side))
}

// renderModelPickerListRegion 渲染搜索框下方的列表：搜索态跨 Agent 列出命中项，
// 非搜索态渲染配对/跟随/档位/收藏与按 Agent 折叠分组的模型列表。
func renderModelPickerListRegion(c *ui.Context, p ModelPickerProps, state *ModelPickerState, side ModelPickerSide,
	selection ModelSelection, searching bool, query string,
	providers []ProviderInfo, pinned []modelPickerPinnedEntry, onPick func(ProviderInfo, ModelInfo)) {
	var list ui.Element
	if isAppFlyoutServices(c.Services()) && p.NativeMenu == nil {
		list = ui.Column(c.Key(p.KeyPrefix + "model-list-rows"))
	} else {
		list = ui.Scroll(c.Key(p.KeyPrefix+"model-list-rows")).MaxHeight(400).ScrollbarInsets(4, 0, 4, 0)
	}
	list.MinWidth(0).FillWidth().Children(func() {
		if searching {
			if side == ModelPickerSideBees && state != nil && state.TierEditing != "" {
				renderTierEditingBanner(c, p, state)
			}
			renderModelPickerSearchResults(c, p, side, providers, query, onPick)
			return
		}
		renderModelPickerGroupedList(c, p, state, side, selection, providers, pinned, onPick)
	})
}

// modelPickerDefaultOpenAgent 返回列表页默认展开的 Agent ID：
// 当前选中的供应商在本次可渲染列表里且有模型时优先展开它，否则回退到第一个有模型的供应商。
func modelPickerDefaultOpenAgent(providers []ProviderInfo, current string) string {
	firstWithModels := ""
	for _, prov := range providers {
		if len(prov.Models) == 0 {
			continue
		}
		if firstWithModels == "" {
			firstWithModels = prov.ID
		}
		if current != "" && prov.ID == current {
			return prov.ID
		}
	}
	return firstWithModels
}

// renderModelPickerListCard 渲染列表页的分组卡片容器，用于分隔收藏与各 Agent 分组。
func renderModelPickerListCard(c *ui.Context, key string, body func()) {
	ui.Column(c.Key(key)).
		Gap(Space0).
		Padding(Space1).
		Radius(RadiusLG).
		Background(modelPickerCardBg).
		FillWidth().
		Children(body)
}

// renderModelPickerGroupedList 渲染非搜索态纵向列表：依次包含档位编辑提示、主导侧配对、
// 执行侧跟随主导、执行侧档位、收藏以及按 Agent 折叠的模型分组。
func renderModelPickerGroupedList(c *ui.Context, p ModelPickerProps, state *ModelPickerState, side ModelPickerSide,
	selection ModelSelection, providers []ProviderInfo, pinned []modelPickerPinnedEntry,
	onPick func(ProviderInfo, ModelInfo)) {
	ui.Column(c.Key(p.KeyPrefix+"model-list-items")).
		Gap(Space3).
		MinWidth(0).
		Padding(0, 0, Space2, 0).
		FillWidth().
		Children(func() {
			if side == ModelPickerSideBees && state != nil && state.TierEditing != "" {
				renderTierEditingBanner(c, p, state)
			}
			if side == ModelPickerSideLead {
				renderModelPickerPairsSection(c, p, state)
			}
			if p.Mode == ModelPickerSwarm && side == ModelPickerSideBees {
				renderModelPickerFollowLeadRow(c, p, state)
			}
			if side == ModelPickerSideBees && len(p.Tiers) > 0 {
				renderModelPickerTiersSection(c, p, state)
			}
			if len(pinned) > 0 {
				ui.Column(c.Key(p.KeyPrefix + "pinned-section")).
					Gap(Space0).
					MinWidth(0).
					FillWidth().
					Children(func() {
						renderProviderGroupHeader(c, p.KeyPrefix+"pinned-group-header", "收藏")
						renderModelPickerListCard(c, p.KeyPrefix+"pinned-card", func() {
							for _, entry := range pinned {
								mIdx := modelIndexByID(entry.Provider.Models, entry.Model.ID)
								renderModelPickerListRow(c, p, modelRowOptions{
									KeyPrefix: "pinned-",
									Provider:  entry.Provider,
									Model:     entry.Model,
									IsChecked: selection.Provider == entry.Provider.ID && modelMatches(entry.Provider.Models, selection.Model, mIdx, entry.Model),
									IsLocked:  side == ModelPickerSideLead && p.HasMessages && entry.Provider.ID != selection.Provider,
									ShowGroup: true,
									OnPick:    onPick,
								})
							}
						})
					})
			}

			currentAgent := selection.Provider
			defaultOpenAgent := modelPickerDefaultOpenAgent(providers, currentAgent)
			lockAgents := side == ModelPickerSideLead && p.HasMessages
			renderedAny := false
			for _, pv := range providers {
				prov := pv
				if lockAgents && prov.ID != currentAgent {
					continue
				}
				if len(prov.Models) == 0 {
					if prov.Available && prov.UnavailableReason != "" {
						renderModelPickerListCard(c, p.KeyPrefix+"agent-notice-card-"+prov.ID, func() {
							renderProviderCatalogNotice(c, p, "", prov)
						})
						renderedAny = true
					}
					continue
				}
				renderedAny = true
				open, hasOpen := state.agentOpen[prov.ID]
				if !hasOpen {
					open = defaultOpenAgent != "" && prov.ID == defaultOpenAgent
				}
				renderModelPickerListCard(c, p.KeyPrefix+"agent-group-wrap-"+prov.ID, func() {
					renderModelPickerAgentHeader(c, p.KeyPrefix+"agent-group-"+prov.ID, prov, modelPickerAgentName(prov), !open, func() {
						if state.agentOpen == nil {
							state.agentOpen = make(map[string]bool)
						}
						state.agentOpen[prov.ID] = !open
						modelPickerInvalidate(p)
					})
					if !open {
						return
					}
					ui.Box(c.Key(p.KeyPrefix+"agent-group-divider-"+prov.ID)).
						Height(Hairline).
						Margin(0, Space3).
						Background(modelPickerDivider).
						FillWidth()
					ui.Column(c.Key(p.KeyPrefix + "agent-group-body-" + prov.ID)).
						Gap(Space0).
						FillWidth().
						Transition(motionCollapse(c)).
						Children(func() {
							for mIdx, m := range prov.Models {
								renderModelPickerListRow(c, p, modelRowOptions{
									KeyPrefix: "model-",
									Provider:  prov,
									Model:     m,
									IsChecked: selection.Provider == prov.ID && modelMatches(prov.Models, selection.Model, mIdx, m),
									ShowQuota: mIdx == 0,
									OnPick:    onPick,
								})
							}
						})
				})
			}
			if !renderedAny {
				ui.Text(c, "没有可用的模型").
					FontSize(uiFont(TypeFootnote)).
					TextColor(modelPickerTextSecondary).
					Padding(Space2, Space3)
			}
		})
}

// renderModelPickerAgentHeader 渲染列表页可点击折叠的 Agent 分组标题：带供应商图标、加粗正文与右侧折叠箭头。
func renderModelPickerAgentHeader(c *ui.Context, key string, prov ProviderInfo, title string, collapsed bool, onToggle func()) {
	label := "折叠 " + title
	if collapsed {
		label = "展开 " + title
	}
	btn := ui.ButtonBase(c.Key(key)).
		Label(label).
		Tooltip(label).
		Height(ControlMD).
		Padding(Space0, Space3).
		Radius(RadiusMD).
		FillWidth()
	if btn.Hovered() {
		btn.Background(ColorHoverDark)
	} else {
		btn.Background(ui.Transparent)
	}
	btn.OnClick(func() {
		if onToggle != nil {
			onToggle()
		}
	})
	btn.Children(func() {
		ui.Row(c.Key(key + "-content")).
			AlignItems(ui.Center).
			Gap(Space2).
			FillWidth().
			Children(func() {
				ui.Box(c.Key(key+"-icon-slot")).
					Size(IconSM, IconSM).
					Center().
					Shrink(0).
					Children(func() {
						renderProviderIcon(c, prov.ID, IconSM)
					})
				ui.Text(c, title).
					FontSize(uiFont(TypeBody)).
					FontWeight(600).
					TextColor(modelPickerTextPrimary).
					SingleLine().
					Grow(1)
				arrow := IconChevronDown()
				if collapsed {
					arrow = IconChevronRight()
				}
				ui.Icon(c, arrow).
					Size(10, 10).
					TextColor(modelPickerTextSecondary)
			})
	})
}

// renderModelPickerFollowLeadRow 渲染执行侧列表顶部的「跟随主导模型」行。
func renderModelPickerFollowLeadRow(c *ui.Context, p ModelPickerProps, state *ModelPickerState) {
	selected := modelPickerSideInfo(p, ModelPickerSideBees).FollowsLead
	btn := ui.ButtonBase(c.Key(p.KeyPrefix+"follow-lead-row")).
		Label("跟随主导模型").
		Tooltip("跟随主导模型").
		Checked(selected).
		Height(modelPickerRowHeight).
		Padding(Space2, Space3).
		Radius(RadiusMD).
		FillWidth()
	if p.Disabled || p.BeesDisabled {
		btn.Disabled(true).
			Opacity(0.45).
			Background(ui.Transparent).
			Tooltip(modelSideDisabledReason(p.Disabled, p.BeesDisabled))
	} else {
		switch {
		case selected:
			btn.Background(ColorSelectedDark)
		case btn.Hovered():
			btn.Background(ColorHoverDark)
		default:
			btn.Background(ui.Transparent)
		}
		btn.OnClick(func() {
			state.setListOpen(p.ScopeKey, ModelPickerSideBees, false)
			state.searchQuery = ""
			state.agentOpen = nil
			if p.OnFollowLead != nil {
				p.OnFollowLead()
			}
			modelPickerInvalidate(p)
		})
	}
	btn.Children(func() {
		ui.Row(c.Key(p.KeyPrefix + "follow-lead-row-content")).
			AlignItems(ui.Center).
			Gap(Space2).
			FillWidth().
			Children(func() {
				ui.Box(c.Key(p.KeyPrefix+"follow-lead-row-icon-slot")).
					Size(IconSM, IconSM).
					Center().
					Shrink(0).
					Children(func() {
						ui.Icon(c, IconByName("merge")).
							Size(IconSM, IconSM).
							TextColor(modelPickerTextSecondary)
					})
				ui.Text(c, "跟随主导模型").
					FontSize(uiFont(TypeBody)).
					TextColor(modelPickerTextPrimary).
					SingleLine().
					Shrink(0)
				ui.Row(c.Key(p.KeyPrefix + "follow-lead-row-gap")).Grow(1)
				if selected {
					ui.Icon(c, IconCheck()).
						Size(IconSM, IconSM).
						TextColor(modelPickerTextPrimary)
				}
			})
	})
}

// renderModelPickerPairsSection 渲染主导侧列表顶部的 Swarm 配对分组：
// 只有存在就绪配对模板时出现，选中项按当前两侧选择解析。
func renderModelPickerPairsSection(c *ui.Context, p ModelPickerProps, state *ModelPickerState) {
	if (p.Mode != ModelPickerSwarm && p.OnApplyPair == nil) || len(p.Pairs) == 0 {
		return
	}
	curPair, hasCurPair := SwarmPair{}, false
	if p.Mode != ModelPickerSwarm {
		curPair, hasCurPair = SwarmPair{ID: p.PairID}, p.PairID != ""
	} else if p.SwarmOn {
		curPair, hasCurPair = currentSwarmPairFor(p.Pairs, p.PairID, p.Selection.Provider, p.Selection.Model, p.Bees.Provider, p.Bees.Model)
	}
	renderProviderGroupHeader(c, p.KeyPrefix+"pair-group-header", "配对")
	for idx, pair := range p.Pairs {
		pair := pair
		selected := hasCurPair && curPair.ID == pair.ID
		onEnter := func() {
			// 应用配对后关闭整个弹层。
			state.Reset()
			clearModelCapsuleSnapshot(p.ScopeKey)
			if p.OnApplyPair != nil {
				p.OnApplyPair(pair)
			}
		}
		renderModelListPairRow(c, p, fmt.Sprintf("%spair-row-%d", p.KeyPrefix, idx), pair, selected, onEnter)
	}
	renderProviderGroupHeader(c, p.KeyPrefix+"pair-models-header", "模型")
}

// renderModelPickerBackButton 渲染搜索框左侧的返回按钮：回到该侧的推理强度卡片。
func renderModelPickerBackButton(c *ui.Context, keyPrefix string, onBack func()) {
	btn := ui.ButtonBase(c.Key(keyPrefix+"model-list-back")).
		Label("返回").
		Tooltip("返回推理强度").
		Size(ControlMD, ControlMD).
		Radius(RadiusSM).
		Shrink(0).
		Center().
		Background(ui.Transparent)
	if btn.Hovered() {
		btn.Background(ColorHoverDark)
	}
	btn.OnClick(onBack)
	btn.Children(func() {
		ui.Icon(c, iconChevronLeft).Size(IconSM, IconSM).TextColor(modelPickerTextSecondary)
	})
}

// renderModelPickerSearchResults 渲染搜索态：跨 Agent 列出命中模型，每个 Agent 一组。
func renderModelPickerSearchResults(c *ui.Context, p ModelPickerProps, side ModelPickerSide,
	providers []ProviderInfo, query string, onPick func(ProviderInfo, ModelInfo)) {
	isChecked := func(prov ProviderInfo, mIdx int, m ModelInfo) bool {
		return prov.ID == p.Selection.Provider && modelMatches(prov.Models, p.Selection.Model, mIdx, m)
	}
	isDisabled := func(prov ProviderInfo) bool { return p.HasMessages && prov.ID != p.Selection.Provider }
	if side == ModelPickerSideBees {
		isChecked = func(prov ProviderInfo, mIdx int, m ModelInfo) bool {
			return prov.ID == p.Bees.Provider && modelMatches(prov.Models, p.Bees.Model, mIdx, m)
		}
		isDisabled = func(ProviderInfo) bool { return false }
	}
	rendered := renderModelList(c, p, "search-", modelPickerSideKey(side), providers, query, isChecked, isDisabled, onPick)
	if !rendered {
		ui.Text(c, "没有匹配的模型").
			FontSize(uiFont(TypeFootnote)).
			TextColor(modelPickerTextSecondary).
			Padding(Space2, Space3, Space1, Space3)
	}
}

// modelPickerAgentName 返回 Agent 的显示名，供分组标题与 Tooltip 使用。
func modelPickerAgentName(prov ProviderInfo) string {
	if name := strings.TrimSpace(prov.Name); name != "" {
		return name
	}
	if name := providerDisplayName(prov.ID); name != "" {
		return name
	}
	return prov.ID
}

// modelPickerRowName 去掉列表行展示名末尾的「 (auto)」后缀；
// 若同供应商下已有其他模型的 Name 或 ID 等于去掉后的名字，则保留原名以免两行同名。
func modelPickerRowName(prov ProviderInfo, name string) string {
	trimmed := strings.TrimSpace(name)
	const suffix = " (auto)"
	if len(trimmed) <= len(suffix) || !strings.EqualFold(trimmed[len(trimmed)-len(suffix):], suffix) {
		return name
	}
	base := strings.TrimSpace(trimmed[:len(trimmed)-len(suffix)])
	if base == "" {
		return name
	}
	for _, m := range prov.Models {
		if m.Name == name || (m.Name == "" && m.ID == name) {
			continue
		}
		if m.Name == base || m.ID == base {
			return name
		}
	}
	return base
}

// renderModelPickerListRow 渲染模型列表页的一行：左侧图标槽与模型名，右侧按需展示配额、收藏星标与当前选中标记。
// 星标只反映模型是否在配置的精选列表里（见 ModelPickerProps.Pins），不可点击。
func renderModelPickerListRow(c *ui.Context, p ModelPickerProps, opt modelRowOptions) {
	prov := opt.Provider
	modelItem := opt.Model
	rowKey := p.KeyPrefix + opt.KeyPrefix + "model-opt-" + prov.ID + "-" + modelItem.ID

	name := modelItem.Name
	if name == "" {
		name = modelItem.ID
	}
	if name == "" {
		name = "默认模型"
	}
	if modelItem.IsDefault {
		name = defaultModelRowDisplayName(prov, modelItem)
	}
	btn := ui.ButtonBase(c.Key(rowKey)).
		Label(name).
		Tooltip(name).
		Checked(opt.IsChecked).
		Height(modelPickerRowHeight).
		Padding(Space2, Space3).
		Radius(RadiusMD).
		FillWidth()
	if opt.IsLocked {
		tip := opt.LockedReason
		if tip == "" {
			tip = "已有消息，只能切换到当前 Agent 的模型（下回合生效）"
		}
		btn.Disabled(true).Opacity(0.45).Background(ui.Transparent).Tooltip(tip)
	} else {
		switch {
		case opt.IsChecked:
			btn.Background(ColorSelectedDark)
		case btn.Hovered():
			btn.Background(ColorHoverDark)
		default:
			btn.Background(ui.Transparent)
		}
		btn.OnClick(func() {
			if opt.OnPick != nil {
				opt.OnPick(prov, modelItem)
			}
		})
	}
	btn.Children(func() {
		ui.Row(c.Key(rowKey + "-content")).
			AlignItems(ui.Center).
			Gap(Space2).
			FillWidth().
			Children(func() {
				ui.Box(c.Key(rowKey+"-icon-slot")).
					Size(IconSM, IconSM).
					Center().
					Shrink(0).
					Children(func() {
						if opt.ShowGroup {
							renderProviderIcon(c, prov.ID, IconSM)
						}
					})
				ui.Text(c, modelPickerRowName(prov, name)).
					FontSize(uiFont(TypeBody)).
					TextColor(modelPickerTextPrimary).
					SingleLine().
					Shrink(0)
				ui.Row(c.Key(rowKey + "-name-gap")).Grow(1)
				if opt.ShowQuota && p.QuotaUsed != nil {
					if usedPercent, ok := p.QuotaUsed(prov.ID); ok {
						clamped := math.Max(0, math.Min(100, usedPercent))
						ui.Text(c, fmt.Sprintf("%d%%", int(math.Round(clamped)))).
							FontSize(uiFont(TypeCaption)).
							TextColor(quotaPercentColor(c, clamped))
					}
				}
				// 收藏行已有供应商图标，仅在分组内的行右端展示实心星，未收藏的模型不显示星。
				if !opt.ShowGroup && providerHasPinnedModel(prov.ID, modelItem.ID, p.Pins) {
					ui.Box(c).
						Label("已收藏").
						Tooltip("已收藏").
						Size(IconSM, IconSM).
						Shrink(0).
						Children(func() {
							ui.Icon(c, IconByName("star-filled")).
								Size(IconSM, IconSM).
								TextColor(modelPickerTextPrimary)
						})
				}
				if opt.IsChecked {
					ui.Icon(c, IconCheck()).
						Size(IconSM, IconSM).
						TextColor(modelPickerTextPrimary)
				} else if modelItem.SupportsFast {
					ui.Icon(c, IconByName("zap")).
						Size(12, 12).
						TextColor(modelPickerTextSecondary)
				}
			})
	})
}

type modelRowOptions struct {
	KeyPrefix    string
	Provider     ProviderInfo
	Model        ModelInfo
	IsChecked    bool
	IsLocked     bool
	LockedReason string // 行被禁用时 Tooltip 显示的说明；为空时按锁定语义给默认文案
	ShowSubtitle bool
	SubtitleText string
	ShowGroup    bool
	ShowQuota    bool
	OnPick       func(prov ProviderInfo, m ModelInfo)
}
