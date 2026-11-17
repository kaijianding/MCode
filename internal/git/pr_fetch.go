package git

import (
	"context"
	"fmt"
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
