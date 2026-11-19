package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	gitTemplateRootOnce sync.Once
	gitTemplateRootDir  string
	gitTemplateRootErr  error
)

func getGitTemplateRootDir() (string, error) {
	gitTemplateRootOnce.Do(func() {
		gitTemplateRootDir, gitTemplateRootErr = os.MkdirTemp("", "mcode-git-test-templates-*")
	})
	return gitTemplateRootDir, gitTemplateRootErr
}

// cleanupGitTestTemplates 在 TestMain 退出前清理进程级模板仓库临时目录。
func cleanupGitTestTemplates() {
	if gitTemplateRootDir != "" {
		_ = os.RemoveAll(gitTemplateRootDir)
	}
}

type gitRepoTemplate struct {
	once   sync.Once
	dir    string
	gitBin string
	skip   bool
	err    error
}

type remoteRepoTemplate struct {
	once    sync.Once
	workDir string
	bareDir string
	gitBin  string
	err     error
}

var (
	emptyRepoTemplate  gitRepoTemplate
	readmeRepoTemplate gitRepoTemplate
	revertRepoTemplate gitRepoTemplate
	syncRemoteTpl      remoteRepoTemplate
	prConflictTpl      remoteRepoTemplate
)

func ensureEmptyRepoTemplate(t *testing.T) (string, string) {
	t.Helper()
	emptyRepoTemplate.once.Do(func() {
		gitBin, err := exec.LookPath("git")
		if err != nil {
			emptyRepoTemplate.skip = true
			emptyRepoTemplate.err = err
			return
		}
		root, err := getGitTemplateRootDir()
		if err != nil {
			emptyRepoTemplate.err = err
			return
		}
		dir := filepath.Join(root, "empty")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			emptyRepoTemplate.err = err
			return
		}
		runner := NewRunner(gitBin, 4)
		ctx := context.Background()
		if _, err := runner.run(ctx, dir, nil, "init"); err != nil {
			emptyRepoTemplate.err = fmt.Errorf("git init failed: %w", err)
			return
		}
		testEnv := []string{
			"GIT_AUTHOR_NAME=test-user",
			"GIT_AUTHOR_EMAIL=test@localhost",
			"GIT_COMMITTER_NAME=test-user",
			"GIT_COMMITTER_EMAIL=test@localhost",
		}
		if _, err := runner.run(ctx, dir, testEnv, "config", "user.name", "test-user"); err != nil {
			emptyRepoTemplate.err = fmt.Errorf("git config user.name: %w", err)
			return
		}
		if _, err := runner.run(ctx, dir, testEnv, "config", "user.email", "test@localhost"); err != nil {
			emptyRepoTemplate.err = fmt.Errorf("git config user.email: %w", err)
			return
		}
		if _, err := runner.run(ctx, dir, nil, "config", "core.autocrlf", "false"); err != nil {
			emptyRepoTemplate.err = fmt.Errorf("git config core.autocrlf: %w", err)
			return
		}
		emptyRepoTemplate.dir = dir
		emptyRepoTemplate.gitBin = gitBin
	})
	if emptyRepoTemplate.skip {
		t.Skip("git 可执行文件在 PATH 中未找到，跳过测试")
	}
	if emptyRepoTemplate.err != nil {
		t.Fatalf("init empty git template failed: %v", emptyRepoTemplate.err)
	}
	return emptyRepoTemplate.gitBin, emptyRepoTemplate.dir
}

func ensureReadmeRepoTemplate(t *testing.T) (string, string) {
	t.Helper()
	gitBin, emptyDir := ensureEmptyRepoTemplate(t)
	readmeRepoTemplate.once.Do(func() {
		root, err := getGitTemplateRootDir()
		if err != nil {
			readmeRepoTemplate.err = err
			return
		}
		dir := filepath.Join(root, "readme")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			readmeRepoTemplate.err = err
			return
		}
		if err := copyDirContents(emptyDir, dir); err != nil {
			readmeRepoTemplate.err = fmt.Errorf("copy empty template: %w", err)
			return
		}
		readmePath := filepath.Join(dir, "README.md")
		if err := os.WriteFile(readmePath, []byte("# Worktree Test Repo\n"), 0o644); err != nil {
			readmeRepoTemplate.err = fmt.Errorf("write README.md: %w", err)
			return
		}
		runner := NewRunner(gitBin, 4)
		ctx := context.Background()
		if _, err := runner.run(ctx, dir, nil, "add", "README.md"); err != nil {
			readmeRepoTemplate.err = fmt.Errorf("git add README.md: %w", err)
			return
		}
		if _, err := runner.run(ctx, dir, nil, "commit", "-m", "initial commit"); err != nil {
			readmeRepoTemplate.err = fmt.Errorf("git commit: %w", err)
			return
		}
		readmeRepoTemplate.dir = dir
		readmeRepoTemplate.gitBin = gitBin
	})
	if readmeRepoTemplate.err != nil {
		t.Fatalf("init readme git template failed: %v", readmeRepoTemplate.err)
	}
	return readmeRepoTemplate.gitBin, readmeRepoTemplate.dir
}

