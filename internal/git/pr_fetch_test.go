package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestPRResolveWorktreeReuseMergesNewBase(t *testing.T) {
	t.Parallel()
	runner, dir, _ := setupPRConflict(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pr-1")
	if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, "seed.txt", "resolved\n")
	runTestGit(t, path, "add", "seed.txt")
	runTestGit(t, path, "commit", "-qm", "resolve")
	runTestGit(t, path, "push", "-q", "origin", "HEAD:refs/heads/pr-head", "HEAD:refs/pull/1/head")
	writeTestFile(t, dir, "seed.txt", "new main\n")
	runTestGit(t, dir, "commit", "-qam", "advance main")
	runTestGit(t, dir, "push", "-q", "origin", "main")

	// 未提交修改挡住合并时必须返回错误，不能误报解决工作区已就绪。
	writeTestFile(t, path, "seed.txt", "uncommitted\n")
	if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err == nil {
		t.Fatal("未提交修改阻止合并时应返回错误")
	}
	writeTestFile(t, path, "seed.txt", "resolved\n")
	if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
		t.Fatal(err)
	}
	state, err := runner.OpState(ctx, path)
	if err != nil || state.Kind != OpMerge || len(state.Conflicts) != 1 || state.Conflicts[0].Path != "seed.txt" {
		t.Fatalf("复用后状态 = %+v，错误 = %v", state, err)
	}
}

func TestPRWorktreeKeepReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
	}{
		{"未提交改动", "有未提交的改动"},
		{"未完成合并", "合并尚未完成"},
		{"未推送提交", "有未推送的提交"},
		{"已推送且源分支删除", ""},
		{"未注册路径", ""},
		{"抓取失败", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runner, dir, bare := setupPRConflict(t)
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "pr-1")
			if tc.name == "未注册路径" {
				if reason, err := runner.PRWorktreeKeepReason(ctx, dir, path, ForgeUnknown, 1); err != nil || reason != "" {
					t.Fatalf("原因 = %q，错误 = %v", reason, err)
				}
				return
			}
			if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
				t.Fatal(err)
			}
			if err := runner.AbortOp(ctx, path); err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "未提交改动":
				writeTestFile(t, path, "untracked.txt", "keep\n")
			case "未完成合并":
				runTestGit(t, path, "merge", "--no-commit", "-s", "ours", "origin/main")
			case "未推送提交", "已推送且源分支删除":
				writeTestFile(t, path, "seed.txt", "resolution\n")
				runTestGit(t, path, "commit", "-qam", "resolution")
				if tc.want == "" {
					runTestGit(t, path, "push", "-q", "origin", "HEAD:refs/pull/1/head", ":refs/heads/pr-head")
				}
			case "抓取失败":
				runTestGit(t, bare, "update-ref", "-d", "refs/pull/1/head")
			}
			reason, err := runner.PRWorktreeKeepReason(ctx, dir, path, ForgeGitHub, 1)
			if tc.name == "抓取失败" {
				if err == nil {
					t.Fatal("无法确认远端提交时必须返回错误")
				}
				return
			}
			if err != nil || reason != tc.want {
				t.Fatalf("原因 = %q，期望 %q，错误 = %v", reason, tc.want, err)
			}
		})
	}
}

func TestPRWorktreeKeepReasonBranchWithoutWorktree(t *testing.T) {
	t.Parallel()
	runner, dir, _ := setupPRConflict(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pr-1")
	if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
		t.Fatal(err)
	}
	if err := runner.AbortOp(ctx, path); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, "seed.txt", "resolution\n")
	runTestGit(t, path, "commit", "-qam", "resolution")
	if err := runner.WorktreeRemove(ctx, dir, path, false); err != nil {
		t.Fatal(err)
	}

	reason, err := runner.PRWorktreeKeepReason(ctx, dir, path, ForgeGitHub, 1)
	if err != nil || reason != "有未推送的提交" {
		t.Fatalf("原因 = %q，错误 = %v", reason, err)
	}

	runTestGit(t, dir, "branch", "-D", "mcode/pr-1")
	if reason, err := runner.PRWorktreeKeepReason(ctx, dir, path, ForgeGitHub, 1); err != nil || reason != "" {
		t.Fatalf("分支不存在时原因 = %q，错误 = %v", reason, err)
	}
}

func TestPRRemoveWorktree(t *testing.T) {
	t.Parallel()
	for _, branchOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(branchOnly), func(t *testing.T) {
			t.Parallel()
			runner, dir, _ := setupPRConflict(t)
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "pr-1")
			if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
				t.Fatal(err)
			}
			if err := runner.AbortOp(ctx, path); err != nil {
				t.Fatal(err)
			}
			if branchOnly {
				if err := runner.WorktreeRemove(ctx, dir, path, false); err != nil {
					t.Fatal(err)
				}
			} else {
				writeTestFile(t, path, "keep.txt", "keep\n")
				if err := runner.PRRemoveWorktree(ctx, dir, path, 1); err == nil {
					t.Fatal("不能删除带未提交改动的 worktree")
				}
				if err := os.Remove(filepath.Join(path, "keep.txt")); err != nil {
					t.Fatal(err)
				}
			}
			if err := runner.PRRemoveWorktree(ctx, dir, path, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("worktree 目录仍存在: %v", err)
			}
			if out := runTestGit(t, dir, "branch", "--list", "mcode/pr-1"); out != "" {
				t.Fatalf("本地分支仍存在: %s", out)
			}
			if err := runner.PRRemoveWorktree(ctx, dir, path, 1); err != nil {
				t.Fatalf("重复清理应成功: %v", err)
			}
		})
	}
}

