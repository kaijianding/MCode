package mcodeui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// newPendingTestState 构造带一个项目与一个已有会话的 chatState，便于断言侧栏与选中态。
func newPendingTestState(t *testing.T) (*FakeClient, *chatState) {
	t.Helper()
	fake := NewFakeClient()
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "Proj1"}},
		map[string][]Thread{
			"p1": {{ID: "t1", ProjectID: "p1", Title: "已有会话", UpdatedAt: now}},
		},
	)
	fake.SetProviders([]ProviderInfo{{ID: "claude", Name: "Claude", Available: true}})

	state := newChatState(fake)
	t.Cleanup(state.close)
	state.mu.Lock()
	state.projects = []Project{{ID: "p1", Name: "Proj1"}}
	state.threadsByProj = map[string][]Thread{
		"p1": {{ID: "t1", ProjectID: "p1", Title: "已有会话", UpdatedAt: now}},
	}
	state.threads = []Thread{{ID: "t1", ProjectID: "p1", Title: "已有会话", UpdatedAt: now}}
	state.selectedThreadID = "t1"
	state.newThreadProvider = "claude"
	state.mu.Unlock()
	return fake, state
}

func createTestProvisionalThread(t *testing.T, state *chatState, worktree bool) string {
	t.Helper()
	state.mu.Lock()
	old := state.selectedThreadID
	state.mu.Unlock()
	if worktree {
		state.createThreadInWorktree("p1")
	} else {
		state.createThread("p1")
	}
	var id string
	waitUntil(t, "创建并选中临时会话", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		id = state.selectedThreadID
		return id != "" && id != old && !state.creatingThread
	})
	return id
}

func TestProvisionalThreadCreateEmptyHero(t *testing.T) {
	fake, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)
	if !fake.LastNewThreadOptions().Provisional || fake.CreateThreadCalls() != 1 {
		t.Fatal("新建应立即调用一次 Provisional=true 的创建请求")
	}
	state.mu.Lock()
	th, ok := state.threadSnapshotLocked(id)
	visible := withoutProvisional(state.threadsByProj["p1"])
	loading := state.timelineLoadingThread
	state.mu.Unlock()
	if !ok || !th.IsProvisional || len(visible) != 1 || visible[0].ID != "t1" {
		t.Fatalf("保留真实行并从展示列表隐藏，thread=%+v visible=%+v", th, visible)
	}
	if loading != "" || state.composerDockDecisionFor(true, id).Docked {
		t.Fatalf("空会话不应显示骨架或停靠输入框，loading=%q", loading)
	}
	if got := fake.HistoryCalls(); len(got) != 0 {
		t.Fatalf("已知为空的新会话不应拉取 History，got %v", got)
	}
	for _, item := range buildPaletteItems(state) {
		if item.id == "thread-"+id {
			t.Fatal("命令面板不应显示临时会话")
		}
	}
}

func TestProvisionalThreadRetainOncePreservesTimeline(t *testing.T) {
	fake, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)
	state.mu.Lock()
	state.pendingDraftFill = nil
	state.draft = "你好"
	state.messages = []Message{{ID: "keep", Text: "保留"}}
	state.mu.Unlock()
	for i := 0; i < 10; i++ {
		state.noteComposerDraft(id, "你好")
	}
	waitUntil(t, "首次输入转正", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		th, _ := state.threadSnapshotLocked(id)
		return !th.IsProvisional
	})
	state.mu.Lock()
	if len(state.messages) != 1 || state.messages[0].ID != "keep" || state.selectedThreadID != id || len(withoutProvisional(state.threads)) != 2 {
		t.Errorf("转正不应重置页面，messages=%+v selected=%q", state.messages, state.selectedThreadID)
	}
	state.mu.Unlock()
	fake.mu.Lock()
	calls := len(fake.retainThreadCalls)
	fake.mu.Unlock()
	if calls != 1 || len(fake.HistoryCalls()) != 0 {
		t.Fatalf("首次输入只转正一次且不重拉历史，retain=%d history=%v", calls, fake.HistoryCalls())
	}
}

