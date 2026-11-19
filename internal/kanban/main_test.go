package kanban

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	os.Exit(runKanbanTests(m))
}

func runKanbanTests(m *testing.M) int {
	homeDir, err := os.MkdirTemp("", "mcode-kanban-test-home-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(homeDir)
	defer cleanupMergeTestTemplate()

	if err := os.Setenv("HOME", homeDir); err != nil {
		panic(err)
	}
	if err := os.Setenv("USERPROFILE", homeDir); err != nil {
		panic(err)
	}
	return m.Run()
}

// TestKanbanTemplateRepoAndTimeoutIsolation 验证模板仓库拷贝相互独立（无硬链接且修改不串扰），
// 以及实例级验收超时覆盖不会污染包级默认值与其他 Service 实例。
func TestKanbanTemplateRepoAndTimeoutIsolation(t *testing.T) {
	t.Parallel()

	// 1. 并发从模板仓库拷贝出多个独立仓库，验证无硬链接且各自提交/分支互不影响。
	repos := make([]string, 3)
	var wg sync.WaitGroup
	for i := range repos {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			repos[idx] = initMergeTestRepo(t)
		}(i)
	}
	wg.Wait()

	for _, rel := range []string{".git/HEAD", ".git/index", ".git/config", "app.txt"} {
		s0, err := os.Stat(filepath.Join(repos[0], rel))
		if err != nil {
			t.Fatalf("stat repo[0] %s: %v", rel, err)
		}
		for i := 1; i < len(repos); i++ {
			si, err := os.Stat(filepath.Join(repos[i], rel))
			if err != nil {
				t.Fatalf("stat repo[%d] %s: %v", i, rel, err)
			}
			if os.SameFile(s0, si) {
				t.Fatalf("%s 不得共享硬链接或同一底层文件", rel)
			}
		}
	}

	headBefore := execGit(t, repos[1], "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repos[0], "app.txt"), []byte("mutated-in-repo0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repos[0], "extra.txt"), []byte("extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	execGit(t, repos[0], "checkout", "-b", "branch-only-repo0")
	execGit(t, repos[0], "add", "-A")
	execGit(t, repos[0], "commit", "-m", "repo0 commit")

	if status := execGit(t, repos[1], "status", "--porcelain"); status != "" {
		t.Fatalf("repo[1] 工作区被污染: %q", status)
	}
	if headAfter := execGit(t, repos[1], "rev-parse", "HEAD"); headAfter != headBefore {
		t.Fatalf("repo[1] HEAD 被污染: %s -> %s", headBefore, headAfter)
	}
	if branches := execGit(t, repos[1], "branch", "--list", "branch-only-repo0"); branches != "" {
		t.Fatalf("repo[1] 出现 repo[0] 分支: %q", branches)
	}
	if content, err := os.ReadFile(filepath.Join(repos[1], "app.txt")); err != nil || string(content) != "line1\nline2\n" {
		t.Fatalf("repo[1] app.txt 被污染: %q, %v", content, err)
	}

	// 2. 验证实例级验收超时覆盖不污染默认值及其他 Service 实例。
	svcDefault, stDefault := setupAcceptanceService(t, t.TempDir())
	svcShort, stShort := setupAcceptanceService(t, t.TempDir())
	svcShort.acceptanceCommandTimeout = 80 * time.Millisecond
	svcShort.acceptanceTotalTimeout = 200 * time.Millisecond

	if acceptanceCommandTimeout != 5*time.Minute || acceptanceTotalTimeout != 15*time.Minute {
		t.Fatalf("包级默认超时被修改: cmd=%s total=%s", acceptanceCommandTimeout, acceptanceTotalTimeout)
	}
	if cmdT, totalT := svcDefault.acceptanceTimeouts(); cmdT != 5*time.Minute || totalT != 15*time.Minute {
		t.Fatalf("默认实例超时被污染: cmd=%s total=%s", cmdT, totalT)
	}
	if cmdT, totalT := svcShort.acceptanceTimeouts(); cmdT != 80*time.Millisecond || totalT != 200*time.Millisecond {
		t.Fatalf("覆盖实例超时未生效: cmd=%s total=%s", cmdT, totalT)
	}

	if err := os.MkdirAll(svcShort.TaskWorktreePath("task-short"), 0o755); err != nil {
		t.Fatal(err)
	}
	createAcceptanceTask(t, stShort, "task-short", "- [命令] `sleep 5`")
	shortRes, err := svcShort.RunAcceptanceCommands(context.Background(), "task-short")
	if err != nil || len(shortRes) != 1 || !shortRes[0].TimedOut || shortRes[0].ExitCode != -1 {
		t.Fatalf("覆盖实例应快速超时，实际 res=%+v err=%v", shortRes, err)
	}
	if shortRes[0].Duration > 2*time.Second {
		t.Fatalf("覆盖实例超时耗时过长: %s", shortRes[0].Duration)
	}

	if err := os.MkdirAll(svcDefault.TaskWorktreePath("task-default"), 0o755); err != nil {
		t.Fatal(err)
	}
	createAcceptanceTask(t, stDefault, "task-default", "- [命令] `echo ok`")
	defaultRes, err := svcDefault.RunAcceptanceCommands(context.Background(), "task-default")
	if err != nil || len(defaultRes) != 1 || defaultRes[0].TimedOut || defaultRes[0].ExitCode != 0 || !strings.Contains(defaultRes[0].Output, "ok") {
		t.Fatalf("默认实例执行异常: res=%+v err=%v", defaultRes, err)
	}
	if cmdT, totalT := svcDefault.acceptanceTimeouts(); cmdT != 5*time.Minute || totalT != 15*time.Minute {
		t.Fatalf("运行后默认实例超时被污染: cmd=%s total=%s", cmdT, totalT)
	}
}

