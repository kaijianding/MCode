package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mcode/internal/git"
	"mcode/internal/remote/wire"
)

func TestGitPRCleanupWorktreeSwitchesThreads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("测试使用 POSIX git 包装脚本")
	}
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "空闲会话切回主仓库", true: "运行中保留工作区"}[running], func(t *testing.T) {
			c, root, first := branchClient(t)
			c.dataDir = t.TempDir()
			ctx := context.Background()
			bare := filepath.Join(t.TempDir(), "origin.git")
			branchGit(t, root, "init", "--bare", bare)
			branchGit(t, root, "remote", "add", "origin", bare)
			base := branchGit(t, root, "branch", "--show-current")
			branchGit(t, root, "push", "origin", "HEAD:refs/heads/"+base, "HEAD:refs/heads/pr-head", "HEAD:refs/pull/1/head")

			// 仅平台探测使用 GitHub 地址，所有实际 Git 操作仍访问本地裸仓库。
			gitBin, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(t.TempDir(), "git")
			script := "#!/bin/sh\nif [ \"$1\" = remote ] && [ \"$2\" = get-url ]; then\n  echo https://github.com/test/repo.git\n  exit 0\nfi\nexec '" + strings.ReplaceAll(gitBin, "'", "'\\''") + "' \"$@\"\n"
			if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			c.git = git.NewRunner(wrapper, 2)
			path, err := c.GitPRResolveWorktree(ctx, first, 1, "pr-head", base)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.GitSwitchWorktree(ctx, first, path); err != nil {
				t.Fatal(err)
			}
			if reused, err := c.GitPRResolveWorktree(ctx, first, 1, "pr-head", base); err != nil || reused != path {
				t.Fatalf("从 PR worktree 复用的路径 = %q，期望 %q，错误 = %v", reused, path, err)
			}
			second, err := c.backend.CreateThread(ctx, wire.ThreadCreateParams{ProjectID: "p1"})
			if err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(path, alias); err != nil {
				t.Fatal(err)
			}
			if err := c.GitSwitchWorktree(ctx, second.ThreadID, alias); err != nil {
				t.Fatal(err)
			}
			archived := true
			if _, err := c.backend.UpdateThread(ctx, wire.ThreadUpdateParams{ThreadID: second.ThreadID, Archived: &archived}); err != nil {
				t.Fatal(err)
			}
			if running {
				feed, err := c.backend.EnsureStewardFeedThread(ctx, "p1")
				if err != nil {
					t.Fatal(err)
				}
				if err := c.GitSwitchWorktree(ctx, feed, path); err != nil {
					t.Fatal(err)
				}
				c.backend.SetStewardBusy(feed, true)
				defer c.backend.SetStewardBusy(feed, false)
			}
			reason, err := c.GitPRCleanupWorktree(ctx, first, 1)
			if err != nil {
				t.Fatal(err)
			}
			if running {
				if reason != "会话正在运行" {
					t.Fatalf("保留原因 = %q", reason)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("运行中的工作区必须保留: %v", err)
				}
				return
			}
			if reason != "" {
				t.Fatalf("清理保留原因 = %q", reason)
			}
			realRoot, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{first, second.ThreadID} {
				dir, ok := c.ThreadWorkDir(id)
				realDir, err := filepath.EvalSymlinks(dir)
				if !ok || err != nil || realDir != realRoot {
					t.Fatalf("会话 %s 未切回主仓库: %q，错误 = %v", id, dir, err)
				}
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("worktree 目录未删除: %v", err)
			}
			if out := branchGit(t, root, "branch", "--list", "mcode/pr-1"); out != "" {
				t.Fatalf("PR 本地分支未删除: %s", out)
			}
		})
	}
}
