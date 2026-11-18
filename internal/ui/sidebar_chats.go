package mcodeui

import "github.com/egoist/mygo/ui"

// chatsClient 是「对话」内置项目的可选客户端能力，只返回内置对话项目 ID。
type chatsClient interface {
	ChatsProjectID() string
}

// chatsProjectID 返回内置「对话」项目的 ID，客户端不支持时返回空串。
// 侧栏每帧都会调用它，因此只做类型断言加一次接口调用，不碰 s.mu，不做任何 IO。
func (s *chatState) chatsProjectID() string {
	if s == nil || s.client == nil {
		return ""
	}
	c, ok := s.client.(chatsClient)
	if !ok {
		return ""
	}
	return c.ChatsProjectID()
}

// renderSidebarChatsSection 渲染侧栏顶部的「对话」段：段标题与右侧「+」按钮，
// 下方按最近活动平铺该项目的顶层会话；没有会话时仅显示段标题，chatsProjectID 为空时整段不渲染。
func renderSidebarChatsSection(c *ui.Context, s *chatState) {
	chatsProjID := s.chatsProjectID()
	if chatsProjID == "" {
		return
	}

	s.mu.Lock()
	providers := append([]ProviderInfo(nil), s.providers...)
	rawThreads := withoutProvisional(s.threadsByProj[chatsProjID])
	selectedID := s.selectedThreadID
	s.mu.Unlock()

	m := currentSidebarMetrics(c, s)

	// 一行段标题：左侧文字「对话」，右侧「+」图标按钮，不挂载项目右键菜单。
	headerRow := ui.Row(c.Key("chats-section-header")).
		Height(m.rowHeight).
		Padding(0, m.padX).
		Margin(1, ListInset, 1, ListInset).
		Radius(RowCornerRadius).
		AlignItems(ui.Center)

	headerRow.Children(func() {
		ui.Text(c, "对话").
			FontSize(sidebarFont(FontSizeThreadTitle)).
			FontWeight(500).
			TextColor(ThemePrimaryText(c)).
			Grow(1).
			SingleLine()

		IconButton(c.Key("chats-add-thread"), IconButtonProps{
			Icon:    IconPlus(),
			Size:    ControlMD,
			Variant: ButtonGhost,
			Label:   "新建对话",
			OnClick: func() {
				s.mu.Lock()
				if s.newThreadProvider == "" {
					s.newThreadProvider = defaultNewThreadProvider(s.providers, newThreadProviderContext{threads: s.threads, stewardThreadID: s.stewardThreadID()})
				}
				s.mu.Unlock()
				s.closeWorkspace()
				s.createThread(chatsProjID)
			},
		})
	})

	// 按最近活动平铺顶层会话，子代理仍通过 GroupThreadsByParent 挂载到父会话行下。
	topThs, beesByParent := GroupThreadsByParent(rawThreads)
	topThs = SortThreads(topThs)

	for _, th := range topThs {
		renderSidebarThreadRow(c, s, sidebarThreadRowOptions{
			projectID: chatsProjID,
			thread:    th,
			bees:      beesByParent[th.ID],
			dormant:   th.Dormant,
			selected:  th.ID == selectedID,
			rowHeight: m.rowHeight,
			padY:      m.padY,
			padX:      m.padX,
			compact:   m.compact,
			providers: providers,
		})
	}

	ui.Box(c.Key("chats-section-gap")).Height(m.projectGap)
}
