package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"mcode/internal/git"
)

// GitPRList 列出会话所在仓库开放的 PR/MR，limit<=0 时由 git 层取默认条数。
// 与贮藏列表一样，仓库根目录由 gitStashTarget 定位（gh/glab 需要在仓库内执行）。
func (c *Client) GitPRList(ctx context.Context, threadID string, limit int) ([]git.PRSummary, error) {
	runner, root, err := c.gitStashTarget(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return runner.PRList(ctx, root, limit)
}

// GitPRMerge 按指定方式合入 PR/MR，并删除源分支。
func (c *Client) GitPRMerge(ctx context.Context, threadID string, number int, method string) error {
	if method == "" {
		return errors.New("合并方式不能为空")
	}
	if number <= 0 {
		return fmt.Errorf("无效的 PR 编号: %d", number)
	}
	runner, root, err := c.gitStashTarget(ctx, threadID)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	remoteURL, err := runner.RemoteURL(callCtx, root)
	if err != nil {
		return err
	}
	return git.NewForgeClient(nil).MergePullRequest(callCtx, root, git.DetectForge(remoteURL), strconv.Itoa(number), method)
}

// GitPRReview 提交 PR/MR 审查。
func (c *Client) GitPRReview(ctx context.Context, threadID string, number int, event, body string) error {
	runner, root, err := c.gitStashTarget(ctx, threadID)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	remoteURL, err := runner.RemoteURL(callCtx, root)
	if err != nil {
		return err
	}
	return git.NewForgeClient(nil).ReviewPullRequest(callCtx, root, git.DetectForge(remoteURL), number, event, body)
}

// GitPRDetail 读取单条 PR/MR 的详情。
func (c *Client) GitPRDetail(ctx context.Context, threadID string, number int) (git.PRDetail, error) {
	runner, root, err := c.gitStashTarget(ctx, threadID)
	if err != nil {
		return git.PRDetail{}, err
	}
	return runner.PRDetail(ctx, root, number)
}
