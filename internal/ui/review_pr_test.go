package mcodeui

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"mcode/internal/git"
	"mcode/internal/testenv"
)

type reviewPRCall struct {
	threadID          string
	number            int
	path, event, body string
	comments          []git.PRReviewComment
}

type reviewPRFake struct {
	*FakeClient
	localCalls atomic.Int32
	get        func(context.Context, string, int, string) (ReviewSnapshot, error)
	submit     func(context.Context, reviewPRCall) error
	ai         func(context.Context, string, int) ([]git.PRReviewComment, error)
}

func (f *reviewPRFake) ReviewGet(ctx context.Context, tid, scope, turn, path string) (ReviewSnapshot, error) {
	f.localCalls.Add(1)
	return f.FakeClient.ReviewGet(ctx, tid, scope, turn, path)
}

func (f *reviewPRFake) ReviewGetPR(ctx context.Context, tid string, number int, path string) (ReviewSnapshot, error) {
	if f.get != nil {
		return f.get(ctx, tid, number, path)
	}
	list, full := reviewFixture()
	if path != "" {
		list = full
	}
	list.Scope, list.Number, list.TurnID = "pr", number, ""
	return list, nil
}

func (f *reviewPRFake) GitPRSubmitReview(ctx context.Context, tid string, number int, event, body string, comments []git.PRReviewComment) error {
	return f.submit(ctx, reviewPRCall{threadID: tid, number: number, event: event, body: body, comments: slices.Clone(comments)})
}

func (f *reviewPRFake) GitPRAIReview(ctx context.Context, tid string, number int) ([]git.PRReviewComment, error) {
	if f.ai != nil {
		return f.ai(ctx, tid, number)
	}
	return nil, nil
}

func reviewPRSetup(t *testing.T) (*chatState, *reviewPRFake, *diffPanelState) {
	t.Helper()
	testenv.TempHome(t)
	f := &reviewPRFake{FakeClient: NewFakeClient()}
	s := newChatState(f)
	s.selectedThreadID = "t1"
	s.projects = []Project{{ID: "p1", Path: "/workspace"}}
	s.threads = []Thread{{ID: "t1", ProjectID: "p1"}}
	dp := getDiffPanelState(s)
	dp.scope, dp.prNumber = "pr", 7
	t.Cleanup(s.close)
	return s, f, dp
}

func TestReviewScopeAcceptsPR(t *testing.T) {
	if reviewScope("pr") != "pr" || reviewScope("unknown") != "turn" || reviewScopeTitle("pr") != "PR" {
		t.Fatal("PR 作用域或未知值回退错误")
	}
}

func TestReviewCommentsToPR(t *testing.T) {
	comments := []reviewComment{
		{key: reviewDiffKey{path: "new.go"}, lines: editor.DiffLineRange{Side: editor.DiffNew, Start: 0, End: 0}, body: "新增行"},
		{key: reviewDiffKey{path: "old.go"}, lines: editor.DiffLineRange{Side: editor.DiffOld, Start: 125, End: 125}, body: "删除行"},
		{key: reviewDiffKey{path: "multi.go"}, lines: editor.DiffLineRange{Side: editor.DiffNew, Start: 1233, End: 1237}, body: "多行"},
		{key: reviewDiffKey{path: "old-multi.go"}, lines: editor.DiffLineRange{Side: editor.DiffOld, Start: 10, End: 12}, body: "旧侧多行"},
	}
	want := []git.PRReviewComment{
		{Path: "new.go", Line: 1, Side: "RIGHT", Body: "新增行"},
		{Path: "old.go", Line: 126, Side: "LEFT", Body: "删除行"},
		{Path: "multi.go", Line: 1238, Side: "RIGHT", Body: "多行"},
		{Path: "old-multi.go", Line: 13, Side: "LEFT", Body: "旧侧多行"},
	}
	if got := reviewCommentsToPR(comments); !reflect.DeepEqual(got, want) {
		t.Fatalf("映射 = %+v，期望 %+v", got, want)
	}
}

