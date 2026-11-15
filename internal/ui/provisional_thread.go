package mcodeui

import (
	"context"
	"strings"
)

// provisionalRetain 合并同一会话的转正请求，发送前也等待 Worktree 迁入完成。
type provisionalRetain struct {
	done     chan struct{}
	err      error
	retained bool
}

func withoutProvisional(threads []Thread) []Thread {
	visible := make([]Thread, 0, len(threads))
	for _, th := range threads {
		if !th.IsProvisional {
			visible = append(visible, th)
		}
	}
	return visible
}

func firstVisibleThreadID(threads []Thread) string {
	for _, th := range threads {
		if !th.IsProvisional {
			return th.ID
		}
	}
	return ""
}

// reconcileProvisionalThreadsLocked 防止清理延迟或在途列表快照把已离开的空会话重新带回来。
func (s *chatState) reconcileProvisionalThreadsLocked(threads []Thread) []Thread {
	result := make([]Thread, 0, len(threads))
	for _, th := range threads {
		if s.releasedProvisional[th.ID] {
			continue
		}
		if op := s.provisionalRetains[th.ID]; op != nil && op.retained {
			th.IsProvisional = false
		}
		result = append(result, th)
	}
	return result
}

// reconcileProjectThreadsLocked 保留新建期间已知的空会话，避免较早的列表快照抢走选中态。
func (s *chatState) reconcileProjectThreadsLocked(projectID string, threads []Thread) []Thread {
	result := s.reconcileProvisionalThreadsLocked(threads)
	for _, local := range s.threadsByProj[projectID] {
		if !local.IsProvisional || s.releasedProvisional[local.ID] || (!s.creatingThread && local.ID != s.selectedThreadID) {
			continue
		}
		found := false
		for _, th := range result {
			if th.ID == local.ID {
				found = true
				break
			}
		}
		if !found {
			result = append(result, local)
		}
	}
	return SortThreads(result)
}

// prepareProvisionalSend 等待已有转正请求；没有草稿的直接发送交给后端自动转正。
func (s *chatState) prepareProvisionalSend(threadID string) error {
	s.mu.Lock()
	op := s.provisionalRetains[threadID]
	worktree := s.provisionalWorktrees[threadID]
	s.mu.Unlock()
	if worktree {
		return s.retainProvisionalThread(threadID)
	}
	if op != nil {
		<-op.done
	}
	return nil
}

func (s *chatState) createProvisionalThread(projectID string, worktree bool) {
	if projectID == "" {
		s.setToast("新建会话失败: 项目不能为空")
		return
	}
	s.mu.Lock()
	if s.closed || s.creatingThread {
		s.mu.Unlock()
		return
	}
	s.creatingThread = true
	seq := s.watchSeq
	s.mu.Unlock()
	opts := s.newThreadOptions(projectID)
	opts.Provisional = true
	go func() {
		defer func() {
			s.mu.Lock()
			s.creatingThread = false
			s.mu.Unlock()
			s.invalidate()
		}()
		ctx := context.Background()
		id, err := s.client.CreateThreadWithOptions(ctx, projectID, opts)
		if err != nil {
			s.setToast("新建会话失败: " + err.Error())
			return
		}
		if err := s.refreshProjectThreads(ctx, projectID); err != nil {
			if releaseErr := s.client.ReleaseProvisionalThread(id); releaseErr != nil {
				s.setToast("释放临时会话失败: " + releaseErr.Error())
			} else {
				s.setToast("获取会话列表失败: " + err.Error())
			}
			return
		}
		s.mu.Lock()
		if worktree {
			if s.provisionalWorktrees == nil {
				s.provisionalWorktrees = make(map[string]bool)
			}
			s.provisionalWorktrees[id] = true
		}
		s.mu.Unlock()
		// 请求期间切走后不抢回页面，刚创建的空会话仍按离开流程清理。
		if !s.selectThreadState(id, true, &seq) {
			s.mu.Lock()
			s.releaseEmptyProvisionalLocked(id, "", nil, nil)
			s.mu.Unlock()
		}
	}()
}

// retainProvisionalThread 只更新会话元数据，不重新选中、不重拉消息。
func (s *chatState) retainProvisionalThread(threadID string) error {
	s.mu.Lock()
	if op := s.provisionalRetains[threadID]; op != nil {
		s.mu.Unlock()
		<-op.done
		return op.err
	}
	th, ok := s.threadSnapshotLocked(threadID)
	if s.closed || !ok || !th.IsProvisional || s.releasedProvisional[threadID] {
		s.mu.Unlock()
		return nil
	}
	op := &provisionalRetain{done: make(chan struct{})}
	if s.provisionalRetains == nil {
		s.provisionalRetains = make(map[string]*provisionalRetain)
	}
	s.provisionalRetains[threadID] = op
	s.mu.Unlock()

	err := s.client.RetainThread(threadID)
	s.mu.Lock()
	op.err = err
	worktree := false
	if err == nil {
		op.retained = true
		s.markThreadRetainedLocked(threadID)
		worktree = s.provisionalWorktrees[threadID]
		delete(s.provisionalWorktrees, threadID)
	} else {
		delete(s.provisionalRetains, threadID)
	}
	s.mu.Unlock()
	if worktree {
		if moveErr := s.client.MoveToWorktree(context.Background(), threadID, "", false); moveErr != nil {
			s.setToast("Worktree 创建失败：" + moveErr.Error())
		}
	}
	close(op.done)
	if err != nil {
		s.setToast("保留会话失败: " + err.Error())
	}
	s.invalidate()
	return err
}

func (s *chatState) markThreadRetainedLocked(threadID string) {
	for i := range s.threads {
		if s.threads[i].ID == threadID {
			s.threads[i].IsProvisional = false
		}
	}
	for _, list := range s.threadsByProj {
		for i := range list {
			if list[i].ID == threadID {
				list[i].IsProvisional = false
			}
		}
	}
}

func (s *chatState) releaseEmptyProvisionalLocked(threadID, text string, atts []composerAttachment, snippets []composerSnippet) {
	th, ok := s.threadSnapshotLocked(threadID)
	draft := s.drafts[threadID]
	if !ok || !th.IsProvisional || strings.TrimSpace(text) != "" || len(atts) != 0 || len(snippets) != 0 ||
		strings.TrimSpace(draft.Text) != "" || len(draft.Attachments) != 0 || len(draft.Snippets) != 0 || s.provisionalRetains[threadID] != nil {
		return
	}
	if s.releasedProvisional == nil {
		s.releasedProvisional = make(map[string]bool)
	}
	s.releasedProvisional[threadID] = true
	delete(s.provisionalWorktrees, threadID)
	s.clearDraftLocked(threadID)
	s.threads = s.reconcileProvisionalThreadsLocked(s.threads)
	for pid, list := range s.threadsByProj {
		s.threadsByProj[pid] = s.reconcileProvisionalThreadsLocked(list)
	}
	go func() {
		if err := s.client.ReleaseProvisionalThread(threadID); err != nil {
			s.setToast("释放临时会话失败: " + err.Error())
		}
	}()
}