func TestProvisionalThreadReleaseOnlyEmptyDraft(t *testing.T) {
	for _, kind := range []string{"empty", "whitespace", "text", "attachment", "snippet"} {
		t.Run(kind, func(t *testing.T) {
			fake, state := newPendingTestState(t)
			id := createTestProvisionalThread(t, state, false)
			state.mu.Lock()
			state.pendingDraftFill = nil
			switch kind {
			case "whitespace":
				state.draft = " \t\n"
			case "text":
				state.draft = "草稿"
			case "attachment":
				state.attachments = []composerAttachment{{ID: "att", LocalID: "ready"}}
			case "snippet":
				state.snippets = []composerSnippet{{ID: 1, Text: "片段"}}
			}
			state.mu.Unlock()
			if kind == "whitespace" {
				state.noteComposerDraft(id, " \t\n")
			}
			state.selectThread("t1")
			state.mu.Lock()
			_, exists := state.threadSnapshotLocked(id)
			_, draftExists := state.drafts[id]
			state.mu.Unlock()
			if kind == "empty" || kind == "whitespace" {
				waitUntil(t, "离开空会话后释放", func() bool {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					return len(fake.releaseProvisionalCalls) == 1
				})
				if exists || draftExists {
					t.Fatal("空会话应从源列表移除并清理草稿")
				}
				state.refreshProjectThreads(context.Background(), "p1")
				state.mu.Lock()
				_, resurrected := state.threadSnapshotLocked(id)
				state.mu.Unlock()
				if resurrected {
					t.Fatal("延迟删除前的列表刷新不应复活已释放会话")
				}
			} else {
				fake.mu.Lock()
				calls := len(fake.releaseProvisionalCalls)
				fake.mu.Unlock()
				if !exists || !draftExists || calls != 0 {
					t.Fatalf("有草稿时应保留，exists=%v draft=%v release=%d", exists, draftExists, calls)
				}
			}
		})
	}
}

func TestProvisionalThreadWorktreeOnRetain(t *testing.T) {
	fake, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, true)
	fake.mu.Lock()
	before := len(fake.moveToWorktreeCalls)
	fake.mu.Unlock()
	if before != 0 {
		t.Fatal("临时会话创建时不应创建 Worktree")
	}
	if err := state.retainProvisionalThread(id); err != nil {
		t.Fatal(err)
	}
	if err := state.retainProvisionalThread(id); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.moveToWorktreeCalls) != 1 || fake.moveToWorktreeCalls[0].ThreadID != id {
		t.Fatalf("转正时应只迁入一次 Worktree，got %+v", fake.moveToWorktreeCalls)
	}
}

func TestProvisionalThreadRetainRetry(t *testing.T) {
	fake, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)
	fake.mu.Lock()
	fake.retainThreadErr = errors.New("retain failed")
	fake.mu.Unlock()
	state.noteComposerDraft(id, "草稿")
	waitUntil(t, "转正失败提示", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return strings.Contains(state.toastMessage, "retain failed")
	})
	fake.mu.Lock()
	fake.retainThreadErr = nil
	fake.mu.Unlock()
	state.noteComposerDraft(id, "草稿重试")
	waitUntil(t, "后续输入可重试转正", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		th, _ := state.threadSnapshotLocked(id)
		return !th.IsProvisional
	})
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.retainThreadCalls) != 2 {
		t.Fatalf("失败后应允许重试，got %v", fake.retainThreadCalls)
	}
}

type blockedProvisionalClient struct {
	*FakeClient
	started   chan struct{}
	gate      chan struct{}
	createErr error
}

func (f *blockedProvisionalClient) CreateThreadWithOptions(ctx context.Context, projectID string, opts NewThreadOptions) (string, error) {
	close(f.started)
	<-f.gate
	if f.createErr != nil {
		return "", f.createErr
	}
	return f.FakeClient.CreateThreadWithOptions(ctx, projectID, opts)
}

