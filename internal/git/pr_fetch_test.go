package git

import (
	"context"
	"path/filepath"
	"testing"
)

// setupPRConflict 在 origin 上准备一个与 main 改同一行的 PR head（refs/pull/1/head），
// 返回执行器、主检出目录与裸仓库路径。
func setupPRConflict(t *testing.T) (*Runner, string, string) {
	t.Helper()
	runner, dir, bare := initSyncRemote(t)
	runTestGit(t, dir, "switch", "-q", "-c", "pr-head")
	writeTestFile(t, dir, "seed.txt", "pr\n")
	runTestGit(t, dir, "commit", "-qam", "pr change")
	runTestGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/pr-head", "HEAD:refs/pull/1/head")
	runTestGit(t, dir, "switch", "-q", "main")
	writeTestFile(t, dir, "seed.txt", "main\n")
	runTestGit(t, dir, "commit", "-qam", "main change")
	runTestGit(t, dir, "push", "-q", "origin", "main")
	return runner, dir, bare
}

// TestPRConflicts 覆盖真实冲突与无冲突两种 PR head。
func TestPRConflicts(t *testing.T) {
	runner, dir, _ := setupPRConflict(t)
	ctx := context.Background()

	conflicts, err := runner.PRConflicts(ctx, dir, ForgeGitHub, 1, "main")
	if err != nil {
		t.Fatalf("PRConflicts 失败: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0] != "seed.txt" {
		t.Fatalf("冲突文件 = %v, 期望 [seed.txt]", conflicts)
	}

	// 另起一个只新增文件的 PR：与 main 无冲突，应返回空切片。
	runTestGit(t, dir, "switch", "-q", "-c", "clean-head")
	writeTestFile(t, dir, "clean.txt", "clean\n")
	runTestGit(t, dir, "add", "-A")
	runTestGit(t, dir, "commit", "-q", "-m", "clean change")
	runTestGit(t, dir, "push", "-q", "origin", "HEAD:refs/pull/2/head")
	runTestGit(t, dir, "switch", "-q", "main")

	conflicts, err = runner.PRConflicts(ctx, dir, ForgeGitHub, 2, "main")
	if err != nil {
		t.Fatalf("无冲突 PRConflicts 失败: %v", err)
	}
	if conflicts == nil || len(conflicts) != 0 {
		t.Fatalf("无冲突时 = %v, 期望空切片", conflicts)
	}
}

// TestPRResolveWorktree 覆盖新建分支、设置上游、冲突合并与重复调用。
func TestPRResolveWorktree(t *testing.T) {
	ctx := context.Background()

	t.Run("新建 mcode 分支", func(t *testing.T) {
		runner, dir, _ := setupPRConflict(t)
		path := filepath.Join(t.TempDir(), "pr-1")

		if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
			t.Fatalf("PRResolveWorktree 失败: %v", err)
		}
		state, err := runner.OpState(ctx, path)
		if err != nil {
			t.Fatalf("OpState 失败: %v", err)
		}
		if state.Kind != OpMerge {
			t.Fatalf("操作类型 = %q, 期望 merge", state.Kind)
		}
		if len(state.Conflicts) != 1 || state.Conflicts[0].Path != "seed.txt" {
			t.Fatalf("冲突文件 = %+v, 期望 seed.txt", state.Conflicts)
		}
		upstream := runTestGit(t, path, "for-each-ref", "--format=%(upstream:short)", "refs/heads/mcode/pr-1")
		if upstream != "origin/pr-head" {
			t.Fatalf("上游 = %q, 期望 origin/pr-head", upstream)
		}

		if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
			t.Fatalf("重复调用应返回 nil: %v", err)
		}
	})

	t.Run("复用已有本地分支", func(t *testing.T) {
		runner, dir, _ := setupPRConflict(t)
		if _, err := runner.FetchPRHead(ctx, dir, ForgeGitHub, 1); err != nil {
			t.Fatalf("FetchPRHead 失败: %v", err)
		}
		runTestGit(t, dir, "branch", "mcode/pr-1", "refs/mcode/pr/1")
		path := filepath.Join(t.TempDir(), "pr-1")

		if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
			t.Fatalf("PRResolveWorktree 失败: %v", err)
		}
		state, err := runner.OpState(ctx, path)
		if err != nil {
			t.Fatalf("OpState 失败: %v", err)
		}
		if state.Kind != OpMerge || len(state.Conflicts) != 1 {
			t.Fatalf("状态 = %q, 冲突 = %+v，期望 merge 且 1 个冲突", state.Kind, state.Conflicts)
		}
		upstream := runTestGit(t, path, "for-each-ref", "--format=%(upstream:short)", "refs/heads/mcode/pr-1")
		if upstream != "origin/pr-head" {
			t.Fatalf("上游 = %q, 期望 origin/pr-head", upstream)
		}
	})
}
