package mcodeui

import (
	"go/ast"
	"testing"
)

// 禁止在 internal/ui 的生产代码中直接调用 ui.Segmented / ui.SegmentedBase（controls_toggle.go 除外），统一使用共用 Segmented / SegmentedFill。
func TestNoRawSegmented(t *testing.T) {
	for _, pf := range uiProdFiles(t) {
		if pf.Name == "controls_toggle.go" {
			continue
		}
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
			if sel.Sel.Name != "Segmented" && sel.Sel.Name != "SegmentedBase" {
				return true
			}
			pos := fset.Position(call.Pos())
			t.Errorf("%s:%d: 禁止直接调用 ui.%s(，请改用共用 Segmented 或 SegmentedFill", pos.Filename, pos.Line, sel.Sel.Name)
			return true
		})
	}
}