func TestProvisionalThreadCreateCancellationAndDuplicate(t *testing.T) {
	fake, state := newPendingTestState(t)
	blocked := &blockedProvisionalClient{FakeClient: fake, started: make(chan struct{}), gate: make(chan struct{})}
	state.client = blocked
	state.createThread("p1")
	<-blocked.started
	state.createThread("p1")
	state.selectThread("t1")
	close(blocked.gate)
	waitUntil(t, "取消创建后的空会话被释放", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.releaseProvisionalCalls) == 1
	})
	state.mu.Lock()
	selected := state.selectedThreadID
	state.mu.Unlock()
	if fake.CreateThreadCalls() != 1 || selected != "t1" {
		t.Fatalf("快速连点应合并，切走后不抢回页面，create=%d selected=%q", fake.CreateThreadCalls(), selected)
	}
}

func TestProvisionalThreadCreateFailureKeepsPage(t *testing.T) {
	fake, state := newPendingTestState(t)
	blocked := &blockedProvisionalClient{FakeClient: fake, started: make(chan struct{}), gate: make(chan struct{}), createErr: errors.New("create failed")}
	state.client = blocked
	state.mu.Lock()
	state.messages = []Message{{ID: "keep"}}
	state.mu.Unlock()
	state.createThread("p1")
	<-blocked.started
	close(blocked.gate)
	waitUntil(t, "创建失败结束", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return !state.creatingThread
	})
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.selectedThreadID != "t1" || len(state.messages) != 1 || !strings.Contains(state.toastMessage, "create failed") {
		t.Fatalf("创建失败应保持原页面并提示，selected=%q messages=%v toast=%q", state.selectedThreadID, state.messages, state.toastMessage)
	}
}

func TestProvisionalThreadPasteAndAttachmentRetain(t *testing.T) {
	for _, kind := range []string{"paste", "attachment"} {
		t.Run(kind, func(t *testing.T) {
			_, state := newPendingTestState(t)
			id := createTestProvisionalThread(t, state, false)
			if kind == "paste" {
				if !state.pasteLongText(strings.Repeat("粘贴文本\n", 200)) {
					t.Fatal("应产生粘贴卡片")
				}
			} else if !state.addAttachment("a.txt", "text/plain", []byte("附件")) {
				t.Fatal("应添加附件")
			}
			waitUntil(t, "粘贴或附件转正", func() bool {
				state.mu.Lock()
				defer state.mu.Unlock()
				th, _ := state.threadSnapshotLocked(id)
				return !th.IsProvisional
			})
		})
	}
}

func TestProvisionalThreadStaleProjectSnapshotPreservesSelection(t *testing.T) {
	_, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)
	state.mu.Lock()
	defer state.mu.Unlock()
	loaded := state.reconcileProjectThreadsLocked("p1", []Thread{{ID: "t1", ProjectID: "p1"}})
	if len(loaded) != 2 || firstVisibleThreadID(loaded) != "t1" {
		t.Fatalf("旧快照应保留选中的临时会话并继续隐藏，got %+v", loaded)
	}
	found := false
	for _, th := range loaded {
		found = found || th.ID == id
	}
	if !found {
		t.Fatal("较早的快照不应丢掉新会话 ID")
	}
}

func TestProvisionalThreadSendWithoutDraftUsesBackendPromotion(t *testing.T) {
	fake, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)
	state.sendMessage("第一条")
	waitUntil(t, "直接发送转正", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		th, _ := state.threadSnapshotLocked(id)
		return !state.sending && !th.IsProvisional
	})
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.retainThreadCalls) != 0 || len(fake.sendCalls) != 1 || fake.sendCalls[0].ThreadID != id {
		t.Fatalf("直接按真实 ID 发送，由后端转正，retain=%v send=%v", fake.retainThreadCalls, fake.sendCalls)
	}
}

