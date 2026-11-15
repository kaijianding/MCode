package kanban

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mcode/internal/store"
	"mcode/internal/testenv"
)

func TestFinishedTaskAcceptanceCommands(t *testing.T) {
	for _, tc := range []struct {
		name, criteria, status, feedback string
		autoAccept, missingWorktree      bool
		rejectCount, wantRejectCount     int
	}{
		{name: "自动接受", criteria: "[命令] `true`", autoAccept: true, status: "done", feedback: "验收命令全部通过"},
		{name: "首次失败", criteria: "[命令] `false`\n[命令] `printf failure-output; exit 3`", autoAccept: true, status: "todo", wantRejectCount: 1, feedback: "退出码 1"},
		{name: "再次失败", criteria: "[命令] `false`", autoAccept: true, rejectCount: 1, wantRejectCount: 1, status: "review", feedback: "退出码 1"},
		{name: "你验", criteria: "[命令] `true`\n[你验] 点界面", autoAccept: true, status: "review", feedback: "验收命令全部通过"},
		{name: "自动接受关闭", criteria: "[命令] `true`", status: "review", feedback: "验收命令全部通过"},
		{name: "超时", criteria: "[命令] `exec sleep 5`", autoAccept: true, status: "todo", wantRejectCount: 1, feedback: "超时"},
		{name: "执行错误", criteria: "[命令] `true`", autoAccept: true, missingWorktree: true, status: "todo", wantRejectCount: 1, feedback: "does not exist"},
	} {
		for _, watchdogFirst := range []bool{false, true} {
			name := tc.name + "/scheduler"
			if watchdogFirst {
				name = tc.name + "/watchdog"
			}
			t.Run(name, func(t *testing.T) {
				if tc.name == "超时" {
					oldTimeout := acceptanceCommandTimeout
					acceptanceCommandTimeout = 80 * time.Millisecond
					t.Cleanup(func() { acceptanceCommandTimeout = oldTimeout })
				}
				testenv.TempHome(t)
				ctx := context.Background()
				repo := initMergeTestRepo(t)
				wtDir := t.TempDir()
				st := openMergeTestStore(t)
				driver := newMockDriver()
				svc := newMergeTestService(t, st, repo, wtDir, driver)
				taskID, threadID := "task-acceptance", "thread-acceptance"
				if !tc.missingWorktree {
					wtPath := svc.TaskWorktreePath(taskID)
					execGit(t, repo, "worktree", "add", "-b", "mcode/kanban-"+taskBranchPrefix(taskID), wtPath)
					if err := os.WriteFile(filepath.Join(wtPath, "app.txt"), []byte("line1\nline2\nfeature\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				seedSchedulerGoal(t, ctx, st, store.Goal{
					ID: "goal-acceptance", ProjectID: "proj-1", Title: "验收",
					Policy: map[string]any{"autoAccept": tc.autoAccept, "maxConcurrency": 1},
				}, store.Task{
					ID: taskID, Title: "验收任务", Status: "in_progress", ThreadID: &threadID,
					AcceptanceCriteria: tc.criteria, RejectCount: tc.rejectCount,
				})
				driver.setThreadState(threadID, SessionState{LastAssistantText: "原回执摘要"})
				if watchdogFirst {
					if err := NewWatchdog(svc, time.Minute).Check(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					NewGoalScheduler(svc, time.Minute).Tick(ctx)
				}
				svc.wg.Wait()
				latest, err := st.GetTask(ctx, taskID)
				if err != nil {
					t.Fatal(err)
				}
				if latest.Status != tc.status || latest.RejectCount != tc.wantRejectCount {
					t.Fatalf("状态=%s RejectCount=%d，期望 %s/%d", latest.Status, latest.RejectCount, tc.status, tc.wantRejectCount)
				}
				text := latest.LastActivityText
				if text == nil || strings.Contains(*text, acceptanceRunningText) {
					t.Fatalf("验收后仍缺少结果或保留运行中标记: %v", text)
				}
				if tc.status == "todo" && (latest.ReviewFeedback == nil || *latest.ReviewFeedback != *text) {
					t.Fatalf("卡片文字与驳回反馈不一致: %v / %v", text, latest.ReviewFeedback)
				}
				if tc.feedback != "" && (text == nil || !strings.Contains(*text, tc.feedback)) {
					t.Fatalf("期望反馈包含 %q，实际 %v", tc.feedback, text)
				}
				if tc.name == "首次失败" && (text == nil || !strings.Contains(*text, "`false`") || !strings.Contains(*text, "退出码 3") || !strings.Contains(*text, "failure-output")) {
					t.Fatalf("失败反馈缺少命令、退出码或输出: %v", text)
				}
				if tc.feedback == "验收命令全部通过" && !strings.Contains(*text, "原回执摘要") {
					t.Fatalf("通过后丢失原摘要: %s", *text)
				}
				if latest.ReviewThreadID != nil {
					t.Fatalf("验收流程不应启动评审会话: %v", latest.ReviewThreadID)
				}
			})
		}
	}
}

func TestAcceptanceCommandsResumeAfterRestart(t *testing.T) {
	for _, watchdog := range []bool{false, true} {
		for _, tc := range []struct {
			name, status, text, criteria string
			autoAccept, standalone, skip bool
			wantStatus                   string
		}{
			{name: "自动接受", autoAccept: true, wantStatus: "done"},
			{name: "人工接受", wantStatus: "review"},
			{name: "你验", autoAccept: true, criteria: "[命令] `true`\n[你验] 点界面", wantStatus: "review"},
			{name: "独立任务", standalone: true, wantStatus: "review"},
			{name: "已完成", status: "done", skip: true},
			{name: "无运行标记", text: "原回执摘要", skip: true},
			{name: "无验收命令", criteria: "[审阅] 检查代码", skip: true},
		} {
			if tc.standalone && !watchdog {
				continue
			}
			t.Run(fmt.Sprintf("watchdog=%t/%s", watchdog, tc.name), func(t *testing.T) {
				ctx := context.Background()
				svc, st := setupAcceptanceService(t, t.TempDir())
				text := acceptanceRunningText
				if tc.text != "" {
					text = tc.text
				}
				task := store.Task{ID: "task-resume", Title: "重启续跑", Status: "review", LastActivityText: &text, AcceptanceCriteria: "[命令] `true`"}
				if tc.status != "" {
					task.Status = tc.status
				}
				if tc.criteria != "" {
					task.AcceptanceCriteria = tc.criteria
				}
				if err := os.MkdirAll(svc.TaskWorktreePath(task.ID), 0o755); err != nil {
					t.Fatal(err)
				}
				if tc.standalone {
					if err := st.CreateTask(ctx, task); err != nil {
						t.Fatal(err)
					}
				} else {
					seedSchedulerGoal(t, ctx, st, store.Goal{ID: "goal-resume", ProjectID: "proj-accept", Title: "续跑", Policy: map[string]any{"autoAccept": tc.autoAccept}}, task)
				}
				before, err := st.GetTask(ctx, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				if watchdog {
					if err := NewWatchdog(svc, time.Minute).Check(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					NewGoalScheduler(svc, time.Minute).Tick(ctx)
				}
				svc.wg.Wait()
				latest, err := st.GetTask(ctx, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				if tc.skip {
					if latest.Revision != before.Revision {
						t.Fatalf("不应续跑的任务被更新: %+v", latest)
					}
					return
				}
				if latest.Status != tc.wantStatus || latest.LastActivityText == nil || *latest.LastActivityText != "验收命令全部通过" {
					t.Fatalf("续跑结果错误或拼回了运行标记: %+v", latest)
				}
			})
		}
	}
}

func TestAcceptanceCommandsRecoveryDoesNotDuplicateRunning(t *testing.T) {
	ctx := context.Background()
	svc, st := setupAcceptanceService(t, t.TempDir())
	taskID := "task-recovery-dedup"
	wtPath := svc.TaskWorktreePath(taskID)
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	text := acceptanceRunningText
	seedSchedulerGoal(t, ctx, st, store.Goal{ID: "goal-recovery-dedup", ProjectID: "proj-accept", Title: "续跑去重"}, store.Task{
		ID: taskID, Title: "续跑去重", Status: "review", LastActivityText: &text,
		AcceptanceCriteria: "[命令] `printf x >> runs; while [ ! -f release ]; do sleep 0.01; done`",
	})
	scheduler := NewGoalScheduler(svc, time.Minute)
	watchdog := NewWatchdog(svc, time.Minute)
	var checks sync.WaitGroup
	for i := 0; i < 4; i++ {
		checks.Add(2)
		go func() { defer checks.Done(); scheduler.Tick(ctx) }()
		go func() {
			defer checks.Done()
			if err := watchdog.Check(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	checks.Wait()
	waitAcceptanceMarker(t, filepath.Join(wtPath, "runs"))
	if !svc.isAcceptanceRunning(taskID) {
		t.Fatal("验收命令未保持运行状态")
	}
	if err := os.WriteFile(filepath.Join(wtPath, "release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	svc.wg.Wait()
	if data, err := os.ReadFile(filepath.Join(wtPath, "runs")); err != nil || string(data) != "x" {
		t.Fatalf("调度器与看门狗重复执行验收: %q, %v", data, err)
	}
}

func TestAcceptanceCommandsConcurrentLifecycle(t *testing.T) {
	for _, action := range []string{"latest_revision", "left_review", "left_review_new_text", "close"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			svc, st := setupAcceptanceService(t, t.TempDir())
			taskID := "task-concurrent-acceptance"
			wtPath := svc.TaskWorktreePath(taskID)
			if err := os.MkdirAll(wtPath, 0o755); err != nil {
				t.Fatal(err)
			}
			createAcceptanceTask(t, st, taskID, "[命令] `printf x >> runs; while [ ! -f release ]; do sleep 0.01; done`")
			task, err := st.GetTask(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			task.Status = "review"
			task, err = st.UpdateTask(ctx, task, task.Revision)
			if err != nil {
				t.Fatal(err)
			}
			svc.startAcceptanceCommands(task, false)
			waitAcceptanceMarker(t, filepath.Join(wtPath, "runs"))
			latest, err := st.GetTask(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			if latest.LastActivityText == nil || *latest.LastActivityText != "正在跑验收命令…" {
				t.Fatalf("缺少执行中状态: %v", latest.LastActivityText)
			}
			board, err := svc.Board(ctx)
			if err != nil || len(board.Tasks) != 1 || board.Tasks[0].Derived.CanApprove || board.Tasks[0].Derived.CanReview {
				t.Fatalf("验收运行中不应允许评审: %+v, %v", board.Tasks, err)
			}
			progress := svc.goalProgress(ctx, []store.Task{latest})
			if progress["waitingOnYou"] != 0 || progress["running"] != 1 {
				t.Fatalf("验收运行中的进度错误: %+v", progress)
			}
			var starts sync.WaitGroup
			for i := 0; i < 8; i++ {
				starts.Add(1)
				go func() {
					defer starts.Done()
					svc.startAcceptanceCommands(latest, false)
				}()
			}
			starts.Wait()
			if action == "close" {
				closed := make(chan struct{})
				go func() { svc.Close(); close(closed) }()
				select {
				case <-closed:
				case <-time.After(3 * time.Second):
					t.Fatal("Close 未取消并等待验收退出")
				}
				// 关闭后不能再 Add，与 Wait 并发竞争也不能启动新命令。
				svc.startAcceptanceCommands(latest, false)
			} else {
				if action == "left_review" || action == "left_review_new_text" {
					latest.Status = "todo"
				}
				if action == "left_review_new_text" {
					text := "新的活动"
					latest.LastActivityText = &text
				}
				latest.Notes = "验收期间的新版本"
				latest, err = st.UpdateTask(ctx, latest, latest.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(wtPath, "release"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
				svc.wg.Wait()
			}
			final, err := st.GetTask(ctx, taskID)
			if err != nil {
				t.Fatal(err)
			}
			if action == "latest_revision" {
				if final.Notes != "验收期间的新版本" || final.LastActivityText == nil || !strings.Contains(*final.LastActivityText, "全部通过") {
					t.Fatalf("未基于最新 revision 写回: %+v", final)
				}
			} else if action == "left_review" {
				if final.Status != latest.Status || final.RejectCount != 0 || final.LastActivityText != nil {
					t.Fatalf("离开 review 后未清理运行状态: %+v", final)
				}
			} else if action == "left_review_new_text" {
				if final.LastActivityText == nil || *final.LastActivityText != "新的活动" || final.Revision != latest.Revision {
					t.Fatalf("离开 review 后覆盖了新的活动: %+v", final)
				}
			} else if final.Status != latest.Status || final.RejectCount != 0 || final.Revision != latest.Revision {
				t.Fatalf("取消或离开 review 后不应写回: %+v", final)
			}
			if data, err := os.ReadFile(filepath.Join(wtPath, "runs")); err != nil || string(data) != "x" {
				t.Fatalf("同一任务重复执行验收: %q, %v", data, err)
			}
		})
	}
}

func waitAcceptanceMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("验收命令未开始")
}

func TestAcceptanceStartRacesClose(t *testing.T) {
	ctx := context.Background()
	svc, st := setupAcceptanceService(t, t.TempDir())
	var tasks []store.Task
	for i := 0; i < 8; i++ {
		taskID := fmt.Sprintf("task-close-%d", i)
		createAcceptanceTask(t, st, taskID, "[命令] `true`")
		if err := os.MkdirAll(svc.TaskWorktreePath(taskID), 0o755); err != nil {
			t.Fatal(err)
		}
		task, err := st.GetTask(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		task.Status = "review"
		task, err = st.UpdateTask(ctx, task, task.Revision)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	gate := make(chan struct{})
	var starts sync.WaitGroup
	for _, task := range tasks {
		starts.Add(1)
		go func() {
			defer starts.Done()
			<-gate
			svc.startAcceptanceCommands(task, false)
		}()
	}
	closed := make(chan struct{})
	go func() { <-gate; svc.Close(); close(closed) }()
	close(gate)
	starts.Wait()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("并发启动验收时 Close 未退出")
	}
	svc.acceptanceMu.Lock()
	defer svc.acceptanceMu.Unlock()
	if len(svc.acceptanceRunning) != 0 {
		t.Fatalf("关闭后仍有在跑验收: %v", svc.acceptanceRunning)
	}
}

// TestAutoAcceptMergesFinishedTask 覆盖自动接受：无 [命令]/[你验] 的任务完成后走评审接受触发合并，
// 最终状态为 done，改动进入主工作区。
func TestAutoAcceptMergesFinishedTask(t *testing.T) {
	testenv.TempHome(t)
	ctx := context.Background()

	repo := initMergeTestRepo(t)
	wtDir := t.TempDir()
	st := openMergeTestStore(t)
	driver := newMockDriver()
	svc := newMergeTestService(t, st, repo, wtDir, driver)
	scheduler := NewGoalScheduler(svc, time.Minute)

	taskID := "task-auto-merge"
	branch := "mcode/kanban-" + taskBranchPrefix(taskID)
	wtPath := filepath.Join(wtDir, taskID)
	execGit(t, repo, "worktree", "add", "-b", branch, wtPath)
	if err := os.WriteFile(filepath.Join(wtPath, "app.txt"), []byte("line1\nline2\nfeature\n"), 0o644); err != nil {
		t.Fatalf("修改 worktree 文件失败: %v", err)
	}

	threadID := "thread-auto-merge"
	seedSchedulerGoal(t, ctx, st, store.Goal{
		ID:        "goal-auto-merge",
		ProjectID: "proj-1",
		Title:     "自动接受合并",
		Policy:    map[string]any{"autoAccept": true, "maxConcurrency": 1},
	}, store.Task{ID: taskID, Title: "自动合并任务", Status: string(TaskStatusInProgress), ThreadID: &threadID})
	driver.setThreadState(threadID, SessionState{Running: false, LastAssistantText: "已完成执行。"})

	beforeHead := execGit(t, repo, "rev-parse", "HEAD")
	scheduler.Tick(ctx)

	latest, err := st.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("获取任务失败: %v", err)
	}
	if latest.Status != string(TaskStatusDone) {
		t.Fatalf("期望自动接受后状态为 done，实际: %s", latest.Status)
	}
	if latest.ReviewStatus == nil || *latest.ReviewStatus != "passed" {
		t.Fatalf("期望 ReviewStatus 为 passed，实际: %v", latest.ReviewStatus)
	}
	if afterHead := execGit(t, repo, "rev-parse", "HEAD"); afterHead == beforeHead {
		t.Fatalf("期望自动接受触发合并、主仓库 HEAD 前进，实际仍为 %s", beforeHead)
	}
	content, err := os.ReadFile(filepath.Join(repo, "app.txt"))
	if err != nil {
		t.Fatalf("读取主工作区文件失败: %v", err)
	}
	if !strings.Contains(string(content), "feature") {
		t.Errorf("期望 worktree 改动已合入主工作区，实际内容:\n%s", content)
	}
}

// TestAutoAcceptKeepsGatedTaskInReview 覆盖含 [你验] 的任务：自动接受不生效，
// 任务停在 review，既不合并也不标记 done。
func TestAutoAcceptKeepsGatedTaskInReview(t *testing.T) {
	cases := []struct {
		name     string
		criteria string
	}{
		{name: "含你验", criteria: "- [你验] 手工点一遍界面"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testenv.TempHome(t)
			ctx := context.Background()

			repo := initMergeTestRepo(t)
			wtDir := t.TempDir()
			st := openMergeTestStore(t)
			driver := newMockDriver()
			svc := newMergeTestService(t, st, repo, wtDir, driver)
			scheduler := NewGoalScheduler(svc, time.Minute)

			taskID := "task-gated-" + tc.name
			branch := "mcode/kanban-" + taskBranchPrefix(taskID)
			wtPath := filepath.Join(wtDir, taskID)
			execGit(t, repo, "worktree", "add", "-b", branch, wtPath)
			if err := os.WriteFile(filepath.Join(wtPath, "app.txt"), []byte("line1\nline2\nfeature\n"), 0o644); err != nil {
				t.Fatalf("修改 worktree 文件失败: %v", err)
			}

			threadID := "thread-gated-" + tc.name
			seedSchedulerGoal(t, ctx, st, store.Goal{
				ID:        "goal-gated-" + tc.name,
				ProjectID: "proj-1",
				Title:     "受控验收",
				Policy:    map[string]any{"autoAccept": true, "maxConcurrency": 1},
			}, store.Task{
				ID:                 taskID,
				Title:              "受控验收任务",
				Status:             string(TaskStatusInProgress),
				ThreadID:           &threadID,
				AcceptanceCriteria: tc.criteria,
			})
			driver.setThreadState(threadID, SessionState{Running: false, LastAssistantText: "已完成执行。"})

			beforeHead := execGit(t, repo, "rev-parse", "HEAD")
			scheduler.Tick(ctx)

			latest, err := st.GetTask(ctx, taskID)
			if err != nil {
				t.Fatalf("获取任务失败: %v", err)
			}
			if latest.Status != string(TaskStatusReview) {
				t.Fatalf("期望含验收条目的任务停在 review，实际: %s", latest.Status)
			}
			if latest.ReviewStatus != nil && *latest.ReviewStatus == "passed" {
				t.Fatalf("期望不标记验收通过，实际 ReviewStatus: %v", *latest.ReviewStatus)
			}
			if afterHead := execGit(t, repo, "rev-parse", "HEAD"); afterHead != beforeHead {
				t.Errorf("期望未自动合并，实际 HEAD %s -> %s", beforeHead, afterHead)
			}
			if _, err := os.Stat(wtPath); err != nil {
				t.Errorf("期望保留 worktree，Stat err=%v", err)
			}
		})
	}
}

// TestReviewAcceptMergeFailureStaysReview 覆盖合并失败：任务留在 review，ReviewStatus 为 merge_failed，
// 失败原因同时写入 LastActivityText，主工作区保持原样。
func TestReviewAcceptMergeFailureStaysReview(t *testing.T) {
	testenv.TempHome(t)
	ctx := context.Background()

	repo := initMergeTestRepo(t)
	wtDir := t.TempDir()
	st := openMergeTestStore(t)
	svc := newMergeTestService(t, st, repo, wtDir, newMockDriver())

	taskID := "task-merge-fail"
	branch := "mcode/kanban-" + taskBranchPrefix(taskID)
	wtPath := filepath.Join(wtDir, taskID)
	execGit(t, repo, "worktree", "add", "-b", branch, wtPath)
	// worktree 与主仓库修改同一行，制造合并冲突
	if err := os.WriteFile(filepath.Join(wtPath, "app.txt"), []byte("worktree-line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("修改 worktree 文件失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("main-line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("修改主工作区文件失败: %v", err)
	}
	execGit(t, repo, "add", ".")
	execGit(t, repo, "commit", "-m", "main change")

	inReview := createReviewTask(t, svc, st, taskID, "proj-1", "合并失败任务")
	beforeHead := execGit(t, repo, "rev-parse", "HEAD")

	accepted, err := svc.ReviewTask(ctx, taskID, ReviewAccept, "", inReview.Revision)
	if err != nil {
		t.Fatalf("验收任务失败: %v", err)
	}
	if accepted.Status != string(TaskStatusReview) {
		t.Fatalf("期望合并失败后任务留在 review，实际: %s", accepted.Status)
	}
	if accepted.ReviewStatus == nil || *accepted.ReviewStatus != "merge_failed" {
		t.Fatalf("期望 ReviewStatus 为 merge_failed，实际: %v", accepted.ReviewStatus)
	}
	if accepted.LastActivityText == nil || !strings.Contains(*accepted.LastActivityText, "冲突") {
		t.Fatalf("期望 LastActivityText 含失败原因，实际: %v", accepted.LastActivityText)
	}

	persisted, err := st.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if persisted.Status != string(TaskStatusReview) || persisted.ReviewStatus == nil || *persisted.ReviewStatus != "merge_failed" {
		t.Fatalf("期望落库状态为 review/merge_failed，实际: %s / %v", persisted.Status, persisted.ReviewStatus)
	}
	if bee := execGit(t, repo, "rev-parse", "HEAD"); bee != beforeHead {
		t.Errorf("期望冲突回滚后 HEAD 不变，实际 %s -> %s", beforeHead, bee)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("期望冲突时保留 worktree，Stat err=%v", err)
	}
}