func ensureRevertRepoTemplate(t *testing.T) string {
	t.Helper()
	revertRepoTemplate.once.Do(func() {
		gitBin, err := exec.LookPath("git")
		if err != nil {
			revertRepoTemplate.skip = true
			revertRepoTemplate.err = err
			return
		}
		root, err := getGitTemplateRootDir()
		if err != nil {
			revertRepoTemplate.err = err
			return
		}
		dir := filepath.Join(root, "revert")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			revertRepoTemplate.err = err
			return
		}
		runner := NewRunner(gitBin, 4)
		ctx := context.Background()
		env := []string{
			"GIT_AUTHOR_NAME=mcode-test",
			"GIT_AUTHOR_EMAIL=mcode-test@localhost",
			"GIT_COMMITTER_NAME=mcode-test",
			"GIT_COMMITTER_EMAIL=mcode-test@localhost",
		}
		for _, args := range [][]string{
			{"init", "-q"},
			{"config", "user.name", "mcode-test"},
			{"config", "user.email", "mcode-test@localhost"},
			{"config", "core.autocrlf", "false"},
		} {
			if _, err := runner.run(ctx, dir, env, args...); err != nil {
				revertRepoTemplate.err = fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
				return
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
			revertRepoTemplate.err = fmt.Errorf("write seed.txt: %w", err)
			return
		}
		if _, err := runner.run(ctx, dir, env, "add", "-A"); err != nil {
			revertRepoTemplate.err = fmt.Errorf("git add -A: %w", err)
			return
		}
		if _, err := runner.run(ctx, dir, env, "commit", "-q", "-m", "init"); err != nil {
			revertRepoTemplate.err = fmt.Errorf("git commit: %w", err)
			return
		}
		revertRepoTemplate.dir = dir
		revertRepoTemplate.gitBin = gitBin
	})
	if revertRepoTemplate.skip {
		t.Skip("git 可执行文件在 PATH 中未找到，跳过测试")
	}
	if revertRepoTemplate.err != nil {
		t.Fatalf("init revert git template failed: %v", revertRepoTemplate.err)
	}
	return revertRepoTemplate.dir
}

func ensureSyncRemoteTemplate(t *testing.T) (string, string, string) {
	t.Helper()
	revertDir := ensureRevertRepoTemplate(t)
	syncRemoteTpl.once.Do(func() {
		gitBin := revertRepoTemplate.gitBin
		root, err := getGitTemplateRootDir()
		if err != nil {
			syncRemoteTpl.err = err
			return
		}
		tplRoot := filepath.Join(root, "sync-remote")
		workDir := filepath.Join(tplRoot, "work")
		bareDir := filepath.Join(tplRoot, "origin.git")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			syncRemoteTpl.err = err
			return
		}
		if err := os.MkdirAll(bareDir, 0o755); err != nil {
			syncRemoteTpl.err = err
			return
		}
		if err := copyDirContents(revertDir, workDir); err != nil {
			syncRemoteTpl.err = fmt.Errorf("copy revert template: %w", err)
			return
		}
		runner := NewRunner(gitBin, 4)
		ctx := context.Background()
		if _, err := runner.run(ctx, tplRoot, nil, "init", "--bare", "-q", bareDir); err != nil {
			syncRemoteTpl.err = fmt.Errorf("git init --bare: %w", err)
			return
		}
		if _, err := runner.run(ctx, bareDir, nil, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
			syncRemoteTpl.err = fmt.Errorf("git symbolic-ref: %w", err)
			return
		}
		out, err := runner.run(ctx, workDir, nil, "symbolic-ref", "--short", "HEAD")
		if err != nil {
			syncRemoteTpl.err = fmt.Errorf("git symbolic-ref HEAD: %w", err)
			return
		}
		if strings.TrimSpace(string(out)) != "main" {
			if _, err := runner.run(ctx, workDir, nil, "branch", "-m", "main"); err != nil {
				syncRemoteTpl.err = fmt.Errorf("git branch -m main: %w", err)
				return
			}
		}
		if _, err := runner.run(ctx, workDir, nil, "remote", "add", "origin", bareDir); err != nil {
			syncRemoteTpl.err = fmt.Errorf("git remote add origin: %w", err)
			return
		}
		if _, err := runner.run(ctx, workDir, nil, "push", "-q", "-u", "origin", "main"); err != nil {
			syncRemoteTpl.err = fmt.Errorf("git push origin main: %w", err)
			return
		}
		syncRemoteTpl.gitBin = gitBin
		syncRemoteTpl.workDir = workDir
		syncRemoteTpl.bareDir = bareDir
	})
	if syncRemoteTpl.err != nil {
		t.Fatalf("init sync remote template failed: %v", syncRemoteTpl.err)
	}
	return syncRemoteTpl.gitBin, syncRemoteTpl.workDir, syncRemoteTpl.bareDir
}