// 新建会话时模型回退到目录里的其它模型，不得把回退结果写回“上次使用”的记忆。
func TestProvisionalThreadCreateKeepsLastModelSelection(t *testing.T) {
	// 整包运行时数据目录是全局共享的，先等上一轮后台写盘结束并把全局记忆状态与数据目录复位，
	// 避免别的用例遗留的 ui-state.json 或残留写盘 goroutine 把这里的“上次选择”冲成别的值。
	resetLastSelectionMergeState(t)
	fake, state := newPendingTestState(t)
	// 设置里的默认 Agent 记为 codex：建会话不应把它改成当前输入框的 Agent。
	st := NewSettingsState(settingsTestDir(t), nil, state)
	st.cfg.Models.Provider = "codex"
	setChatSettings(state, st)
	providerBefore := getChatSettings(state).cfg.Models.Provider
	// 目录里只剩 claude/sonnet，记忆里的 claude/opus-removed 已下架。
	state.mu.Lock()
	state.providers = []ProviderInfo{{ID: "claude", Name: "Claude", Models: []ModelInfo{{ID: "sonnet"}}}}
	state.newThreadProvider = "claude"
	state.mu.Unlock()

	remembered := ModelSelection{Provider: "claude", Model: "opus-removed", Effort: "high", Fast: true}
	setLastModelSelection(&remembered)
	defer setLastModelSelection(nil)

	createTestProvisionalThread(t, state, false)

	if got := fake.CreateThreadCalls(); got != 1 {
		t.Fatalf("应恰好创建一次，got %d", got)
	}
	if opts := fake.LastNewThreadOptions(); opts.Model == "opus-removed" {
		t.Fatalf("已下架的记忆模型不应被沿用，got %+v", opts)
	}
	if got := getChatSettings(state).cfg.Models.Provider; got != providerBefore {
		t.Fatalf("建会话不应改写设置里的默认 Agent，want %q，got %q", providerBefore, got)
	}
	last, ok := loadLastModelSelection()
	if !ok || last != remembered {
		t.Fatalf("建会话不应改写“上次模型选择”，got %+v ok=%v", last, ok)
	}
}

func TestProvisionalThreadWatchEmptyUpdatesPreserveHero(t *testing.T) {
	fake, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)
	state.onWatchUpdate(ThreadUpdate{ThreadID: id, Thread: Thread{ID: id, ProjectID: "p1", IsProvisional: true}})
	if err := state.retainProvisionalThread(id); err != nil {
		t.Fatal(err)
	}
	state.onWatchUpdate(ThreadUpdate{ThreadID: id, Thread: Thread{ID: id, ProjectID: "p1"}})
	if len(fake.HistoryCalls()) != 0 || state.composerDockDecisionFor(true, id).Docked {
		t.Fatal("空快照和转正摘要不应拉取 History 或停靠")
	}
	state.selectThread("t1")
}

func TestProvisionalThreadActivityAndProjectSelection(t *testing.T) {
	_, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)
	projects := []Project{{ID: "p1"}, {ID: "p2"}}
	threads := map[string][]Thread{
		"p1": {{ID: id, IsProvisional: true, UpdatedAt: time.Now()}},
		"p2": {{ID: "real", UpdatedAt: time.Now().Add(-time.Hour)}},
	}
	if got := SortProjectsByActivity(projects, threads); got[0].ID != "p2" {
		t.Fatalf("临时会话不应影响项目活跃排序，got %+v", got)
	}
	state.mu.Lock()
	state.threadsByProj["p1"] = append([]Thread{{ID: "other-provisional", ProjectID: "p1", IsProvisional: true}}, state.threadsByProj["p1"]...)
	state.mu.Unlock()
	switchEmptyThreadProject(state, "p1")
	state.mu.Lock()
	selected := state.selectedThreadID
	state.mu.Unlock()
	if selected != "t1" {
		t.Fatalf("切换项目不应选中其它临时会话，got %q", selected)
	}
}

func TestProvisionalThreadSummaryPromotion(t *testing.T) {
	_, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)
	state.applyThreadSummary(Thread{ID: id, ProjectID: "p1", IsProvisional: false})
	state.mu.Lock()
	defer state.mu.Unlock()
	th, _ := state.threadSnapshotLocked(id)
	if th.IsProvisional || len(withoutProvisional(state.threadsByProj["p1"])) != 2 {
		t.Fatal("全局摘要应同步转正字段并让会话可见")
	}
}

type provisionalListFailureClient struct {
	*FakeClient
}

func (f provisionalListFailureClient) Threads(context.Context, string) ([]Thread, error) {
	return nil, errors.New("list failed")
}

