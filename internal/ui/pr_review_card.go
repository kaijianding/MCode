// PR 审查卡片渲染（```pr-review 围栏代码块）。
package mcodeui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// prReviewItem 表示 pr-review 代码块 JSON 数组中的单条审查意见。
type prReviewItem struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

// parsePRReviewItems 解析 pr-review 代码块中的 JSON 数组；非数组或非法 JSON 返回 false。
func parsePRReviewItems(raw string) ([]prReviewItem, bool) {
	var items []prReviewItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil || items == nil {
		return nil, false
	}
	return items, true
}

// prReviewCardTitle 返回审查卡片标题栏左侧文字。
func prReviewCardTitle(count int) string {
	if count <= 0 {
		return "没有发现问题"
	}
	return fmt.Sprintf("%d 个问题", count)
}

// prReviewLocationText 格式化文件定位文案；line <= 0 时只显示路径。
func prReviewLocationText(path string, line int) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	if line > 0 {
		return fmt.Sprintf("%s:%d", p, line)
	}
	return p
}

// openPRReviewLocation 在点击文件定位行时解析路径并跳转到文件面板对应行。
func openPRReviewLocation(s *chatState, path string, line int) {
	p := strings.TrimSpace(path)
	if s == nil || p == "" {
		return
	}
	threadID := selectedThreadIDSnapshot(s)
	if threadID == "" {
		return
	}
	workDir := turnFilesWorkDir(s, threadID)
	abs := resolveTurnFilePath(workDir, p)
	if abs == "" {
		return
	}
	first, last := 0, 0
	if line > 0 {
		first, last = line, line
	}
	openTurnFileAt(s, threadID, workDir, turnEditedFile{path: p, abs: abs}, first, last)
}

// renderPRReviewCard 把 ```pr-review 代码块渲染为审查卡片；流式未闭合或 JSON 解析失败时
// 退回通用代码块容器原样显示，避免丢失内容。
func renderPRReviewCard(c *ui.Context, s *chatState, b *markdown.CodeBlockView) {
	if b == nil {
		return
	}
	if b.Open {
		renderCodeBlockContainer(c, s, b)
		return
	}
	items, ok := parsePRReviewItems(b.Code())
	if !ok {
		renderCodeBlockContainer(c, s, b)
		return
	}

	key := fmt.Sprintf("code-block-%d", b.Index)
	codeFont := "monospace"
	if st := getChatSettings(s); st != nil {
		codeFont = codeFontFamily(st.cfg.Appearance)
	}

	ui.Column(c.Key(key)).
		Direction(ui.LeftToRight).
		AlignSelf(ui.Stretch).
		Background(ThemeSurface(c)).
		Border(1, ThemeBorder(c)).
		Radius(RadiusLG).
		Clip().
		Children(func() {
			header := ui.Row(c.Key(key+"-header")).
				Height(codeBlockHeaderHeight).
				AlignItems(ui.Center).
				Padding(Space1, Space2).
				Background(ThemeHover(c))
			if len(items) > 0 {
				header.BorderWidth(0, 0, 1, 0).BorderColor(ThemeBorder(c))
			}
			header.Children(func() {
				ui.Text(c, prReviewCardTitle(len(items))).
					FontSize(scaledFont(TypeCaption)).
					TextColor(ThemeSecondaryText(c)).
					Unselectable()
				ui.Spacer(c)
				copyKey := key + "-copy"
				copied := copyFeedbackActive(copyKey, time.Now())
				CopyButton(c, CopyButtonProps{
					Key: copyKey, Label: "复制代码", Tooltip: "复制代码", Text: "复制",
					Copied: copied, Disabled: b.Open, Value: b.Code,
					OnCopy: func() {
						markCopyFeedback(s, copyKey)
						if s != nil {
							s.setToast("已复制")
						}
					},
				})
			})

			for i, item := range items {
				itemCol := ui.Column(c.Key(fmt.Sprintf("%s-item-%d", key, i))).
					FillWidth().
					Padding(Space2, Space2).
					Gap(Space0)
				if i > 0 {
					itemCol.BorderWidth(Hairline, 0, 0, 0).BorderColor(ThemeBorder(c))
				}
				itemCol.Children(func() {
					loc := prReviewLocationText(item.Path, item.Line)
					if loc != "" {
						locBtn := ui.ButtonBase(c.Key(fmt.Sprintf("%s-loc-%d", key, i))).
							Label(loc).
							Tooltip(loc).
							FillWidth().
							MinHeight(ControlSM).
							Padding(Space1, Space2).
							Radius(RadiusSM).
							Justify(ui.Start).
							AlignItems(ui.Center).
							Cursor(ui.CursorPointer).
							Transition(motionFeedback(c))
						if locBtn.Hovered() {
							locBtn.Background(ThemeHover(c))
						}
						itemPath, itemLine := item.Path, item.Line
						locBtn.OnClick(func() {
							openPRReviewLocation(s, itemPath, itemLine)
						})
						locBtn.Children(func() {
							ui.Text(c, loc).
								Font(codeFont).
								FontSize(scaledFont(TypeCaption)).
								TextColor(ThemeSecondaryText(c)).
								SingleLine().
								Ellipsis("…").
								Shrink(1).
								MinWidth(0)
						})
					}
					if body := strings.TrimSpace(item.Body); body != "" {
						ui.Box(c.Key(fmt.Sprintf("%s-body-%d", key, i))).
							FillWidth().
							Padding(0, Space2, Space1, Space2).
							Children(func() {
								ui.Text(c, body).
									FontSize(scaledFont(TypeBody)).
									LineHeight(1.45).
									TextColor(ThemePrimaryText(c)).
									FillWidth().
									MinWidth(0).
									Selectable()
							})
					}
				})
			}
		})
}
