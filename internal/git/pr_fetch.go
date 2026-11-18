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
// 复用 worktree 时保留进行中的合并，否则重新合并最新 base。
func (r *Runner) PRResolveWorktree(ctx context.Context, root, path string, number int, head, base string) error {
	reused, err := r.preparePRWorktree(ctx, root, path, number, head, base)
	if err != nil {
		return err
	}
	if !reused {
		return r.mergePRBase(ctx, path, base)
	}
	state, err := r.OpState(ctx, path)
	if err != nil {
		return err
	}
	if state.Kind == OpMerge {
		return nil
	}
	return r.mergePRBase(ctx, path, base)
}

// PRCheckoutWorktree 在 path 检出 PR/MR 的 worktree：只抓取 head、检出本地分支
// mcode/pr-<N> 并设置上游，不合并 base。worktree 已存在时直接返回 nil，
// 保留进行中的合并与已有改动。
func (r *Runner) PRCheckoutWorktree(ctx context.Context, root, path string, number int, head string) error {
	_, err := r.preparePRWorktree(ctx, root, path, number, head, "")
	return err
}

// preparePRWorktree 校验分支、抓取 head（base 非空时一并抓取）、复用或新建 worktree，
// 检出本地分支 mcode/pr-<N> 并设置上游；不合并 base。
// 返回 true 表示复用了已有 worktree，调用方据此决定是否继续合并。
func (r *Runner) preparePRWorktree(ctx context.Context, root, path string, number int, head, base string) (bool, error) {
	if number <= 0 {
		return false, fmt.Errorf("无效的 PR 编号: %d", number)
	}
	if !filepath.IsAbs(path) {
		return false, fmt.Errorf("worktree path must be absolute: %s", path)
	}
	if err := r.validatePRBranch(ctx, root, head); err != nil {
		return false, err
	}
	branches := []string{head}
	if base != "" {
		if err := r.validatePRBranch(ctx, root, base); err != nil {
			return false, err
		}
		branches = append(branches, base)
	}
	for _, branch := range branches {
		if _, err := r.run(ctx, root, nil, "fetch", "origin", "+refs/heads/"+branch+":refs/remotes/origin/"+branch); err != nil {
			return false, fmt.Errorf("fetch %s: %w", branch, err)
		}
	}

	worktrees, err := r.WorktreeList(ctx, root)
	if err != nil {
		return false, err
	}
	for _, wt := range worktrees {
		if resolvePath(wt.Path) == resolvePath(path) {
			return true, nil
		}
	}

	branch := fmt.Sprintf("mcode/pr-%d", number)
	_, existsErr := r.run(ctx, root, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if existsErr == nil {
		if _, err := r.run(ctx, root, nil, "worktree", "add", "--", path, branch); err != nil {
			return false, fmt.Errorf("git worktree add failed: %w", err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, err
		}
		if err := r.WorktreeAdd(ctx, root, path, branch, "origin/"+head); err != nil {
			return false, err
		}
	}

	if err := r.SetUpstream(ctx, path, branch, "origin/"+head); err != nil {
		return false, fmt.Errorf("设置上游分支失败: %w", err)
	}
	return false, nil
}

func (r *Runner) mergePRBase(ctx context.Context, path, base string) error {
	if _, err := r.run(ctx, path, opEnv, "merge", "--no-edit", "origin/"+base); err != nil {
		state, stateErr := r.OpState(ctx, path)
		if stateErr == nil && state.Kind == OpMerge && len(state.Conflicts) > 0 {
			return nil
		}
		return fmt.Errorf("merge %s: %w", base, err)
	}
	return nil
}

// PRWorktreeKeepReason 返回阻止自动清理 PR worktree 的原因。
// worktree 还在时检查未提交改动与未完成的合并；只要本地分支 mcode/pr-<N> 存在，
// 不论 worktree 是否已被删除，都检查是否有未推送的提交。
func (r *Runner) PRWorktreeKeepReason(ctx context.Context, root, path string, forge Forge, number int) (string, error) {
	worktrees, err := r.WorktreeList(ctx, root)
	if err != nil {
		return "", err
	}
	for _, wt := range worktrees {
		if resolvePath(wt.Path) != resolvePath(path) {
			continue
		}
		out, err := r.run(ctx, path, nil, "status", "--porcelain")
		if err != nil {
			return "", err
		}
		if len(strings.TrimSpace(string(out))) > 0 {
			return "有未提交的改动", nil
		}
		state, err := r.OpState(ctx, path)
		if err != nil {
			return "", err
		}
		if state.Kind != "" {
			return "合并尚未完成", nil
		}
		break
	}

	branch := "mcode/pr-" + strconv.Itoa(number)
	if _, err := r.run(ctx, root, nil, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		if isExitCode(err, 1) {
			return "", nil
		}
		return "", err
	}
	if _, err := r.FetchPRHead(ctx, root, forge, number); err != nil {
		return "", err
	}
	out, err := r.run(ctx, root, nil, "rev-list", "--count", PRHeadRef(number)+".."+branch)
	if err != nil {
		return "", err
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return "", err
	}
	if count > 0 {
		return "有未推送的提交", nil
	}
	return "", nil
}

// PRRemoveWorktree 删除已确认安全的 PR worktree 及本地解决分支。
func (r *Runner) PRRemoveWorktree(ctx context.Context, root, path string, number int) error {
	worktrees, err := r.WorktreeList(ctx, root)
	if err != nil {
		return err
	}
	for _, wt := range worktrees {
		if resolvePath(wt.Path) == resolvePath(path) {
			if err := r.WorktreeRemove(ctx, root, path, false); err != nil {
				return err
			}
			break
		}
	}
	branch := "mcode/pr-" + strconv.Itoa(number)
	if _, err := r.run(ctx, root, nil, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		if isExitCode(err, 1) {
			return nil
		}
		return err
	}
	_, err = r.run(ctx, root, nil, "branch", "-D", "--", branch)
	return err
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