func TestProvisionalThreadListFailureReleasesCreatedThread(t *testing.T) {
	fake, state := newPendingTestState(t)
	state.client = provisionalListFailureClient{FakeClient: fake}
	state.createThread("p1")
	waitUntil(t, "列表失败后释放新建会话", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.releaseProvisionalCalls) == 1
	})
	waitUntil(t, "列表失败完成收尾", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return !state.creatingThread
	})
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.selectedThreadID != "t1" || !strings.Contains(state.toastMessage, "list failed") {
		t.Fatalf("列表失败应保留原页面并提示，selected=%q toast=%q", state.selectedThreadID, state.toastMessage)
	}
}

type blockedRetainClient struct {
	*FakeClient
	started chan struct{}
	gate    chan struct{}
}

func (f blockedRetainClient) RetainThread(threadID string) error {
	close(f.started)
	<-f.gate
	return f.FakeClient.RetainThread(threadID)
}

func TestProvisionalThreadConcurrentRetainAndLeave(t *testing.T) {
	fake, state := newPendingTestState(t)
	blocked := blockedRetainClient{FakeClient: fake, started: make(chan struct{}), gate: make(chan struct{})}
	state.client = blocked
	id := createTestProvisionalThread(t, state, true)
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- state.retainProvisionalThread(id) }()
	<-blocked.started
	go func() { second <- state.retainProvisionalThread(id) }()
	state.selectThread("t1")
	close(blocked.gate)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.retainThreadCalls) != 1 || len(fake.releaseProvisionalCalls) != 0 || len(fake.moveToWorktreeCalls) != 1 {
		t.Fatalf("在途转正应合并并保留，retain=%v release=%v worktree=%v", fake.retainThreadCalls, fake.releaseProvisionalCalls, fake.moveToWorktreeCalls)
	}
}

func containsThreadID(threads []Thread, id string) bool {
	for _, th := range threads {
		if th.ID == id {
			return true
		}
	}
	return false
}

func TestProvisionalThreadClearedDraftDemotes(t *testing.T) {
	fake, state := newPendingTestState(t)
	id := createTestProvisionalThread(t, state, false)

	// 1. 输入草稿转正，侧栏包含该会话
	state.mu.Lock()
	state.pendingDraftFill = nil
	state.draft = "你好"
	state.mu.Unlock()
	state.noteComposerDraft(id, "你好")
	waitUntil(t, "输入草稿后转正", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		th, _ := state.threadSnapshotLocked(id)
		return !th.IsProvisional && containsThreadID(withoutProvisional(state.threads), id)
	})
	state.mu.Lock()
	selectedAfterRetain := state.selectedThreadID
	state.mu.Unlock()
	if selectedAfterRetain != id {
		t.Fatalf("转正后应保持选中 %q，got %q", id, selectedAfterRetain)
	}

	// 2. 清空草稿退回临时态，从侧栏消失但仍保持选中
	state.mu.Lock()
	state.draft = ""
	state.mu.Unlock()
	state.noteComposerDraft(id, "")
	waitUntil(t, "清空草稿后退回临时态", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		th, ok := state.threadSnapshotLocked(id)
		return ok && th.IsProvisional && !containsThreadID(withoutProvisional(state.threads), id)
	})
	state.mu.Lock()
	selectedAfterDemote := state.selectedThreadID
	state.mu.Unlock()
	if selectedAfterDemote != id {
		t.Fatalf("退回临时态后应保持选中 %q，got %q", id, selectedAfterDemote)
	}
	fake.mu.Lock()
	demoteCalls := append([]string(nil), fake.demoteEmptyThreadCalls...)
	fake.mu.Unlock()
	if len(demoteCalls) != 1 || demoteCalls[0] != id {
		t.Fatalf("清空草稿应调用一次 DemoteEmptyThread(%q)，got %v", id, demoteCalls)
	}

	// 3. 再次输入草稿重新转正并回到侧栏
	state.mu.Lock()
	state.draft = "再来"
	state.mu.Unlock()
	state.noteComposerDraft(id, "再来")
	waitUntil(t, "再次输入后重新转正", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		th, _ := state.threadSnapshotLocked(id)
		return !th.IsProvisional && containsThreadID(withoutProvisional(state.threads), id)
	})

	// 4. 选中线程有消息时清空草稿不调用 DemoteEmptyThread
	state.selectThread("t1")
	state.mu.Lock()
	state.messages = []Message{{ID: "m1", Text: "已有消息"}}
	state.draft = "草稿"
	state.mu.Unlock()
	state.noteComposerDraft("t1", "草稿")
	state.mu.Lock()
	state.draft = ""
	state.mu.Unlock()
	state.noteComposerDraft("t1", "")
	fake.mu.Lock()
	demoteCallsAfterMsg := append([]string(nil), fake.demoteEmptyThreadCalls...)
	fake.mu.Unlock()
	if len(demoteCallsAfterMsg) != 1 {
		t.Fatalf("有消息的选中会话清空草稿不应调用 DemoteEmptyThread，got %v", demoteCallsAfterMsg)
	}
}

