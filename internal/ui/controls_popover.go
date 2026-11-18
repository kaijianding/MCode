package mcodeui

import "github.com/egoist/mygo/ui"

type PopoverAlign int

const (
	BelowStart PopoverAlign = iota
	BelowEnd
	AboveStart
	AboveEnd
)

type PopoverProps struct {
	Key       any
	Open      *bool
	Align     PopoverAlign
	Width     float32
	MaxWidth  float32
	MaxHeight float32
	OnDismiss func()
}

type anchoredPopoverProps struct {
	Key                           any
	Open                          *bool
	Align                         PopoverAlign
	MinWidth, MaxWidth, MaxHeight float32
	OnDismiss                     func()
}

func anchoredPopover(c *ui.Context, anchor ui.Element, p anchoredPopoverProps, content func(ui.Element)) ui.Element {
	if p.Open == nil || !*p.Open {
		return ui.Element{}
	}
	ctx := c
	if p.Key != nil {
		ctx = c.Key(p.Key)
	}
	panel := ui.PopoverBase(ctx, anchor, p.Open, func(panel ui.Element) {
		switch p.Align {
		case BelowEnd:
			panel.AttachTo(anchor, ui.AnchorBottomRight, ui.AnchorTopRight).Margin(Space1, 0, 0, 0)
		case AboveStart:
			panel.AttachTo(anchor, ui.AnchorTopLeft, ui.AnchorBottomLeft).Margin(0, 0, Space1, 0)
		case AboveEnd:
			panel.AttachTo(anchor, ui.AnchorTopRight, ui.AnchorBottomRight).Margin(0, 0, Space1, 0)
		default:
			panel.AttachTo(anchor, ui.AnchorBottomLeft, ui.AnchorTopLeft).Margin(Space1, 0, 0, 0)
		}
		if p.MinWidth > 0 {
			panel.MinWidth(p.MinWidth)
		}
		if p.MaxWidth > 0 {
			panel.MaxWidth(p.MaxWidth)
		}
		if p.MaxHeight > 0 {
			panel.MaxHeight(p.MaxHeight)
		}
		panel.Transition(motionPopover(c, popoverRiseOffset(panel, anchor)))
		if content != nil {
			content(panel)
		}
	})
	if panel.Dismissed() && p.OnDismiss != nil {
		p.OnDismiss()
	}
	return panel
}

func popoverCardStyle(c *ui.Context, card ui.Element) ui.Element {
	return applyElevation(c, card.Radius(RadiusLG).Background(ThemeSurfacePopover(c)).Border(Hairline, ThemeHairline(c)), ElevPopover)
}

func PopoverCard(c *ui.Context, anchor ui.Element, p PopoverProps, content func()) ui.Element {
	return anchoredPopover(c, anchor, anchoredPopoverProps{Key: p.Key, Open: p.Open, Align: p.Align, MaxWidth: p.MaxWidth, MaxHeight: p.MaxHeight, OnDismiss: p.OnDismiss}, func(_ ui.Element) {
		card := popoverCardStyle(c, ui.Column(c).Padding(Space2))
		if p.Width > 0 {
			card.Width(p.Width)
		}
		if p.MaxWidth > 0 {
			card.MaxWidth(p.MaxWidth)
		}
		if p.MaxHeight > 0 {
			card.MaxHeight(p.MaxHeight)
		}
		card.Children(func() {
			if content != nil {
				content()
			}
		})
	})
}