func ensurePRConflictTemplate(t *testing.T) (string, string, string) {
	t.Helper()
	gitBin, syncWork, syncBare := ensureSyncRemoteTemplate(t)
	prConflictTpl.once.Do(func() {
		root, err := getGitTemplateRootDir()
		if err != nil {
			prConflictTpl.err = err
			return
		}
		tplRoot := filepath.Join(root, "pr-conflict")
		workDir := filepath.Join(tplRoot, "work")
		bareDir := filepath.Join(tplRoot, "origin.git")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			prConflictTpl.err = err
			return
		}
		if err := os.MkdirAll(bareDir, 0o755); err != nil {
			prConflictTpl.err = err
			return
		}
		if err := copyDirContents(syncWork, workDir); err != nil {
			prConflictTpl.err = fmt.Errorf("copy sync work template: %w", err)
			return
		}
		if err := copyDirContents(syncBare, bareDir); err != nil {
			prConflictTpl.err = fmt.Errorf("copy sync bare template: %w", err)
			return
		}
		runner := NewRunner(gitBin, 4)
		ctx := context.Background()
		if _, err := runner.run(ctx, workDir, nil, "remote", "set-url", "origin", bareDir); err != nil {
			prConflictTpl.err = fmt.Errorf("git remote set-url origin: %w", err)
			return
		}
		env := []string{
			"GIT_AUTHOR_NAME=mcode-test",
			"GIT_AUTHOR_EMAIL=mcode-test@localhost",
			"GIT_COMMITTER_NAME=mcode-test",
			"GIT_COMMITTER_EMAIL=mcode-test@localhost",
		}
		if _, err := runner.run(ctx, workDir, nil, "switch", "-q", "-c", "pr-head"); err != nil {
			prConflictTpl.err = fmt.Errorf("git switch -c pr-head: %w", err)
			return
		}
		if err := os.WriteFile(filepath.Join(workDir, "seed.txt"), []byte("pr\n"), 0o644); err != nil {
			prConflictTpl.err = fmt.Errorf("write seed.txt (pr): %w", err)
			return
		}
		if _, err := runner.run(ctx, workDir, env, "commit", "-qam", "pr change"); err != nil {
			prConflictTpl.err = fmt.Errorf("git commit pr change: %w", err)
			return
		}
		if _, err := runner.run(ctx, workDir, nil, "push", "-q", "origin", "HEAD:refs/heads/pr-head", "HEAD:refs/pull/1/head"); err != nil {
			prConflictTpl.err = fmt.Errorf("git push pr-head: %w", err)
			return
		}
		if _, err := runner.run(ctx, workDir, nil, "switch", "-q", "main"); err != nil {
			prConflictTpl.err = fmt.Errorf("git switch main: %w", err)
			return
		}
		if err := os.WriteFile(filepath.Join(workDir, "seed.txt"), []byte("main\n"), 0o644); err != nil {
			prConflictTpl.err = fmt.Errorf("write seed.txt (main): %w", err)
			return
		}
		if _, err := runner.run(ctx, workDir, env, "commit", "-qam", "main change"); err != nil {
			prConflictTpl.err = fmt.Errorf("git commit main change: %w", err)
			return
		}
		if _, err := runner.run(ctx, workDir, nil, "push", "-q", "origin", "main"); err != nil {
			prConflictTpl.err = fmt.Errorf("git push main: %w", err)
			return
		}
		prConflictTpl.gitBin = gitBin
		prConflictTpl.workDir = workDir
		prConflictTpl.bareDir = bareDir
	})
	if prConflictTpl.err != nil {
		t.Fatalf("init pr conflict template failed: %v", prConflictTpl.err)
	}
	return prConflictTpl.gitBin, prConflictTpl.workDir, prConflictTpl.bareDir
}

