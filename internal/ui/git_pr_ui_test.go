package mcodeui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"mcode/internal/git"
	"mcode/internal/testenv"
)

func TestGitPRActionsVisible(t *testing.T) {
	for _, tc := range []struct {
		state       string
		draft, want bool
	}{{"open", false, true}, {"open", true, true}, {"draft", true, true}, {"merged", false, false}, {"closed", false, false}} {
		if got := gitPRActionsVisible(git.PRDetail{PRSummary: git.PRSummary{State: tc.state, IsDraft: tc.draft}}); got != tc.want {
			t.Errorf("状态 %q 草稿 %v: 得到 %v", tc.state, tc.draft, got)
		}
	}
}

func TestGitPRMergeBlocked(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  bool
	}{{"CONFLICTING", true}, {"MERGEABLE", false}, {"UNKNOWN", false}, {"", false}} {
		if got := gitPRMergeBlocked(git.PRDetail{Mergeable: tc.state}); got != tc.want {
			t.Errorf("合并状态 %q: 得到 %v", tc.state, got)
		}
	}
}

type gitPRActionCall struct {
	threadID    string
	number      int
	kind, value string
}
type gitPRActionFake struct {
	*FakeClient
	calls chan gitPRActionCall
	err   error
}

func (f *gitPRActionFake) GitPRList(context.Context, string, int) ([]git.PRSummary, error) {
	return []git.PRSummary{{Number: 42, State: "open"}}, nil
}
func (f *gitPRActionFake) GitPRDetail(context.Context, string, int) (git.PRDetail, error) {
	return git.PRDetail{PRSummary: git.PRSummary{Number: 42, State: "open"}}, nil
}
func (f *gitPRActionFake) GitPRReview(_ context.Context, threadID string, number int, event, body string) error {
	f.calls <- gitPRActionCall{threadID, number, event, body}
	return f.err
}
func (f *gitPRActionFake) GitPRMerge(_ context.Context, threadID string, number int, method string) error {
	f.calls <- gitPRActionCall{threadID, number, "merge", method}
	return f.err
}

