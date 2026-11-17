package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FetchPRHead 将 PR/MR 头提交抓取到仅供 mcode 使用的私有 ref。
func (r *Runner) FetchPRHead(ctx context.Context, root string, forge Forge, number int) (string, error) {
	if number <= 0 {
		return "", fmt.Errorf("无效的 PR 编号: %d", number)
	}
	var source string
	switch forge {
	case ForgeGitHub:
		source = "refs/pull/" + strconv.Itoa(number) + "/head"
	case ForgeGitLab:
		source = "refs/merge-requests/" + strconv.Itoa(number) + "/head"
	default:
		return "", ErrForgeUnsupported
	}
	ref := PRHeadRef(number)
	if _, err := r.run(ctx, root, nil, "fetch", "origin", "+"+source+":"+ref); err != nil {
		return "", fmt.Errorf("fetch PR/MR head: %w", err)
	}
	return r.ReadPRHeadRef(ctx, root, number)
}

// PRHeadRef 返回 PR/MR 在本地的私有审阅引用。
func PRHeadRef(number int) string { return "refs/mcode/pr/" + strconv.Itoa(number) }

// PRBaseRef 返回 PR/MR merge-base 的私有审阅引用。
func PRBaseRef(number int) string { return "refs/mcode/pr-base/" + strconv.Itoa(number) }

// SavePRBase 保存 PR/MR 的 merge-base。
func (r *Runner) SavePRBase(ctx context.Context, root string, number int, commit string) error {
	if number <= 0 {
		return fmt.Errorf("无效的 PR 编号: %d", number)
	}
	_, err := r.run(ctx, root, nil, "update-ref", PRBaseRef(number), commit)
	return err
}

// ReadPRBaseRef 读取已保存的 PR/MR merge-base。
func (r *Runner) ReadPRBaseRef(ctx context.Context, root string, number int) (string, error) {
	if number <= 0 {
		return "", fmt.Errorf("无效的 PR 编号: %d", number)
	}
	out, err := r.run(ctx, root, nil, "rev-parse", "--verify", PRBaseRef(number)+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ReadPRHeadRef 读取已抓取的 PR/MR head；ref 不存在时返回 git 错误。
func (r *Runner) ReadPRHeadRef(ctx context.Context, root string, number int) (string, error) {
	if number <= 0 {
		return "", fmt.Errorf("无效的 PR 编号: %d", number)
	}
	out, err := r.run(ctx, root, nil, "rev-parse", "--verify", PRHeadRef(number)+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// PRConflicts 计算 PR/MR head 与目标分支之间的冲突文件；没有冲突时返回空切片。
// base 是目标分支名（如 main），只能是分支名，不能是任意 rev。
func (r *Runner) PRConflicts(ctx context.Context, root string, forge Forge, number int, base string) ([]string, error) {
	if err := r.validatePRBranch(ctx, root, base); err != nil {
		return nil, err
	}
	head, err := r.FetchPRHead(ctx, root, forge, number)
	if err != nil {
		return nil, err
	}
	if _, err := r.run(ctx, root, nil, "fetch", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base); err != nil {
		return nil, fmt.Errorf("fetch PR base: %w", err)
	}
	baseRef := "origin/" + base
	mergeBase, err := r.run(ctx, root, nil, "merge-base", head, baseRef)
	if err != nil {
		return nil, fmt.Errorf("计算 PR merge-base: %w", err)
	}
	baseSHA, err := r.run(ctx, root, nil, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("解析 PR base: %w", err)
	}
	result, err := r.MergeTreeOnto(ctx, root, strings.TrimSpace(string(mergeBase)), strings.TrimSpace(string(baseSHA)), head)
	if err != nil {
		return nil, err
	}
	return result.Conflicts, nil
}

// PRResolveWorktree 在 path 准备 PR/MR 的冲突解决 worktree：
// 抓取 head 与 base、检出本地分支 mcode/pr-<N>、设置上游并合并 base。
// 合并产生冲突是预期结果：只要 OpState 显示 merge 进行中且有冲突就返回 nil。
// path 已是本仓库的 worktree 时直接返回 nil。
func (r *Runner) PRResolveWorktree(ctx context.Context, root, path string, number int, head, base string) error {
	if number <= 0 {
		return fmt.Errorf("无效的 PR 编号: %d", number)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("worktree path must be absolute: %s", path)
	}
	if err := r.validatePRBranch(ctx, root, head); err != nil {
		return err
	}
	if err := r.validatePRBranch(ctx, root, base); err != nil {
		return err
	}
	for _, branch := range []string{head, base} {
		if _, err := r.run(ctx, root, nil, "fetch", "origin", "+refs/heads/"+branch+":refs/remotes/origin/"+branch); err != nil {
			return fmt.Errorf("fetch %s: %w", branch, err)
		}
	}

	worktrees, err := r.WorktreeList(ctx, root)
	if err != nil {
		return err
	}
	for _, wt := range worktrees {
		if resolvePath(wt.Path) == resolvePath(path) {
			return nil
		}
	}

	branch := fmt.Sprintf("mcode/pr-%d", number)
	_, existsErr := r.run(ctx, root, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if existsErr == nil {
		if _, err := r.run(ctx, root, nil, "worktree", "add", "--", path, branch); err != nil {
			return fmt.Errorf("git worktree add failed: %w", err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := r.WorktreeAdd(ctx, root, path, branch, "origin/"+head); err != nil {
			return err
		}
	}

	if err := r.SetUpstream(ctx, path, branch, "origin/"+head); err != nil {
		return fmt.Errorf("设置上游分支失败: %w", err)
	}

	if _, err := r.run(ctx, path, opEnv, "merge", "--no-edit", "origin/"+base); err != nil {
		state, stateErr := r.OpState(ctx, path)
		if stateErr == nil && state.Kind == OpMerge && len(state.Conflicts) > 0 {
			return nil
		}
		return fmt.Errorf("merge %s: %w", base, err)
	}
	return nil
}

// validatePRBranch 校验来自 forge CLI 的分支名，拒绝空值、以 '-' 开头的值
// 以及 git 不接受的引用名。
func (r *Runner) validatePRBranch(ctx context.Context, root, branch string) error {
	if err := validBranchArg(branch); err != nil {
		return err
	}
	if _, err := r.run(ctx, root, nil, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("无效的分支名: %q", branch)
	}
	return nil
}

// ReviewPRBase 抓取目标分支并返回其与 PR head 的 merge-base 提交。
func (r *Runner) ReviewPRBase(ctx context.Context, root, baseBranch, head string) (string, error) {
	if baseBranch == "" || head == "" {
		return "", fmt.Errorf("PR base 或 head 为空")
	}
	if _, err := r.run(ctx, root, nil, "fetch", "origin", baseBranch); err != nil {
		return "", fmt.Errorf("fetch PR base: %w", err)
	}
	out, err := r.run(ctx, root, nil, "merge-base", "origin/"+baseBranch, head)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
