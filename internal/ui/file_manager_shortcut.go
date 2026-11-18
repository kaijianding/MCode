package mcodeui

// fileManagerHasClosableTab 报告文件面板当前是否有可关闭的预览标签页。
// 面板没打开或没有激活的 tab 时没有可关的内容。
func fileManagerHasClosableTab(s *chatState) bool {
	st := getFileManagerState(s)
	if st == nil {
		return false
	}
	// 打开状态由右栏容器判断，需在拿 fileManagerMu 之前查询。
	open := isFileManagerOpen(s)
	if !open {
		return false
	}
	fileManagerMu.Lock()
	defer fileManagerMu.Unlock()
	return st.active != ""
}

// fileManagerCloseActiveTab 关闭文件面板里当前激活的标签页，并切到相邻标签页。
// 关完最后一个标签页时回到文件树，面板本身仍然打开。
// 没有可关闭的标签页时返回 false，关闭成功返回 true。
//
// 调用方（关闭菜单项与 root.go 的动作分发）不需要改动。
func fileManagerCloseActiveTab(s *chatState) bool {
	st := getFileManagerState(s)
	if st == nil {
		return false
	}
	// 打开状态由右栏容器判断，需在拿 fileManagerMu 之前查询。
	if !isFileManagerOpen(s) {
		return false
	}
	fileManagerMu.Lock()
	if st.active == "" {
		fileManagerMu.Unlock()
		return false
	}
	path := st.active
	fileManagerMu.Unlock()

	s.closeFileManagerTab(path)
	return true
}

// fileManagerQuickLookSelected 在 macOS 上对文件树当前选中的本地文件触发系统快速查看。
func fileManagerQuickLookSelected(s *chatState) bool {
	if fileManagerGOOS != "darwin" || s == nil || !isFileManagerOpen(s) {
		return false
	}
	st := getFileManagerState(s)
	if st == nil {
		return false
	}
	fileManagerMu.Lock()
	threadID := st.threadID
	tree := st.tree
	if tree == nil || st.query != "" {
		fileManagerMu.Unlock()
		return false
	}
	outline := fileTreeActiveOutlineLocked(tree)
	selected := tree.selected
	selection := &tree.selection
	if tree.onlyChanged {
		selected = tree.changedSelected
		selection = &tree.changedSelection
	}
	var entry FileEntry
	var found bool
	if selected >= 0 && selected < outline.Rows() {
		path := outline.Item(selected)
		if selection.Has(path) {
			entry, found = tree.entries[path]
		}
	}
	if !found && selection.Len() == 1 {
		for path := range selection.All() {
			entry, found = tree.entries[path]
			break
		}
	}
	deleted := found && tree.onlyChanged && tree.git[entry.Path].Status == "D"
	fileManagerMu.Unlock()

	if !found || entry.IsDir || entry.Protected || deleted {
		return false
	}
	abs := fileManagerWorkspacePath(s, threadID, entry.Path)
	if !fileManagerLocalPath(abs) {
		return false
	}
	_ = fileManagerPreviewItem(abs)
	return true
}