func TestReviewLoadPRScope(t *testing.T) {
	s, f, dp := reviewPRSetup(t)
	calls := make(chan reviewPRCall, 4)
	f.get = func(_ context.Context, tid string, number int, path string) (ReviewSnapshot, error) {
		calls <- reviewPRCall{threadID: tid, number: number, path: path}
		list, full := reviewFixture()
		if path != "" {
			list = full
		}
		list.Scope, list.Number, list.TurnID = "pr", number, ""
		return list, nil
	}
	s.loadReview(dp, f, "t1", "ignored-turn")
	waitReviewComments(t, func() bool {
		diffPanelMu.Lock()
		defer diffPanelMu.Unlock()
		return !dp.loading
	})
	diffPanelMu.Lock()
	snapshot, turn := dp.snapshot, dp.turnID
	diffPanelMu.Unlock()
	if snapshot.Scope != "pr" || snapshot.Number != 7 || turn != "" {
		t.Fatalf("PR 快照错误: %+v, turn=%q", snapshot, turn)
	}
	key := reviewKey(s, "t1", snapshot, "a.go")
	reviewContentFor(s, dp, key)
	waitReviewComments(t, func() bool { return !reviewContentFor(s, dp, key).loading })
	for _, path := range []string{"", "a.go"} {
		select {
		case call := <-calls:
			if call.threadID != "t1" || call.number != 7 || call.path != path {
				t.Fatalf("PR 请求错误: %+v", call)
			}
		default:
			t.Fatal("缺少 PR 请求")
		}
	}
	if f.localCalls.Load() != 0 {
		t.Fatal("PR 审阅调用了本地 ReviewGet")
	}
}

func TestReviewPRSubmit(t *testing.T) {
	for _, event := range []string{"comment", "approve", "request-changes"} {
		for _, fail := range []bool{false, true} {
			name := event
			if fail {
				name += "-failure"
			}
			t.Run(name, func(t *testing.T) {
				s, f, dp := reviewPRSetup(t)
				list, full := reviewFixture()
				list.Scope, list.Number = "pr", 7
				key := reviewKey(s, "t1", list, "a.go")
				addReviewTestComment(t, dp, key, *full.File, editor.DiffLineRange{Side: editor.DiffNew, Start: 0, End: 1}, "核对")
				other := key
				other.prNumber = 8
				addReviewTestComment(t, dp, other, *full.File, editor.DiffLineRange{Side: editor.DiffOld, Start: 1, End: 1}, "另一条 PR")
				other.scope = "turn"
				addReviewTestComment(t, dp, other, *full.File, editor.DiffLineRange{Side: editor.DiffNew, Start: 1, End: 1}, "本地草稿")
				calls, release := make(chan reviewPRCall, 1), make(chan struct{})
				f.submit = func(ctx context.Context, call reviewPRCall) error {
					calls <- call
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					if fail {
						return errors.New("提交失败")
					}
					return nil
				}
				t.Cleanup(func() {
					select {
					case <-release:
					default:
						close(release)
					}
				})
				if !submitReviewPR(s, dp, "t1", event) || submitReviewPR(s, dp, "t1", event) {
					t.Fatal("提交没有启动或没有阻止重复提交")
				}
				select {
				case got := <-calls:
					want := reviewPRCall{threadID: "t1", number: 7, event: event, comments: []git.PRReviewComment{{Path: "a.go", Line: 2, Side: "RIGHT", Body: "核对"}}}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("调用 = %+v，期望 %+v", got, want)
					}
				case <-time.After(time.Second):
					t.Fatal("没有调用提交接口")
				}
				tt := ui.NewTester(func(c *ui.Context) { renderReviewPRAction(c, s, dp, "t1") }, 800, 100)
				tt.Frame()
				if !tt.HasText("评论") || !tt.HasText("批准") || !tt.HasText("要求修改") {
					t.Fatal("缺少 PR 审阅按钮")
				}
				for _, label := range []string{"评论", "批准", "要求修改"} {
					if err := tt.Click(label); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case <-calls:
					t.Fatal("提交期间按钮触发了重复请求")
				default:
				}
				// 提交期间新增的草稿不能被成功回包清掉。
				addReviewTestComment(t, dp, key, *full.File, editor.DiffLineRange{Side: editor.DiffOld, Start: 1, End: 1}, "稍后提交")
				diffPanelMu.Lock()
				dp.prNumber = 8
				diffPanelMu.Unlock()
				close(release)
				waitReviewComments(t, func() bool {
					diffPanelMu.Lock()
					defer diffPanelMu.Unlock()
					return dp.prReviewEvent == ""
				})
				diffPanelMu.Lock()
				count, errText := len(dp.comments), dp.prReviewErr
				diffPanelMu.Unlock()
				wantCount := 3
				if fail {
					wantCount = 4
				}
				if count != wantCount || (errText != "") != fail {
					t.Fatalf("草稿=%d，错误=%q", count, errText)
				}
				tt.Frame()
				if tt.HasText("提交失败") {
					t.Fatal("其他 PR 显示了旧请求的错误")
				}
				diffPanelMu.Lock()
				dp.prNumber = 7
				diffPanelMu.Unlock()
				tt.Frame()
				if tt.HasText("提交失败") != fail {
					t.Fatal("错误文字显示错误")
				}
			})
		}
	}
}