func cloneRemoteRepoTemplate(t *testing.T, gitBin, tplWork, tplBare string) (*Runner, string, string) {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatalf("mkdir bare repo: %v", err)
	}
	if err := copyDirContents(tplWork, dir); err != nil {
		t.Fatalf("copy work repo template: %v", err)
	}
	if err := copyDirContents(tplBare, bare); err != nil {
		t.Fatalf("copy bare repo template: %v", err)
	}
	runner := NewRunner(gitBin, 2)
	if _, err := runner.run(context.Background(), dir, nil, "remote", "set-url", "origin", bare); err != nil {
		t.Fatalf("git remote set-url origin failed: %v", err)
	}
	return runner, dir, bare
}

// copyDirContents 使用普通文件内容拷贝递归复制目录结构、文件权限与符号链接（严禁硬链接）。
func copyDirContents(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if d.IsDir() {
			if mode == 0 {
				mode = 0o755
			}
			return os.MkdirAll(target, mode)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, mode)
	})
}

func TestGitTemplateRepoCopyIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. 模板 A（空初始化仓）隔离性验证
	rA1, dirA1 := initGitRepo(t)
	rA2, dirA2 := initGitRepo(t)

	if _, err := rA1.run(ctx, dirA1, nil, "config", "user.name", "isolated-user"); err != nil {
		t.Fatalf("config dirA1: %v", err)
	}
	writeTestFile(t, dirA1, "a1.txt", "only in a1\n")
	if _, err := rA1.run(ctx, dirA1, nil, "add", "a1.txt"); err != nil {
		t.Fatalf("add a1.txt: %v", err)
	}
	if _, err := rA1.run(ctx, dirA1, nil, "commit", "-m", "commit a1"); err != nil {
		t.Fatalf("commit a1: %v", err)
	}

	if out, err := rA2.run(ctx, dirA2, nil, "status", "--porcelain"); err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("dirA2 status 异常: out=%q err=%v", out, err)
	}
	if _, err := rA2.run(ctx, dirA2, nil, "rev-parse", "--verify", "HEAD"); err == nil {
		t.Fatal("dirA2 不应受 dirA1 提交影响而产生 HEAD")
	}
	if name, err := rA2.run(ctx, dirA2, nil, "config", "user.name"); err != nil || strings.TrimSpace(string(name)) != "test-user" {
		t.Fatalf("dirA2 user.name 被污染: %q err=%v", name, err)
	}

	// 2. 模板 B（带初始 README.md 提交）隔离性与文件独立性验证
	rB1, dirB1 := setupTestRepo(t)
	rB2, dirB2 := setupTestRepo(t)

	for _, rel := range []string{".git/HEAD", ".git/index", ".git/config"} {
		s1, err := os.Stat(filepath.Join(dirB1, rel))
		if err != nil {
			t.Fatalf("stat dirB1 %s: %v", rel, err)
		}
		s2, err := os.Stat(filepath.Join(dirB2, rel))
		if err != nil {
			t.Fatalf("stat dirB2 %s: %v", rel, err)
		}
		if os.SameFile(s1, s2) {
			t.Fatalf("%s 不得共享硬链接/同一底层文件", rel)
		}
	}

	headB1Before := strings.TrimSpace(string(mustRunGit(t, rB1, dirB1, "rev-parse", "HEAD")))
	headB2Before := strings.TrimSpace(string(mustRunGit(t, rB2, dirB2, "rev-parse", "HEAD")))
	if len(headB1Before) != 40 || headB1Before != headB2Before {
		t.Fatalf("模板初始 HEAD 异常: b1=%q b2=%q", headB1Before, headB2Before)
	}

	writeTestFile(t, dirB1, "README.md", "modified in b1\n")
	writeTestFile(t, dirB1, "extra.txt", "extra\n")
	mustRunGit(t, rB1, dirB1, "checkout", "-b", "branch-only-b1")
	mustRunGit(t, rB1, dirB1, "add", "-A")
	mustRunGit(t, rB1, dirB1, "commit", "-m", "b1 commit")

	if out := strings.TrimSpace(string(mustRunGit(t, rB2, dirB2, "status", "--porcelain"))); out != "" {
		t.Fatalf("dirB2 git status 应为空，实际 %q", out)
	}
	if headB2After := strings.TrimSpace(string(mustRunGit(t, rB2, dirB2, "rev-parse", "HEAD"))); headB2After != headB2Before {
		t.Fatalf("dirB2 HEAD 被污染: got %q want %q", headB2After, headB2Before)
	}
	if branches := strings.TrimSpace(string(mustRunGit(t, rB2, dirB2, "branch", "--list", "branch-only-b1"))); branches != "" {
		t.Fatalf("dirB2 出现 dirB1 分支: %q", branches)
	}
	if _, err := os.Stat(filepath.Join(dirB2, "extra.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirB2 不应存在 extra.txt: %v", err)
	}
	if got := readTestFile(t, dirB2, "README.md"); got != "# Worktree Test Repo\n" {
		t.Fatalf("dirB2 README.md 被污染: %q", got)
	}

	// 3. 模板 C（revert 测试仓）隔离性验证
	rC1, dirC1 := newRevertTestRepo(t)
	rC2, dirC2 := newRevertTestRepo(t)
	headC2Before := strings.TrimSpace(string(mustRunGit(t, rC2, dirC2, "rev-parse", "HEAD")))

	writeTestFile(t, dirC1, "seed.txt", "mutated in c1\n")
	mustRunGit(t, rC1, dirC1, "commit", "-am", "c1 commit")

	if out := strings.TrimSpace(string(mustRunGit(t, rC2, dirC2, "status", "--porcelain"))); out != "" {
		t.Fatalf("dirC2 git status 应为空，实际 %q", out)
	}
	if headC2After := strings.TrimSpace(string(mustRunGit(t, rC2, dirC2, "rev-parse", "HEAD"))); headC2After != headC2Before {
		t.Fatalf("dirC2 HEAD 被污染: got %q want %q", headC2After, headC2Before)
	}
	if got := readTestFile(t, dirC2, "seed.txt"); got != "seed\n" {
		t.Fatalf("dirC2 seed.txt 被污染: %q", got)
	}
}

