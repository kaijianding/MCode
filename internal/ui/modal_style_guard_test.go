package mcodeui

import (
	"go/ast"
	"testing"
)

// 禁止在 internal/ui 的生产代码中调用 ui.Modal / ui.Popover（它们自带背景、内边距和阴影，会让自绘卡片外多一圈黑边）。
func TestNoStyledModalOrPopover(t *testing.T) {
	for _, pf := range uiProdFiles(t) {
		fset, file := pf.Fset, pf.File
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "ui" {
				return true
			}
			if sel.Sel.Name != "Modal" && sel.Sel.Name != "Popover" {
				return true
			}
			t.Errorf("%s: 禁止使用 ui.%s：ui.Modal / ui.Popover 自带背景、内边距和阴影，会让自绘卡片外多一圈黑边；弹窗请用 ui.DialogBase 或 modalDragModal，浮层请用 ui.PopoverBase 或 appPopover", fset.Position(call.Pos()), sel.Sel.Name)
			return true
		})
	}
}
