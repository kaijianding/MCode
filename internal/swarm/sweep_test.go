package swarm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"mcode/internal/git"
	"mcode/internal/testenv"
)

func TestSweepDeletesUnreferencedSwarmBranches(t *testing.T) {
	testenv.TempHome(t)
	runner, repo := initTestRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, name := range []string{"mcode/swarm/x/0", "mcode/swarm/x/1", "mcode/swarm-x-abc", "feature"} {
		cmd := exec.CommandContext(ctx, runner.Bin, "branch", name)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create branch %s: %v: %s", name, err, out)
		}
	}
	checkedOut := filepath.Join(t.TempDir(), "checked-out")
	stale := filepath.Join(t.TempDir(), "stale")
	if err := runner.WorktreeAdd(ctx, repo, checkedOut, "mcode/swarm/x/2", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := runner.WorktreeAdd(ctx, repo, stale, "", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Options{Git: runner})
	m.leads["lead"] = &leadState{leadWorkDir: repo, bees: []Bee{{Branch: "mcode/swarm/x/1"}}}
	removed, err := m.SweepOrphanCopies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("unexpected removed directories: %v", removed)
	}
	branches, err := runner.Branches(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	remaining := make(map[string]bool)
	for _, branch := range branches {
		remaining[branch.Name] = true
	}
	if remaining["mcode/swarm/x/0"] {
		t.Error("unreferenced swarm branch remains")
	}
	for _, name := range []string{"mcode/swarm/x/1", "mcode/swarm/x/2", "mcode/swarm-x-abc", "feature"} {
		if !remaining[name] {
			t.Errorf("branch %s was deleted", name)
		}
	}
	worktrees, err := runner.WorktreeList(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, worktree := range worktrees {
		if sweepPathReferenced(sweepPathSet([]string{stale}), worktree.Path) {
			t.Errorf("stale worktree remains registered: %s", worktree.Path)
		}
	}
}

// 被 bee 引用的 worktree 保留，未被引用的孤儿副本经 git worktree remove 清理。
func TestManager_SweepOrphanCopies(t *testing.T) {
	testenv.TempHome(t)

	runner, repoDir := initTestRepo(t)
	wtRoot := t.TempDir()
	mgr := NewManager(Options{Git: runner, WorktreeDir: wtRoot})

	ctx := context.Background()
	referenced := filepath.Join(wtRoot, "lead-1-0")
	orphan := filepath.Join(wtRoot, "lead-1-1")
	if err := runner.WorktreeAdd(ctx, repoDir, referenced, "mcode/test-referenced", "HEAD"); err != nil {
		t.Fatalf("worktree add referenced: %v", err)
	}
	if err := runner.WorktreeAdd(ctx, repoDir, orphan, "mcode/test-orphan", "HEAD"); err != nil {
		t.Fatalf("worktree add orphan: %v", err)
	}

	mgr.mu.Lock()
	mgr.leads["lead-1"] = &leadState{
		leadThreadID: "lead-1",
		bees:         []Bee{{ThreadID: "bee-1", WorktreePath: referenced}},
	}
	mgr.mu.Unlock()

	removed, err := mgr.SweepOrphanCopies(ctx)
	if err != nil {
		t.Fatalf("SweepOrphanCopies returned error: %v", err)
	}
	if len(removed) != 1 || removed[0] != orphan {
		t.Fatalf("expected only the orphan dir removed, got %v", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("expected orphan dir removed, stat err: %v", err)
	}
	if _, err := os.Stat(referenced); err != nil {
		t.Errorf("expected referenced worktree kept, stat err: %v", err)
	}
}

// 非 git worktree 的孤儿目录直接删除；startingWorkDirs 中登记的目录保留。
func TestManager_SweepOrphanCopies_PlainDirectory(t *testing.T) {
	testenv.TempHome(t)

	wtRoot := t.TempDir()
	mgr := NewManager(Options{Git: git.NewRunner("git", 4), WorktreeDir: wtRoot})

	keep := filepath.Join(wtRoot, "keep-0")
	orphan := filepath.Join(wtRoot, "orphan-1")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatalf("mkdir keep: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(orphan, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}

	mgr.mu.Lock()
	mgr.startingWorkDirs[filepath.Clean(keep)] = workDirOwner{leadThreadID: "lead-1"}
	mgr.mu.Unlock()

	removed, err := mgr.SweepOrphanCopies(context.Background())
	if err != nil {
		t.Fatalf("SweepOrphanCopies returned error: %v", err)
	}
	if len(removed) != 1 || removed[0] != orphan {
		t.Fatalf("expected only the orphan dir removed, got %v", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("expected plain orphan dir removed, stat err: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("expected starting work dir kept, stat err: %v", err)
	}
}

// WorktreeDir 为空或不存在时返回 nil, nil。
func TestManager_SweepOrphanCopies_MissingWorktreeDir(t *testing.T) {
	testenv.TempHome(t)

	mgr := NewManager(Options{WorktreeDir: filepath.Join(t.TempDir(), "missing")})
	removed, err := mgr.SweepOrphanCopies(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if removed != nil {
		t.Fatalf("expected nil removed list, got %v", removed)
	}

	empty := NewManager(Options{})
	removed, err = empty.SweepOrphanCopies(context.Background())
	if err != nil || removed != nil {
		t.Fatalf("expected nil, nil for empty WorktreeDir, got %v, %v", removed, err)
	}
}

func TestManager_SweepOrphanCopies_Project(t *testing.T) {
	t.Parallel()
	runner, repo := initTestRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := runner.RepoRoot(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(repo, ".mcode", "worktrees", "swarm")
	referenced := filepath.Join(root, "referenced")
	orphan := filepath.Join(root, "orphan")
	starting := filepath.Join(root, "starting")
	for _, copy := range []struct{ path, branch string }{
		{referenced, "mcode/test-project-referenced"},
		{orphan, "mcode/test-project-orphan"},
	} {
		if err := runner.WorktreeAdd(ctx, repo, copy.path, copy.branch, "HEAD"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(starting, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, central := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		t.Run(central, func(t *testing.T) {
			// 两个 lead 指向同一仓库；默认集中模式也要清理之前创建的项目内副本。
			m := NewManager(Options{Git: runner, WorktreeDir: central})
			m.leads["lead"] = &leadState{leadWorkDir: repo, bees: []Bee{{WorktreePath: referenced}}}
			m.leads["nested"] = &leadState{leadWorkDir: nested}
			m.leads["non-git"] = &leadState{leadWorkDir: t.TempDir()}
			m.startingWorkDirs[starting] = workDirOwner{leadThreadID: "lead"}
			if err := os.MkdirAll(orphan, 0o755); err != nil {
				t.Fatal(err)
			}
			removed, err := m.SweepOrphanCopies(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(removed) != 1 || !sweepPathReferenced(sweepPathSet([]string{orphan}), removed[0]) {
				t.Fatalf("应仅清理项目内孤儿副本：%v", removed)
			}
			for _, keep := range []string{referenced, starting} {
				if _, err := os.Stat(keep); err != nil {
					t.Errorf("受保护的副本被删除 %q：%v", keep, err)
				}
			}
			if _, err := os.Stat(orphan); !os.IsNotExist(err) {
				t.Errorf("孤儿副本未删除：%v", err)
			}
		})
	}
}