func TestSyncRemoteTemplateCopyIsolation(t *testing.T) {
	t.Parallel()

	r1, dir1, bare1 := initSyncRemote(t)
	r2, dir2, bare2 := initSyncRemote(t)

	if url1 := strings.TrimSpace(string(mustRunGit(t, r1, dir1, "remote", "get-url", "origin"))); url1 != bare1 {
		t.Fatalf("dir1 origin URL = %q, want %q", url1, bare1)
	}
	if url2 := strings.TrimSpace(string(mustRunGit(t, r2, dir2, "remote", "get-url", "origin"))); url2 != bare2 {
		t.Fatalf("dir2 origin URL = %q, want %q", url2, bare2)
	}
	if bare1 == bare2 || dir1 == dir2 {
		t.Fatalf("两次调用应返回独立目录: dir1=%q dir2=%q bare1=%q bare2=%q", dir1, dir2, bare1, bare2)
	}

	bare2HeadBefore := strings.TrimSpace(string(mustRunGit(t, r2, bare2, "rev-parse", "refs/heads/main")))

	writeTestFile(t, dir1, "sync-isolated.txt", "only in 1\n")
	mustRunGit(t, r1, dir1, "add", "sync-isolated.txt")
	mustRunGit(t, r1, dir1, "commit", "-m", "push from 1")
	mustRunGit(t, r1, dir1, "push", "origin", "main")

	bare1HeadAfter := strings.TrimSpace(string(mustRunGit(t, r1, bare1, "rev-parse", "refs/heads/main")))
	if bare1HeadAfter == bare2HeadBefore {
		t.Fatalf("bare1 应在 push 后前进")
	}
	bare2HeadAfter := strings.TrimSpace(string(mustRunGit(t, r2, bare2, "rev-parse", "refs/heads/main")))
	if bare2HeadAfter != bare2HeadBefore {
		t.Fatalf("bare2 被污染: got %q want %q", bare2HeadAfter, bare2HeadBefore)
	}
	if out := strings.TrimSpace(string(mustRunGit(t, r2, dir2, "status", "--porcelain"))); out != "" {
		t.Fatalf("dir2 status 应为空，实际 %q", out)
	}
}

func mustRunGit(t *testing.T, r *Runner, dir string, args ...string) []byte {
	t.Helper()
	out, err := r.run(context.Background(), dir, nil, args...)
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}