func TestReviewPRDiscardsOldNumber(t *testing.T) {
	for _, path := range []string{"", "a.go"} {
		t.Run("path="+path, func(t *testing.T) {
			s, f, dp := reviewPRSetup(t)
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			f.get = func(ctx context.Context, _ string, _ int, _ string) (ReviewSnapshot, error) {
				// loadReview 和正文请求退出时都会取消上下文，作为异步处理完成的屏障。
				context.AfterFunc(ctx, func() { close(done) })
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return ReviewSnapshot{}, ctx.Err()
				}
				_, full := reviewFixture()
				full.Scope, full.Number = "pr", 7
				return full, nil
			}
			list, _ := reviewFixture()
			list.Scope, list.Number = "pr", 7
			key := reviewKey(s, "t1", list, path)
			if path == "" {
				s.loadReview(dp, f, "t1", "")
			} else {
				reviewContentFor(s, dp, key)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("请求未启动")
			}
			diffPanelMu.Lock()
			dp.prNumber, dp.snapshot.HeadRevision = 8, "current"
			diffPanelMu.Unlock()
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("请求未完成")
			}
			diffPanelMu.Lock()
			defer diffPanelMu.Unlock()
			if path == "" && dp.snapshot.HeadRevision != "current" {
				t.Fatal("旧 PR 列表覆盖了当前结果")
			}
			if path != "" && dp.contents[key].content != nil {
				t.Fatal("切换 PR 后仍接收了旧正文")
			}
		})
	}
}

func TestReviewPRPreferenceStaysLocal(t *testing.T) {
	s, _, dp := reviewPRSetup(t)
	t.Setenv("MCODE_DATA", t.TempDir())
	t.Cleanup(waitUIStateWrites)
	WithUIStatePersistence()(s)
	dp.scope, dp.selectedPath = "turn", "local.go"
	rememberReviewPreference(s, dp, "t1")
	waitUIStateWrites()
	dp.scope, dp.selectedPath = "pr", "pr.go"
	rememberReviewPreference(s, dp, "t1")
	waitUIStateWrites()
	restored := &diffPanelState{}
	loadReviewPreference(s, restored, "t1")
	if reviewScope(restored.scope) != "turn" || restored.selectedPath != "local.go" {
		t.Fatal("PR 改写了本地审阅偏好")
	}
	pr := &diffPanelState{scope: "pr", prNumber: 7}
	loadReviewPreference(s, pr, "t1")
	if pr.scope != "pr" || pr.selectedPath != "" {
		t.Fatal("本地偏好覆盖了 PR 入口")
	}
}

func TestReviewPRIdentityAndReopen(t *testing.T) {
	s, _, dp := reviewPRSetup(t)
	inspectorSelect(s, sidePaneDiffID, false)
	openReviewPR(s, 7)
	waitReviewComments(t, func() bool {
		diffPanelMu.Lock()
		defer diffPanelMu.Unlock()
		return !dp.loading
	})
	if snap, ok := inspectorSnapshotFor(s); !ok || snap.ReviewScope != "pr" {
		t.Fatal("快照未识别 PR 作用域")
	}
	list, _ := reviewFixture()
	list.Scope, list.Number = "pr", 7
	a := reviewKey(s, "t1", list, "a.go")
	list.Number = 8
	b := reviewKey(s, "t1", list, "a.go")
	if a == b || reviewSameFile(a, b) || reviewReadKey(a, list.Files[0]) == reviewReadKey(b, list.Files[0]) {
		t.Fatal("不同 PR 的缓存或草稿共用了身份")
	}
	s.selectReviewScope(dp, "session")
	diffPanelMu.Lock()
	number := dp.prNumber
	diffPanelMu.Unlock()
	if number != 7 {
		t.Fatal("切回本地作用域丢失 PR 编号")
	}
	s.selectReviewScope(dp, "pr")
	inspectorClose(s)
	inspectorSelect(s, sidePaneDiffID, false)
	diffPanelMu.Lock()
	scope := dp.scope
	diffPanelMu.Unlock()
	if scope != "turn" {
		t.Fatal("重新打开没有回到本地作用域")
	}
}

