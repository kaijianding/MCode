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

// gitPRDialogFake 提供可控的 PR 列表与详情，并记录详情请求。
type gitPRDialogFake struct {
	*FakeClient
	prs     []git.PRSummary
	listErr error
	mu      sync.Mutex
	details []int
}

func (f *gitPRDialogFake) GitPRList(context.Context, string, int) ([]git.PRSummary, error) {
	return f.prs, f.listErr
}

func (f *gitPRDialogFake) GitPRDetail(_ context.Context, _ string, number int) (git.PRDetail, error) {
	f.mu.Lock()
	f.details = append(f.details, number)
	f.mu.Unlock()
	return git.PRDetail{PRSummary: git.PRSummary{Number: number, Title: "详情标题", State: "open"}}, nil
}

func (f *gitPRDialogFake) detailCalls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.details...)
}

func newGitPRDialogTestState(t *testing.T, client *gitPRDialogFake) *chatState {
	t.Helper()
	testenv.TempHome(t)
	s := newChatState(client)
	s.mu.Lock()
	s.selectedThreadID = "thread-1"
	s.threads = []Thread{{ID: "thread-1"}}
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