func TestGitPRRunAction(t *testing.T) {
	for _, tc := range []struct {
		kind, method, body, value string
		fail                      bool
	}{
		{"approve", "", "批准评论", "批准评论", false},
		{"request-changes", "", "需要调整", "需要调整", false},
		{"merge", "rebase", "草稿", "rebase", false},
		{"approve", "", "草稿", "草稿", true},
	} {
		t.Run(tc.kind+tc.method+tc.value, func(t *testing.T) {
			f := &gitPRActionFake{FakeClient: NewFakeClient(), calls: make(chan gitPRActionCall, 1)}
			if tc.fail {
				f.err = errors.New("操作失败")
			}
			s := newChatState(f)
			s.selectedThreadID = "thread-1"
			st := getGitPRDialogState(s)
			st.mu.Lock()
			st.open, st.threadID, st.selected, st.reviewBody = true, "thread-1", 42, tc.body
			st.detail = &git.PRDetail{PRSummary: git.PRSummary{Number: 42, State: "open"}}
			st.mu.Unlock()
			s.runGitPRAction(tc.kind, tc.method)
			select {
			case got := <-f.calls:
				if want := (gitPRActionCall{"thread-1", 42, tc.kind, tc.value}); got != want {
					t.Errorf("调用 = %+v，期望 %+v", got, want)
				}
			case <-time.After(time.Second):
				t.Fatal("操作未执行")
			}
			deadline := time.Now().Add(time.Second)
			for {
				st.mu.Lock()
				busy, body, errText := st.actionBusy, st.reviewBody, st.actionErr
				st.mu.Unlock()
				if busy == "" {
					if tc.fail && errText == "" {
						t.Error("缺少错误信息")
					}
					if !tc.fail && (body != "" || errText != "") {
						t.Errorf("草稿 %q，错误 %q", body, errText)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("操作未完成")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

// TestGitPRStateLabel 覆盖状态与草稿标签的文案。
func TestGitPRStateLabel(t *testing.T) {
	cases := []struct {
		name  string
		state string
		draft bool
		want  string
	}{
		{"开放", "open", false, "开放"},
		{"大写开放", " OPEN ", false, "开放"},
		{"已关闭", "closed", false, "已关闭"},
		{"已合并", "merged", false, "已合并"},
		{"已锁定", "locked", false, "已锁定"},
		{"草稿优先于开放", "open", true, "草稿"},
		{"空状态", "", false, "未知状态"},
		{"未知状态原样显示", "archived", false, "archived"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitPRStateLabel(tc.state, tc.draft); got != tc.want {
				t.Errorf("gitPRStateLabel(%q, %v) = %q, want %q", tc.state, tc.draft, got, tc.want)
			}
		})
	}
}

// TestGitPRChecksLabel 覆盖 CI 汇总状态的文案。
func TestGitPRChecksLabel(t *testing.T) {
	cases := map[string]string{
		"success":  "检查通过",
		" FAILURE": "检查失败",
		"pending":  "检查进行中",
		"none":     "无检查",
		"":         "检查未知",
		"weird":    "检查未知",
	}
	for in, want := range cases {
		if got := gitPRChecksLabel(in); got != want {
			t.Errorf("gitPRChecksLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGitPRReviewLabel 覆盖评审结论的文案。
func TestGitPRReviewLabel(t *testing.T) {
	cases := map[string]string{
		"APPROVED":          "评审通过",
		"changes_requested": "评审要求修改",
		"REVIEW_REQUIRED":   "等待评审",
		"":                  "暂无评审",
		"other":             "评审未定",
	}
	for in, want := range cases {
		if got := gitPRReviewLabel(in); got != want {
			t.Errorf("gitPRReviewLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGitPRChangeStats 覆盖改动统计的拼装。
func TestGitPRChangeStats(t *testing.T) {
	if got := gitPRChangeStats(120, 8, 5); got != "+120 · -8 · 5 个文件" {
		t.Errorf("gitPRChangeStats = %q", got)
	}
	// 文件数为 0（例如 glab 不提供）时只显示增删行数。
	if got := gitPRChangeStats(0, 0, 0); got != "+0 · -0" {
		t.Errorf("gitPRChangeStats(0,0,0) = %q", got)
	}
}

// TestGitPRMetaLine 覆盖元信息行的拼装与空字段跳过。
func TestGitPRMetaLine(t *testing.T) {
	full := git.PRSummary{
		Author:     "octocat",
		HeadBranch: "feat/pr-view",
		BaseBranch: "main",
		UpdatedAt:  time.Now().Add(-2 * time.Hour),
	}
	if got := gitPRMetaLine(full); got != "作者 octocat · feat/pr-view → main · 更新于 2小时前" {
		t.Errorf("gitPRMetaLine = %q", got)
	}
	// 只有目标分支时补一个箭头，时间零值不显示。
	if got := gitPRMetaLine(git.PRSummary{BaseBranch: "main"}); got != "→ main" {
		t.Errorf("gitPRMetaLine(BaseBranch) = %q", got)
	}
	if got := gitPRMetaLine(git.PRSummary{}); got != "" {
		t.Errorf("gitPRMetaLine(空) = %q, 期望空字符串", got)
	}
}

// TestGitPRUpdatedLabel 覆盖零值时间不显示。
func TestGitPRUpdatedLabel(t *testing.T) {
	if got := gitPRUpdatedLabel(time.Time{}); got != "" {
		t.Errorf("零值时间 = %q, 期望空字符串", got)
	}
	if got := gitPRUpdatedLabel(time.Now().Add(-30 * time.Second)); got != "刚刚" {
		t.Errorf("30 秒前 = %q, 期望 刚刚", got)
	}
}

// gitPRDialogFake 提供可控的 PR 列表、详情与冲突解决调用记录。
type gitPRDialogFake struct {
	*FakeClient
	prs     []git.PRSummary
	listErr error
	mu      sync.Mutex
	details []int

	detail      git.PRDetail
	conflicts   []string
	conflictErr error

	conflictCalls []gitPRConflictCall
	resolvePath   string
	resolveErr    error
	resolveCalls  []gitPRResolveCall
	switchCalls   []gitPRSwitchCall
	cleanupCalls  []gitPRActionCall
	cleanupReason string
	cleanupErr    error
	cleanupWait   <-chan struct{}
	mergeErr      error
	mergeStarted  chan struct{}
	mergeWait     <-chan struct{}
}

type gitPRConflictCall struct {
	number int
	base   string
}

type gitPRResolveCall struct {
	number     int
	head, base string
}

type gitPRSwitchCall struct {
	threadID, path string
}

func (f *gitPRDialogFake) GitPRList(context.Context, string, int) ([]git.PRSummary, error) {
	return f.prs, f.listErr
}

func (f *gitPRDialogFake) GitPRDetail(_ context.Context, _ string, number int) (git.PRDetail, error) {
	f.mu.Lock()
	f.details = append(f.details, number)
	detail := f.detail
	f.mu.Unlock()
	if detail.Number == 0 {
		detail = git.PRDetail{PRSummary: git.PRSummary{Number: number, Title: "详情标题", State: "open"}}
	}
	return detail, nil
}

func (f *gitPRDialogFake) GitPRConflicts(_ context.Context, _ string, number int, base string) ([]string, error) {
	f.mu.Lock()
	f.conflictCalls = append(f.conflictCalls, gitPRConflictCall{number: number, base: base})
	files, err := append([]string(nil), f.conflicts...), f.conflictErr
	f.mu.Unlock()
	return files, err
}

func (f *gitPRDialogFake) GitPRResolveWorktree(_ context.Context, _ string, number int, head, base string) (string, error) {
	f.mu.Lock()
	f.resolveCalls = append(f.resolveCalls, gitPRResolveCall{number: number, head: head, base: base})
	path, err := f.resolvePath, f.resolveErr
	f.mu.Unlock()
	return path, err
}

func (f *gitPRDialogFake) GitSwitchWorktree(ctx context.Context, threadID, path string) error {
	f.mu.Lock()
	f.switchCalls = append(f.switchCalls, gitPRSwitchCall{threadID: threadID, path: path})
	f.mu.Unlock()
	return f.FakeClient.GitSwitchWorktree(ctx, threadID, path)
}

func (f *gitPRDialogFake) GitPRCleanupWorktree(_ context.Context, threadID string, number int) (string, error) {
	f.mu.Lock()
	f.cleanupCalls = append(f.cleanupCalls, gitPRActionCall{threadID: threadID, number: number})
	reason, err, wait := f.cleanupReason, f.cleanupErr, f.cleanupWait
	f.mu.Unlock()
	if wait != nil {
		<-wait
	}
	return reason, err
}

func (f *gitPRDialogFake) GitPRMerge(context.Context, string, int, string) error {
	if f.mergeStarted != nil {
		close(f.mergeStarted)
		<-f.mergeWait
	}
	return f.mergeErr
}

func (f *gitPRDialogFake) GitPRReview(context.Context, string, int, string, string) error { return nil }

func (f *gitPRDialogFake) detailCalls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.details...)
}

func (f *gitPRDialogFake) conflictCallsSnapshot() []gitPRConflictCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gitPRConflictCall(nil), f.conflictCalls...)
}

func (f *gitPRDialogFake) resolveCallsSnapshot() []gitPRResolveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gitPRResolveCall(nil), f.resolveCalls...)
}

func (f *gitPRDialogFake) switchCallsSnapshot() []gitPRSwitchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]gitPRSwitchCall(nil), f.switchCalls...)
}

func (f *gitPRDialogFake) renameCallsSnapshot() []renameCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]renameCall(nil), f.renameCalls...)
}

func newGitPRDialogTestState(t *testing.T, client *gitPRDialogFake) *chatState {
	t.Helper()
	testenv.TempHome(t)
	s := newChatState(client)
	s.mu.Lock()
	s.selectedThreadID = "thread-1"
	s.threads = []Thread{{ID: "thread-1", ProjectID: "p1"}}
	s.mu.Unlock()
	t.Cleanup(func() {
		s.close()
		modalDragReset(modalDragGitPR)
	})
	return s
}

func waitGitPRDialog(t *testing.T, s *chatState, check func(*gitPRDialogState) bool) {
	t.Helper()
	st := getGitPRDialogState(s)
	deadline := time.Now().Add(3 * time.Second)
	for {
		st.mu.Lock()
		ok := check(st)
		st.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("等待 PR 弹窗状态超时")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGitPRMergeCleansWorktree(t *testing.T) {
	for _, tc := range []struct {
		name, reason, toast string
		err, mergeErr       error
		closeDialog         bool
	}{
		{name: "清理成功"},
		{name: "保留工作区", reason: "有未推送的提交", toast: "已保留 PR #42 的工作区：有未推送的提交"},
		{name: "清理失败", err: errors.New("清理失败原文"), toast: "清理失败原文"},
		{name: "合并失败", mergeErr: errors.New("合并失败原文")},
		{name: "合并期间关闭弹窗", closeDialog: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(done) }) }
			t.Cleanup(release)
			f := &gitPRDialogFake{FakeClient: NewFakeClient(), cleanupReason: tc.reason, cleanupErr: tc.err, cleanupWait: done, mergeErr: tc.mergeErr}
			mergeGate := make(chan struct{})
			if tc.closeDialog {
				f.mergeStarted, f.mergeWait = make(chan struct{}), mergeGate
			}
			s := newGitPRDialogTestState(t, f)
			st := getGitPRDialogState(s)
			st.mu.Lock()
			st.open, st.threadID, st.selected = true, "thread-1", 42
			st.detail = &git.PRDetail{PRSummary: git.PRSummary{Number: 42, State: "open"}}
			st.mu.Unlock()
			s.runGitPRAction("merge", "squash")
			if tc.closeDialog {
				select {
				case <-f.mergeStarted:
				case <-time.After(3 * time.Second):
					close(mergeGate)
					t.Fatal("合并未开始")
				}
				st.mu.Lock()
				st.open = false
				st.detailSeq++
				st.mu.Unlock()
				close(mergeGate)
			} else {
				waitGitPRDialog(t, s, func(st *gitPRDialogState) bool { return st.actionBusy == "" && !st.detailLoading && !st.listLoading })
			}
			if tc.mergeErr == nil && !tc.closeDialog {
				// 清理仍阻塞时，合并成功后的详情刷新必须已执行。
				if len(f.detailCalls()) != 1 {
					t.Error("清理阻塞了合并后的详情刷新")
				}
			}
			release()
			deadline := time.Now().Add(3 * time.Second)
			for {
				f.mu.Lock()
				calls := append([]gitPRActionCall(nil), f.cleanupCalls...)
				f.mu.Unlock()
				s.mu.Lock()
				toast := s.toastMessage
				s.mu.Unlock()
				if tc.mergeErr != nil {
					if len(calls) != 0 {
						t.Fatal("合并失败后不应清理")
					}
					break
				}
				if len(calls) == 1 && toast == tc.toast {
					if calls[0].number != 42 || calls[0].threadID != "thread-1" {
						t.Fatalf("清理调用 = %+v", calls)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("清理调用 = %+v，toast = %q", calls, toast)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

// TestGitPRDialogEmptyStateOffersCreate 覆盖没有开放 PR 时的空状态。
func TestGitPRDialogEmptyStateOffersCreate(t *testing.T) {
	s := newGitPRDialogTestState(t, &gitPRDialogFake{FakeClient: NewFakeClient()})
	s.openGitPRDialog()
	waitGitPRDialog(t, s, func(st *gitPRDialogState) bool { return !st.listLoading && st.listErr == "" })

	tt := ui.NewTester(func(c *ui.Context) { renderGitPRDialog(c, s) }, 1000, 800)
	tt.Advance(DurSheet)
	for _, text := range []string{"开放的 PR/MR", "从左侧选择一条 PR 查看详情", "Esc 关闭"} {
		if tt.HasText(text) {
			t.Errorf("空状态不应显示 %q", text)
		}
	}
	for _, text := range []string{"没有开放的 PR", "创建 PR…"} {
		if !tt.HasText(text) {
			t.Errorf("空状态缺少 %q", text)
		}
	}
}

// TestGitPRDialogAutoSelectsFirst 覆盖列表加载完成后自动选中第一条并请求详情。
func TestGitPRDialogAutoSelectsFirst(t *testing.T) {
	f := &gitPRDialogFake{FakeClient: NewFakeClient(), prs: []git.PRSummary{
		{Number: 7, Title: "第一条", State: "open"},
		{Number: 9, Title: "第二条", State: "open"},
	}}
	s := newGitPRDialogTestState(t, f)
	s.openGitPRDialog()
	waitGitPRDialog(t, s, func(st *gitPRDialogState) bool {
		return st.selected == 7 && st.detail != nil && !st.detailLoading
	})
	if calls := f.detailCalls(); len(calls) != 1 || calls[0] != 7 {
		t.Fatalf("详情请求 = %v，期望只请求第一条 #7", calls)
	}

	tt := ui.NewTester(func(c *ui.Context) { renderGitPRDialog(c, s) }, 1000, 800)
	tt.Advance(DurSheet)
	if !tt.HasText("第一条") {
		t.Error("左列没有显示第一条 PR")
	}
}

// TestGitPRDialogConflictList 覆盖冲突时读取并展示冲突文件路径。
func TestGitPRDialogConflictList(t *testing.T) {
	f := &gitPRDialogFake{
		FakeClient: NewFakeClient(),
		prs:        []git.PRSummary{{Number: 42, Title: "冲突", State: "open"}},
		detail: git.PRDetail{
			PRSummary: git.PRSummary{Number: 42, Title: "冲突", State: "open", HeadBranch: "feat/x", BaseBranch: "main"},
			Mergeable: "CONFLICTING",
		},
		conflicts: []string{"src/a.go", "src/b.go"},
	}
	s := newGitPRDialogTestState(t, f)
	s.openGitPRDialog()
	waitGitPRDialog(t, s, func(st *gitPRDialogState) bool {
		return st.selected == 42 && st.detail != nil && !st.detailLoading && !st.conflictLoading && st.conflictNumber == 42
	})
	calls := f.conflictCallsSnapshot()
	if len(calls) != 1 || calls[0].number != 42 || calls[0].base != "main" {
		t.Fatalf("冲突预演调用 = %+v，期望 #42 base main", calls)
	}

	tt := ui.NewTester(func(c *ui.Context) { renderGitPRDialog(c, s) }, 1000, 800)
	tt.Advance(DurSheet)
	for _, text := range []string{"2 个文件冲突", "src/a.go", "src/b.go"} {
		if !tt.HasText(text) {
			t.Errorf("详情缺少 %q", text)
		}
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Error("冲突列表加载完成后仍在请求下一帧")
	}
}

// TestGitPRDialogConflictResolve 覆盖「解决冲突」的完整调用顺序。
func TestGitPRDialogConflictResolve(t *testing.T) {
	f := &gitPRDialogFake{
		FakeClient: NewFakeClient(),
		prs:        []git.PRSummary{{Number: 42, Title: "冲突", State: "open"}},
		detail: git.PRDetail{
			PRSummary: git.PRSummary{Number: 42, Title: "冲突", State: "open", HeadBranch: "feat/x", BaseBranch: "main"},
			Mergeable: "CONFLICTING",
		},
		conflicts:   []string{"src/a.go"},
		resolvePath: "/tmp/mcode-pr-42",
	}
	s := newGitPRDialogTestState(t, f)
	s.openGitPRDialog()
	waitGitPRDialog(t, s, func(st *gitPRDialogState) bool {
		return st.selected == 42 && st.detail != nil && !st.detailLoading && !st.conflictLoading
	})

	tt := ui.NewTester(func(c *ui.Context) { renderGitPRDialog(c, s) }, 1000, 800)
	tt.Advance(DurSheet)
	if err := tt.Click("解决冲突"); err != nil {
		t.Fatalf("点击解决冲突: %v", err)
	}
	waitGitPRDialog(t, s, func(st *gitPRDialogState) bool { return !st.open })

	if calls := f.resolveCallsSnapshot(); len(calls) != 1 || calls[0] != (gitPRResolveCall{number: 42, head: "feat/x", base: "main"}) {
		t.Fatalf("冲突解决调用 = %+v", calls)
	}
	if got := f.LastCreatedProjectID(); got != "p1" {
		t.Errorf("新建会话项目 = %q，期望 p1", got)
	}
	f.mu.Lock()
	opts := f.lastNewThreadOpts
	f.mu.Unlock()
	if opts.Provisional {
		t.Error("冲突解决会话不应是临时会话")
	}
	renames := f.renameCallsSnapshot()
	if len(renames) != 1 || renames[0].Title != "解决 PR #42 冲突" {
		t.Fatalf("重命名调用 = %+v", renames)
	}
	newID := renames[0].ThreadID
	switches := f.switchCallsSnapshot()
	if len(switches) != 1 || switches[0] != (gitPRSwitchCall{threadID: newID, path: "/tmp/mcode-pr-42"}) {
		t.Fatalf("切换 worktree 调用 = %+v，期望会话 %q", switches, newID)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		selected := s.selectedThreadID
		s.mu.Unlock()
		if selected == newID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("选中会话 = %q，期望 %q", selected, newID)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestGitPRDialogCrossRepoBlocksResolve 覆盖 fork 来的 PR 禁止解决冲突。
func TestGitPRDialogCrossRepoBlocksResolve(t *testing.T) {
	f := &gitPRDialogFake{
		FakeClient: NewFakeClient(),
		prs:        []git.PRSummary{{Number: 42, Title: "冲突", State: "open"}},
		detail: git.PRDetail{
			PRSummary: git.PRSummary{Number: 42, Title: "冲突", State: "open", HeadBranch: "feat/x", BaseBranch: "main"},
			Mergeable: "CONFLICTING",
			CrossRepo: true,
		},
		resolvePath: "/tmp/mcode-pr-42",
	}
	s := newGitPRDialogTestState(t, f)
	s.openGitPRDialog()
	waitGitPRDialog(t, s, func(st *gitPRDialogState) bool {
		return st.selected == 42 && st.detail != nil && !st.detailLoading
	})

	tt := ui.NewTester(func(c *ui.Context) { renderGitPRDialog(c, s) }, 1000, 800)
	tt.Advance(DurSheet)
	if !tt.HasText("解决冲突") {
		t.Fatal("缺少解决冲突按钮")
	}
	_ = tt.Click("解决冲突")
	time.Sleep(50 * time.Millisecond)
	if calls := f.resolveCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("fork PR 不应创建冲突 worktree，调用 = %+v", calls)
	}
	st := getGitPRDialogState(s)
	st.mu.Lock()
	open, busy := st.open, st.actionBusy
	st.mu.Unlock()
	if !open || busy != "" {
		t.Errorf("弹窗状态 open=%v busy=%q", open, busy)
	}
}
