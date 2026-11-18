package swarm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcode/internal/git"
	"mcode/internal/kanban"
)

// newRetryBeeManager 造一个已启用、含指定 bee 记录的管理器，供「沿用改动续做」的用例共用。
func newRetryBeeManager(t *testing.T, leadID string, bees ...Bee) (*Manager, *fakeDriver, *fakePersister, *git.Runner, string) {
	t.Helper()
	runner, dir := initTestRepo(t)
	driver := newFakeDriver()
	persister := newFakePersister()
	mgr := NewManager(Options{
		Git:         runner,
		Driver:      driver,
		WorktreeDir: t.TempDir(),
		Persister:   persister,
	})
	mgr.SetEnabled(leadID, true)

	mgr.mu.Lock()
	lead := mgr.leads[leadID]
	for _, bee := range bees {
		if bee.TargetWorkDir == "" {
			bee.TargetWorkDir = dir
		}
		if bee.ProjectID == "" {
			bee.ProjectID = "proj-1"
		}
		lead.bees = append(lead.bees, bee)
	}
	mgr.mu.Unlock()
	return mgr, driver, persister, runner, dir
}

// 验证 RetryBee 成功复用失败 bee 的原 worktree 与半成品：新 bee 替换旧条目、沿用 Index、
// 提示里带上改动文件清单，并且只给一次重试机会。
func TestManager_RetryBee_ReusesWorktreeWithPartialWork(t *testing.T) {
	t.Parallel()
	const leadID = "lead-retry-reuse"
	ctx := context.Background()

	// 先手工造一份「上一个子代理改了一半」的 worktree，记录落在 bee 上。
	mgr, driver, persister, runner, dir := newRetryBeeManager(t, leadID)
	base, err := runner.RevParse(ctx, dir, "HEAD")
	if err != nil {
		t.Fatalf("RevParse failed: %v", err)
	}
	wtPath := filepath.Join(t.TempDir(), "wt-reuse")
	branch := "mcode/swarm/lead-ret/0"
	if err := runner.WorktreeAdd(ctx, dir, wtPath, branch, base); err != nil {
		t.Fatalf("WorktreeAdd failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "partial.txt"), []byte("half done\n"), 0644); err != nil {
		t.Fatalf("write partial.txt: %v", err)
	}

	mgr.mu.Lock()
	mgr.leads[leadID].bees = append(mgr.leads[leadID].bees, Bee{
		ThreadID:      "bee-failed-1",
		Index:         0,
		Name:          "Aspen",
		Task:          "补齐改动",
		Prompt:        "原始 brief",
		Status:        StatusFailed,
		Summary:       "工具调用失败",
		WorktreePath:  wtPath,
		Branch:        branch,
		BaseCommit:    base,
		ChangedFiles:  []string{"partial.txt"},
		ProjectID:     "proj-1",
		TargetWorkDir: dir,
		RuntimeMode:   "auto",
		Acceptance:    []string{"true"},
		Reported:      true,
	})
	mgr.mu.Unlock()

	newBee, err := mgr.RetryBee(ctx, leadID, "bee-failed-1")
	if err != nil {
		t.Fatalf("RetryBee failed: %v", err)
	}
	if newBee.ThreadID == "bee-failed-1" {
		t.Fatalf("重试应派出新会话，实际仍是旧线程 %s", newBee.ThreadID)
	}
	if newBee.Status != StatusRunning {
		t.Fatalf("重试出的 bee 应为 running，实际 %s", newBee.Status)
	}
	if newBee.WorktreePath != wtPath || newBee.Branch != branch || newBee.BaseCommit != base {
		t.Fatalf("重试出的 bee 应复用原 worktree 与基线，实际 %+v", newBee)
	}
	if newBee.Index != 0 {
		t.Fatalf("重试出的 bee 应沿用原 Index=0，实际 %d", newBee.Index)
	}
	if len(newBee.Acceptance) != 1 || newBee.Acceptance[0] != "true" {
		t.Fatalf("重试丢失验收命令：%v", newBee.Acceptance)
	}
	if newBee.RuntimeMode != "auto" {
		t.Fatalf("重试出的 bee 应继承原权限模式 auto，实际 %q", newBee.RuntimeMode)
	}
	if !strings.Contains(newBee.Prompt, "partial.txt") || !strings.Contains(newBee.Prompt, "工具调用失败") {
		t.Fatalf("重试提示应说明现状与改动文件：%q", newBee.Prompt)
	}
	if got := mgr.getRetryCount(newBee.ThreadID); got != 1 {
		t.Fatalf("重试出的 bee 应标记为已用掉重试机会，实际 %d", got)
	}

	// 旧条目被新 bee 替换，数量不变；落盘记录同步更新。
	st := mgr.State(leadID)
	if len(st.Bees) != 1 || st.Bees[0].ThreadID != newBee.ThreadID {
		t.Fatalf("lead 里应只剩替换后的新 bee，实际 %+v", st.Bees)
	}
	if persisted := persister.leads[leadID]; len(persisted.Bees) != 1 || persisted.Bees[0].ThreadID != newBee.ThreadID {
		t.Fatalf("落盘记录应保存重试后的新 bee：%+v", persisted.Bees)
	}

	// 新会话确实走到了驱动层，且仍挂在原主会话下。
	driver.mu.Lock()
	relation := driver.relations[newBee.ThreadID]
	driver.mu.Unlock()
	if relation.parentThreadID != leadID || !relation.isHelper {
		t.Fatalf("新 bee 会话应挂在原主会话下，实际 %+v", relation)
	}
}

// 验证状态不是 failed/stopped 的 bee（这里用已完成的）不允许沿用改动续做。
func TestManager_RetryBee_RejectsFinishedStatus(t *testing.T) {
	t.Parallel()
	const leadID = "lead-retry-status"
	ctx := context.Background()

	mgr, _, _, _, _ := newRetryBeeManager(t, leadID, Bee{
		ThreadID: "bee-done-1",
		Index:    0,
		Name:     "Birch",
		Task:     "已完成",
		Status:   StatusCompleted,
	})
	if _, err := mgr.RetryBee(ctx, leadID, "bee-done-1"); err == nil || !strings.Contains(err.Error(), "只有失败或已停止") {
		t.Fatalf("已完成状态应被拒绝，实际 %v", err)
	}

	// 已合并的失败 bee 同样不能沿用改动续做。
	mgr.mu.Lock()
	mgr.leads[leadID].bees = append(mgr.leads[leadID].bees, Bee{
		ThreadID: "bee-merged-2",
		Index:    1,
		Name:     "Cedar",
		Status:   StatusFailed,
		Merged:   true,
	})
	mgr.mu.Unlock()
	if _, err := mgr.RetryBee(ctx, leadID, "bee-merged-2"); err == nil || !strings.Contains(err.Error(), "已合并") {
		t.Fatalf("已合并的 bee 应被拒绝，实际 %v", err)
	}

	// 被拒绝后旧记录保持原样，不会残留派发中的占位状态。
	if st := mgr.State(leadID); len(st.Bees) != 2 || st.Bees[0].ThreadID != "bee-done-1" {
		t.Fatalf("拒绝后不应改动 bee 列表：%+v", st.Bees)
	}
}

// 验证 bee 不属于该主会话时不重试：既接不住别的 lead 的 bee，也接不住不存在的线程。
func TestManager_RetryBee_RejectsBeeFromOtherLead(t *testing.T) {
	t.Parallel()
	const leadID = "lead-retry-owner"
	const otherLeadID = "lead-retry-other"
	ctx := context.Background()

	mgr, _, _, _, _ := newRetryBeeManager(t, leadID, Bee{
		ThreadID: "bee-mine-1",
		Index:    0,
		Name:     "Aspen",
		Task:     "我的任务",
		Status:   StatusFailed,
	})
	mgr.SetEnabled(otherLeadID, true)
	mgr.mu.Lock()
	mgr.leads[otherLeadID].bees = append(mgr.leads[otherLeadID].bees, Bee{
		ThreadID: "bee-other-1",
		Index:    0,
		Name:     "Elm",
		Task:     "别人的任务",
		Status:   StatusFailed,
	})
	mgr.mu.Unlock()

	if _, err := mgr.RetryBee(ctx, leadID, "bee-other-1"); !errors.Is(err, ErrBeeNotFound) {
		t.Fatalf("不属于该 lead 的 bee 应返回 ErrBeeNotFound，实际 %v", err)
	}
	if _, err := mgr.RetryBee(ctx, leadID, "bee-missing"); !errors.Is(err, ErrBeeNotFound) {
		t.Fatalf("不存在的 bee 应返回 ErrBeeNotFound，实际 %v", err)
	}
	if _, err := mgr.RetryBee(ctx, "lead-missing", "bee-mine-1"); !errors.Is(err, ErrLeadNotFound) {
		t.Fatalf("主会话不存在时应返回 ErrLeadNotFound，实际 %v", err)
	}

	// 别的 lead 的 bee 必须原封不动。
	st := mgr.State(otherLeadID)
	if len(st.Bees) != 1 || st.Bees[0].ThreadID != "bee-other-1" || st.Bees[0].Status != StatusFailed {
		t.Fatalf("跨会话的重试请求不应改动另一个主会话的记录：%+v", st.Bees)
	}
}

// 验证原 worktree 已被删除时退回新建 worktree，并在提示里说明半成品已丢失。
func TestManager_RetryBee_FallsBackWhenWorktreeGone(t *testing.T) {
	t.Parallel()
	const leadID = "lead-retry-gone"
	ctx := context.Background()

	goneWorktree := filepath.Join(t.TempDir(), "gone")
	mgr, _, _, _, dir := newRetryBeeManager(t, leadID, Bee{
		ThreadID:     "bee-stopped-1",
		Index:        0,
		Name:         "Fir",
		Task:         "补测试",
		Prompt:       "原始 brief",
		Status:       StatusStopped,
		Summary:      "已停止",
		WorktreePath: goneWorktree,
		Branch:       "mcode/swarm/lead-ret/0",
		BaseCommit:   "abc123",
	})

	newBee, err := mgr.RetryBee(ctx, leadID, "bee-stopped-1")
	if err != nil {
		t.Fatalf("RetryBee failed: %v", err)
	}
	if newBee.WorktreePath == "" || newBee.WorktreePath == goneWorktree {
		t.Fatalf("worktree 已不存在时应新建，实际 %q", newBee.WorktreePath)
	}
	if _, statErr := os.Stat(newBee.WorktreePath); statErr != nil {
		t.Fatalf("新建的 worktree 应该真实存在：%v", statErr)
	}
	if newBee.Branch != "" {
		t.Fatalf("新建副本应使用 detached HEAD：branch=%q", newBee.Branch)
	}
	if newBee.BaseCommit == "abc123" {
		t.Fatalf("新建 worktree 应重新取基线，实际沿用了旧基线 %q", newBee.BaseCommit)
	}
	if newBee.TargetWorkDir != dir {
		t.Fatalf("新 bee 仍应以原工作区为目标目录，实际 %q", newBee.TargetWorkDir)
	}
	if !strings.Contains(newBee.Prompt, "工作副本已经不存在") || !strings.Contains(newBee.Prompt, "原始 brief") {
		t.Fatalf("半成品丢失时提示应说明并从原 brief 重做：%q", newBee.Prompt)
	}
	if st := mgr.State(leadID); len(st.Bees) != 1 || st.Bees[0].ThreadID != newBee.ThreadID {
		t.Fatalf("lead 里应只剩替换后的新 bee，实际 %+v", st.Bees)
	}
}

// 验证 retryingBees 中的 failed bee 在 ClearFinished 时不会被删掉副本和记录。
func TestClearFinishedSkipsRetryingBee(t *testing.T) {
	t.Parallel()
	const leadID = "lead-clear-skips-retrying"
	ctx := context.Background()

	mgr, _, _, runner, dir := newRetryBeeManager(t, leadID)
	base, err := runner.RevParse(ctx, dir, "HEAD")
	if err != nil {
		t.Fatalf("RevParse failed: %v", err)
	}
	retryingWT := filepath.Join(t.TempDir(), "wt-retrying")
	retryingBranch := "mcode/swarm/lead-csr/0"
	if err := runner.WorktreeAdd(ctx, dir, retryingWT, retryingBranch, base); err != nil {
		t.Fatalf("WorktreeAdd retrying failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(retryingWT, "partial.txt"), []byte("keep me\n"), 0644); err != nil {
		t.Fatalf("write partial.txt: %v", err)
	}

	finishedWT := filepath.Join(t.TempDir(), "wt-finished")
	finishedBranch := "mcode/swarm/lead-csr/1"
	if err := runner.WorktreeAdd(ctx, dir, finishedWT, finishedBranch, base); err != nil {
		t.Fatalf("WorktreeAdd finished failed: %v", err)
	}

	mgr.mu.Lock()
	mgr.leads[leadID].bees = append(mgr.leads[leadID].bees,
		Bee{
			ThreadID:      "bee-retrying",
			Index:         0,
			Name:          "Aspen",
			Task:          "续做中",
			Status:        StatusFailed,
			WorktreePath:  retryingWT,
			Branch:        retryingBranch,
			BaseCommit:    base,
			TargetWorkDir: dir,
			Reported:      true,
		},
		Bee{
			ThreadID:      "bee-done",
			Index:         1,
			Name:          "Birch",
			Task:          "已结束",
			Status:        StatusFailed,
			WorktreePath:  finishedWT,
			Branch:        finishedBranch,
			BaseCommit:    base,
			TargetWorkDir: dir,
			Reported:      true,
		},
	)
	if mgr.retryingBees == nil {
		mgr.retryingBees = make(map[string]bool)
	}
	mgr.retryingBees["bee-retrying"] = true
	mgr.mu.Unlock()

	if err := mgr.ClearFinished(ctx, leadID); err != nil {
		t.Fatalf("ClearFinished failed: %v", err)
	}

	// retryingBees 中的 failed bee 必须保留记录与副本，非 retrying 的已结束 bee 被清理。
	st := mgr.State(leadID)
	if len(st.Bees) != 1 || st.Bees[0].ThreadID != "bee-retrying" {
		t.Fatalf("应只保留 bee-retrying，实际 %+v", st.Bees)
	}
	if _, err := os.Stat(filepath.Join(retryingWT, "partial.txt")); err != nil {
		t.Fatalf("retrying bee 的副本不应被删: %v", err)
	}
	if _, err := os.Stat(finishedWT); !os.IsNotExist(err) {
		t.Fatalf("普通已结束 bee 的副本应被删除，stat err=%v", err)
	}

	// 再验证 RetryBee 与 ClearFinished 并发：RetryBee 从读取旧记录到替换完成全程持 mergeMu，
	// ClearFinished 不会在交接中途删掉复用的副本。
	mgr.mu.Lock()
	delete(mgr.retryingBees, "bee-retrying")
	mgr.mu.Unlock()

	driver := mgr.opts.Driver.(*fakeDriver)
	startEntered := make(chan struct{}, 1)
	allowStart := make(chan struct{})
	driver.onStart = func(_ kanban.SessionRequest) {
		select {
		case startEntered <- struct{}{}:
		default:
		}
		<-allowStart
	}

	retryDone := make(chan error, 1)
	go func() {
		_, err := mgr.RetryBee(ctx, leadID, "bee-retrying")
		retryDone <- err
	}()

	select {
	case <-startEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("RetryBee 未进入 StartSession")
	}

	clearDone := make(chan error, 1)
	go func() {
		clearDone <- mgr.ClearFinished(ctx, leadID)
	}()

	select {
	case err := <-clearDone:
		t.Fatalf("RetryBee 持 mergeMu 期间 ClearFinished 不应提前完成: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(allowStart)
	if err := <-retryDone; err != nil {
		t.Fatalf("RetryBee 失败: %v", err)
	}
	if err := <-clearDone; err != nil {
		t.Fatalf("ClearFinished 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(retryingWT, "partial.txt")); err != nil {
		t.Fatalf("并发 ClearFinished 后复用的副本不应被删: %v", err)
	}
	if got := mgr.State(leadID).Bees; len(got) != 1 || got[0].Status != StatusRunning {
		t.Fatalf("并发后应保留新派发的 running bee，实际 %+v", got)
	}
}

// 验证另一 goroutine 持该 lead 的 mergeMu 时 ClearFinished 等待，不提前删副本，释放后才删。
func TestClearFinishedWaitsLeadMergeMutex(t *testing.T) {
	t.Parallel()
	const leadID = "lead-clear-waits-mergemu"
	ctx := context.Background()

	mgr, _, _, runner, dir := newRetryBeeManager(t, leadID)
	base, err := runner.RevParse(ctx, dir, "HEAD")
	if err != nil {
		t.Fatalf("RevParse failed: %v", err)
	}
	wtPath := filepath.Join(t.TempDir(), "wt-wait-lock")
	branch := "mcode/swarm/lead-cwm/0"
	if err := runner.WorktreeAdd(ctx, dir, wtPath, branch, base); err != nil {
		t.Fatalf("WorktreeAdd failed: %v", err)
	}

	mgr.mu.Lock()
	mgr.leads[leadID].bees = append(mgr.leads[leadID].bees, Bee{
		ThreadID:      "bee-finished-1",
		Index:         0,
		Name:          "Cedar",
		Task:          "待清理",
		Status:        StatusFailed,
		WorktreePath:  wtPath,
		Branch:        branch,
		BaseCommit:    base,
		TargetWorkDir: dir,
		Reported:      true,
	})
	mgr.mu.Unlock()

	mergeMu := mgr.getLeadMergeMutex(leadID)
	mergeMu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			mergeMu.Unlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- mgr.ClearFinished(ctx, leadID)
	}()

	select {
	case err := <-done:
		t.Fatalf("持有 mergeMu 期间 ClearFinished 不应返回: %v", err)
	case <-time.After(80 * time.Millisecond):
	}

	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("持有 mergeMu 期间不应删除副本: %v", err)
	}
	if got := mgr.State(leadID).Bees; len(got) != 1 {
		t.Fatalf("持有 mergeMu 期间不应删除记录: %+v", got)
	}

	unlocked = true
	mergeMu.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ClearFinished 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("释放 mergeMu 后 ClearFinished 未在 5 秒内完成")
	}

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("释放 mergeMu 后副本应已删除，stat err=%v", err)
	}
	if got := mgr.State(leadID).Bees; len(got) != 0 {
		t.Fatalf("释放 mergeMu 后记录应已删除: %+v", got)
	}
}