type blockedDemoteClient struct {
	*FakeClient
	started chan struct{}
	gate    chan struct{}
}

func (f blockedDemoteClient) DemoteEmptyThread(threadID string) (bool, error) {
	close(f.started)
	<-f.gate
	return f.FakeClient.DemoteEmptyThread(threadID)
}

func TestProvisionalThreadConcurrentRetainAndDemote(t *testing.T) {
	t.Run("retain_in_flight_then_clear_draft", func(t *testing.T) {
		fake, state := newPendingTestState(t)
		blocked := blockedRetainClient{FakeClient: fake, started: make(chan struct{}), gate: make(chan struct{})}
		state.client = blocked
		id := createTestProvisionalThread(t, state, false)

		state.mu.Lock()
		state.pendingDraftFill = nil
		state.draft = "你好"
		state.mu.Unlock()
		state.noteComposerDraft(id, "你好")
		<-blocked.started

		// 转正尚未返回前立刻清空草稿
		state.mu.Lock()
		state.draft = ""
		state.mu.Unlock()
		state.noteComposerDraft(id, "")
		close(blocked.gate)

		waitUntil(t, "转正完成后顺次降级回临时态", func() bool {
			state.mu.Lock()
			th, ok := state.threadSnapshotLocked(id)
			visible := containsThreadID(withoutProvisional(state.threads), id)
			state.mu.Unlock()
			fake.mu.Lock()
			demoted := len(fake.demoteEmptyThreadCalls) == 1
			fake.mu.Unlock()
			return ok && th.IsProvisional && !visible && demoted
		})
	})

	t.Run("demote_in_flight_then_retype", func(t *testing.T) {
		fake, state := newPendingTestState(t)
		id := createTestProvisionalThread(t, state, false)
		state.mu.Lock()
		state.pendingDraftFill = nil
		state.draft = "你好"
		state.mu.Unlock()
		state.noteComposerDraft(id, "你好")
		waitUntil(t, "首次转正", func() bool {
			state.mu.Lock()
			defer state.mu.Unlock()
			th, _ := state.threadSnapshotLocked(id)
			return !th.IsProvisional
		})

		blocked := blockedDemoteClient{FakeClient: fake, started: make(chan struct{}), gate: make(chan struct{})}
		state.client = blocked
		state.mu.Lock()
		state.draft = ""
		state.mu.Unlock()
		state.noteComposerDraft(id, "")
		<-blocked.started

		// 降级 RPC 返回前用户又重新输入
		state.mu.Lock()
		state.draft = "又输入"
		state.mu.Unlock()
		state.noteComposerDraft(id, "又输入")
		close(blocked.gate)

		waitUntil(t, "降级期间重新输入后自动转正回侧栏", func() bool {
			state.mu.Lock()
			th, ok := state.threadSnapshotLocked(id)
			visible := containsThreadID(withoutProvisional(state.threads), id)
			state.mu.Unlock()
			fake.mu.Lock()
			retains := len(fake.retainThreadCalls)
			fake.mu.Unlock()
			return ok && !th.IsProvisional && visible && retains == 2
		})
	})
}