func TestReviewPRNoHunkRevert(t *testing.T) {
	list, full := reviewFixture()
	list.Scope, list.Number = "pr", 7
	_, tt, view := newReviewHunkTester(t, NewFakeClient(), list, list.Files[0], *full.File)
	hoverReviewHunk(t, tt, view)
	if view.state.HunkActions || view.state.RenderHunkActions != nil || tt.HasText("撤销此块") {
		t.Fatal("PR 审阅提供了撤销入口")
	}
}

func TestReviewPRAIAddsDrafts(t *testing.T) {
	s, f, dp := reviewPRSetup(t)
	tt := ui.NewTester(func(c *ui.Context) { renderReviewPRAction(c, s, dp, "t1") }, 800, 100)
	tt.Frame()
	if !tt.HasText("AI 审查") {
		t.Fatal("没有草稿时缺少 AI 审查按钮")
	}
	if tt.HasText("批准") || tt.HasText("要求修改") {
		t.Fatal("没有草稿时显示了提交审阅按钮")
	}
	calls := atomic.Int32{}
	var first atomic.Bool
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	f.ai = func(ctx context.Context, _ string, _ int) ([]git.PRReviewComment, error) {
		calls.Add(1)
		if first.CompareAndSwap(false, true) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []git.PRReviewComment{
			{Path: "a.go", Line: 2, Side: "RIGHT", Body: " 改用常量 "},
			{Path: "a.go", Line: 99, Side: "RIGHT", Body: "超出文件"},
		}, nil
	}
	if !startReviewPRAI(s, dp, "t1") {
		t.Fatal("AI 审查没有启动")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("AI 审查请求未开始")
	}
	if startReviewPRAI(s, dp, "t1") {
		t.Fatal("AI 审查运行中重复启动了请求")
	}
	close(release)
	waitReviewComments(t, func() bool {
		diffPanelMu.Lock()
		defer diffPanelMu.Unlock()
		return dp.prAIBusy == 0 && len(dp.comments) == 1
	})
	diffPanelMu.Lock()
	comment := *dp.comments[0]
	diffPanelMu.Unlock()
	if comment.key.path != "a.go" || comment.key.scope != "pr" || comment.key.prNumber != 7 || comment.lines != (editor.DiffLineRange{Side: editor.DiffNew, Start: 1, End: 1}) || comment.original != "var a = 2\n" || comment.body != "改用常量" {
		t.Fatalf("草稿 = %+v", comment)
	}
	if calls.Load() != 1 {
		t.Fatalf("AI 审查调用 %d 次", calls.Load())
	}
	if !startReviewPRAI(s, dp, "t1") {
		t.Fatal("再次 AI 审查没有启动")
	}
	waitReviewComments(t, func() bool {
		diffPanelMu.Lock()
		defer diffPanelMu.Unlock()
		return dp.prAIBusy == 0 && calls.Load() == 2
	})
	diffPanelMu.Lock()
	count := len(dp.comments)
	diffPanelMu.Unlock()
	if count != 1 {
		t.Fatalf("重复请求产生了 %d 条草稿", count)
	}
	tt.Frame()
	if !tt.HasText("批准") || !tt.HasText("要求修改") {
		t.Fatal("有草稿时缺少提交审阅按钮")
	}
}

func TestReviewPRAIStalePR(t *testing.T) {
	s, f, dp := reviewPRSetup(t)
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	f.ai = func(ctx context.Context, _ string, _ int) ([]git.PRReviewComment, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []git.PRReviewComment{{Path: "a.go", Line: 2, Side: "RIGHT", Body: "旧 PR"}}, nil
	}
	if !startReviewPRAI(s, dp, "t1") {
		t.Fatal("AI 审查没有启动")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("AI 审查请求未开始")
	}
	diffPanelMu.Lock()
	dp.prNumber = 8
	diffPanelMu.Unlock()
	close(release)
	waitReviewComments(t, func() bool {
		diffPanelMu.Lock()
		defer diffPanelMu.Unlock()
		return dp.prAIBusy == 0
	})
	diffPanelMu.Lock()
	count := len(dp.comments)
	diffPanelMu.Unlock()
	if count != 0 {
		t.Fatalf("旧 PR 结果生成了 %d 条草稿", count)
	}
}
