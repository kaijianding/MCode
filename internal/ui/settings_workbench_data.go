package mcodeui

import (
	"sync"
	"sync/atomic"

	"mcode/internal/projctx"
)

// workbenchContextBuilds 仅用于测试观测 projctx.New 的实际构建次数。
var workbenchContextBuilds atomic.Uint64

type workbenchContextCache struct {
	mu           sync.Mutex
	projectPath  string
	loadingPath  string
	loaded       bool
	manifest     *projctx.Manifest
	guidanceInit bool
	lastGuidance bool
}

func invalidateWorkbenchContextCache(st *SettingsState) {
	if st == nil {
		return
	}
	st.workbenchCtx.mu.Lock()
	st.workbenchCtx.loaded = false
	st.workbenchCtx.mu.Unlock()
	st.invalidate()
}

// workbenchContextManifest 按 projectPath 缓存项目文档清单，并在后台 goroutine 中重算。
func workbenchContextManifest(st *SettingsState) *projctx.Manifest {
	if st == nil {
		return nil
	}
	st.mu.Lock()
	chat := st.chat
	st.mu.Unlock()
	if chat == nil || chat.client == nil {
		return nil
	}
	projectPath := chat.getSelectedProjectPath()
	if projectPath == "" {
		return nil
	}
	st.workbenchCtx.mu.Lock()
	if st.workbenchCtx.loaded && st.workbenchCtx.projectPath == projectPath {
		m := st.workbenchCtx.manifest
		st.workbenchCtx.mu.Unlock()
		return m
	}
	var cached *projctx.Manifest
	if st.workbenchCtx.projectPath == projectPath {
		cached = st.workbenchCtx.manifest
	}
	if st.workbenchCtx.loadingPath == projectPath {
		st.workbenchCtx.mu.Unlock()
		return cached
	}
	st.workbenchCtx.loadingPath = projectPath
	st.workbenchCtx.mu.Unlock()

	go func(path string) {
		workbenchContextBuilds.Add(1)
		var result *projctx.Manifest
		if svc, err := projctx.New(path); err == nil {
			if manifest, err := svc.Manifest(); err == nil {
				m := manifest
				result = &m
			}
		}
		st.workbenchCtx.mu.Lock()
		if st.workbenchCtx.loadingPath == path {
			st.workbenchCtx.loadingPath = ""
			st.workbenchCtx.projectPath = path
			st.workbenchCtx.loaded = true
			st.workbenchCtx.manifest = result
		}
		st.workbenchCtx.mu.Unlock()
		st.invalidate()
	}(projectPath)
	return cached
}