// TestPRConflicts 覆盖真实冲突与无冲突两种 PR head。
func TestPRConflicts(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	ctx := context.Background()

	t.Run("新建 mcode 分支", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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

// TestPRCheckoutWorktree 覆盖只检出 PR head、不合并 base，以及 worktree 已存在时不动进行中的合并。
func TestPRCheckoutWorktree(t *testing.T) {
	t.Parallel()
	runner, dir, _ := setupPRConflict(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pr-1")

	if err := runner.PRCheckoutWorktree(ctx, dir, path, 1, "pr-head"); err != nil {
		t.Fatalf("PRCheckoutWorktree 失败: %v", err)
	}
	if branch := runTestGit(t, path, "symbolic-ref", "--short", "HEAD"); branch != "mcode/pr-1" {
		t.Fatalf("当前分支 = %q, 期望 mcode/pr-1", branch)
	}
	head := runTestGit(t, path, "rev-parse", "HEAD")
	want := runTestGit(t, path, "rev-parse", "origin/pr-head")
	if head != want {
		t.Fatalf("HEAD = %s, 期望 origin/pr-head = %s", head, want)
	}
	state, err := runner.OpState(ctx, path)
	if err != nil {
		t.Fatalf("OpState 失败: %v", err)
	}
	if state.Kind != "" {
		t.Fatalf("不应发生合并，操作类型 = %q", state.Kind)
	}
	if merges := runTestGit(t, path, "rev-list", "--merges", "--count", "HEAD"); merges != "0" {
		t.Fatalf("不应有合并提交，得到 %s", merges)
	}
	if got := readTestFile(t, path, "seed.txt"); got != "pr\n" {
		t.Fatalf("seed.txt = %q, 期望未合并的 pr", got)
	}

	// 再次检出直接返回 nil，不修改已有 worktree。
	if err := runner.PRCheckoutWorktree(ctx, dir, path, 1, "pr-head"); err != nil {
		t.Fatalf("重复检出应返回 nil: %v", err)
	}

	// 产生进行中的合并后，检出不能让合并中断。
	if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
		t.Fatalf("PRResolveWorktree 失败: %v", err)
	}
	if err := runner.PRCheckoutWorktree(ctx, dir, path, 1, "pr-head"); err != nil {
		t.Fatalf("已有 worktree 时应返回 nil: %v", err)
	}
	state, err = runner.OpState(ctx, path)
	if err != nil || state.Kind != OpMerge || len(state.Conflicts) != 1 {
		t.Fatalf("进行中的合并被改动: 状态 = %+v，错误 = %v", state, err)
	}
}

func TestReviewPRBaseRejectsInvalidBranch(t *testing.T) {
	t.Parallel()
	runner, dir, _ := initSyncRemote(t)
	ctx := context.Background()

	fetchMarker := filepath.Join(t.TempDir(), "fetch-called")
	if runtime.GOOS != "windows" {
		gitBin, err := exec.LookPath("git")
		if err != nil {
			t.Fatal(err)
		}
		wrapper := filepath.Join(t.TempDir(), "git")
		script := "#!/bin/sh\nif [ \"$1\" = fetch ]; then\n  : > '" + strings.ReplaceAll(fetchMarker, "'", "'\\''") + "'\n  exit 1\nfi\nexec '" + strings.ReplaceAll(gitBin, "'", "'\\''") + "' \"$@\"\n"
		if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		runner = NewRunner(wrapper, 2)
	}

	for _, bad := range []string{"-main", "--upload-pack=evil"} {
		if _, err := runner.ReviewPRBase(ctx, dir, bad, "HEAD"); err == nil {
			t.Fatalf("应拒绝非法分支名 %q", bad)
		} else if strings.Contains(err.Error(), "fetch PR base") {
			t.Fatalf("校验前不应执行 fetch: %v", err)
		}
		if _, err := os.Stat(fetchMarker); !os.IsNotExist(err) {
			t.Fatalf("非法分支名 %q 不应触发 git fetch", bad)
		}
	}
}

func TestPRWorktreeCleanupPrunable(t *testing.T) {
	t.Parallel()
	runner, dir, _ := setupPRConflict(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pr-1")
	if err := runner.PRResolveWorktree(ctx, dir, path, 1, "pr-head", "main"); err != nil {
		t.Fatal(err)
	}
	if err := runner.AbortOp(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	reason, err := runner.PRWorktreeKeepReason(ctx, dir, path, ForgeGitHub, 1)
	if err != nil || reason != "" {
		t.Fatalf("PRWorktreeKeepReason = (%q, %v), 期望 (\"\", nil)", reason, err)
	}
	if err := runner.PRRemoveWorktree(ctx, dir, path, 1); err != nil {
		t.Fatalf("PRRemoveWorktree 失败: %v", err)
	}
	if out := runTestGit(t, dir, "branch", "--list", "mcode/pr-1"); out != "" {
		t.Fatalf("本地分支仍存在: %s", out)
	}
	worktrees, err := runner.WorktreeList(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range worktrees {
		if resolvePath(wt.Path) == resolvePath(path) {
			t.Fatalf("prunable worktree 记录未清理: %+v", wt)
		}
	}
}
