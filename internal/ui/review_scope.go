package mcodeui

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func reviewScope(scope string) string {
	switch scope {
	case "session", "uncommitted", "pr":
		return scope
	default:
		return "turn"
	}
}

func reviewScopeTitle(scope string) string {
	switch reviewScope(scope) {
	case "pr":
		return "PR"
	case "session":
		return "整个会话"
	case "uncommitted":
		return "未提交"
	default:
		return "本轮"
	}
}

func (s *chatState) loadReview(dp *diffPanelState, client reviewClient, threadID, turnID string) {
	loadReviewPreference(s, dp, threadID)
	diffPanelMu.Lock()
	scope, number := reviewScope(dp.scope), dp.prNumber
	if scope != "turn" {
		turnID = ""
	}
	dp.scope = scope
	dp.requestGen++
	gen := dp.requestGen
	dp.threadID, dp.turnID = threadID, turnID
	dp.loading, dp.err = threadID != "", ""
	// 旧正文请求不能阻止新批次重新取数。
	for key, entry := range dp.contents {
		if entry.loading || entry.err != "" {
			delete(dp.contents, key)
		}
	}
	if threadID == "" {
		dp.snapshot = ReviewSnapshot{}
		dp.selectedPath = ""
	}
	diffPanelMu.Unlock()
	s.invalidate()
	if threadID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		snapshot, err := getReview(ctx, client, threadID, scope, turnID, number, "")
		diffPanelMu.Lock()
		if dp.requestGen != gen || dp.threadID != threadID || reviewScope(dp.scope) != scope || dp.turnID != turnID || dp.prNumber != number {
			diffPanelMu.Unlock()
			return
		}
		dp.loading = false
		if err != nil {
			dp.err = err.Error()
		} else {
			snapshot.Scope = scope
			if scope == "pr" {
				snapshot.Number = number
			}
			if scope == "turn" && snapshot.TurnID == "" {
				snapshot.TurnID = turnID
			}
			dp.snapshot = snapshot
			selected := dp.pendingPath
			if selected == "" {
				selected = dp.selectedPath
			}
			dp.selectedPath = ""
			for _, file := range snapshot.Files {
				if toolDiffPathMatch(file.Path, selected) {
					dp.selectedPath = file.Path
					break
				}
			}
			dp.pendingPath = ""
		}
		diffPanelMu.Unlock()
		if err == nil {
			rememberReviewPreference(s, dp, threadID)
		}
		s.invalidate()
	}()
}

func (s *chatState) selectReviewScope(dp *diffPanelState, scope string) {
	diffPanelMu.Lock()
	dp.scope, dp.scopeMenuOpen = reviewScope(scope), false
	threadID := dp.threadID
	diffPanelMu.Unlock()
	inspectorFollowPause(s, threadID)
	s.loadDiff(threadID, "")
}

func renderReviewScope(c *ui.Context, s *chatState, dp *diffPanelState) {
	diffPanelMu.Lock()
	scope, open, number := reviewScope(dp.scope), dp.scopeMenuOpen, dp.prNumber
	diffPanelMu.Unlock()
	title := func(value string) string {
		if value == "pr" {
			return fmt.Sprintf("PR #%d", number)
		}
		return reviewScopeTitle(value)
	}
	trigger := ui.ButtonBase(c.Key("review-scope-trigger")).Label(title(scope)).Padding(Space1, Space3).Radius(RadiusSM)
	triggerScale := motionPressScale(c, trigger, "button-press-scale", trigger.Pressed())
	trigger.Scale(triggerScale, triggerScale)
	if trigger.Clicked() {
		open = !open
		diffPanelMu.Lock()
		dp.scopeMenuOpen = open
		diffPanelMu.Unlock()
	}
	trigger.Children(func() {
		ui.Text(c, title(scope)).FontSize(uiFont(TypeFootnote)).TextColor(ThemePrimaryText(c))
	})
	bg := ThemeSurfacePopover(c)
	native := flyoutHostWindow(c.Services()) != nil
	host := c.Services()
	appFlyoutPopover(c, "review-scope-popover", trigger, &open, func() {
		diffPanelMu.Lock()
		dp.scopeMenuOpen = false
		diffPanelMu.Unlock()
	}, appFlyoutProps{Width: 150, Background: &bg}, func(c *ui.Context) {
		card := ui.Column(c.Key("review-scope-menu"))
		if !native {
			card = popoverCard(c, card)
		}
		card.Transition(motionPopover(c, Space2)).Padding(Space1).Children(func() {
			values := []string{"turn", "session", "uncommitted"}
			if _, ok := s.client.(reviewPRClient); ok && number > 0 {
				values = append(values, "pr")
			}
			for _, value := range values {
				value := value
				btn := ui.ButtonBase(c.Key("review-scope-"+value)).Label(title(value)).FillWidth().Padding(Space2, Space3).Radius(RadiusXS)
				if value == scope {
					btn.Background(ThemeSelected(c))
				}
				if btn.Clicked() {
					closeAppFlyoutPopover(host, "review-scope-popover")
					s.selectReviewScope(dp, value)
					host.Invalidate()
				}
				btn.Children(func() {
					ui.Text(c, title(value)).FontSize(uiFont(TypeFootnote)).TextColor(ThemePrimaryText(c))
				})
			}
		})
	})
}

// 轮次来自消息记录，不为获取菜单而额外读取整批 patch。
func reviewTurns(s *chatState) ThreadDiff {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := ThreadDiff{}
	seen := map[string]bool{}
	for _, m := range s.messages {
		if m.TurnID != "" && !seen[m.TurnID] {
			seen[m.TurnID] = true
			data.Turns = append(data.Turns, DiffTurn{TurnID: m.TurnID, Index: m.TurnIndex, HasChanges: true})
		}
	}
	return data
}
