package mcodeui

import (
	"context"

	"mcode/internal/git"
)

// ReviewSnapshot 包含固定版本的文件列表，以及按需获取的完整文本。
type ReviewSnapshot struct {
	Scope        string
	TurnID       string
	Number       int
	BaseRevision string
	HeadRevision string
	Running      bool
	Unavailable  string
	Files        []ReviewFile
	File         *ReviewFileContent
}

// ReviewFile 是审阅文件的元数据，大小单位为字节。
type ReviewFile struct {
	Path     string
	OrigPath string
	Status   string
	Language string
	OldSize  int64
	NewSize  int64
	OldHash  string
	NewHash  string
	Binary   bool
	TooLarge bool
	Missing  bool
}

// ReviewFileContent 的不可用标记为真时不包含正文。
type ReviewFileContent struct {
	Path     string
	OldText  string
	NewText  string
	OldHash  string
	NewHash  string
	Binary   bool
	TooLarge bool
	Missing  bool
}

// reviewClient 是可选能力，旧 Client 实现不必提供审阅接口。
type reviewClient interface {
	ReviewGet(ctx context.Context, threadID, scope, turnID, path string) (ReviewSnapshot, error)
}

type reviewPRClient interface {
	ReviewGetPR(ctx context.Context, threadID string, number int, path string) (ReviewSnapshot, error)
	GitPRSubmitReview(ctx context.Context, threadID string, number int, event, body string, comments []git.PRReviewComment) error
}

func getReview(ctx context.Context, client reviewClient, threadID, scope, turnID string, number int, path string) (ReviewSnapshot, error) {
	if scope == "pr" {
		if pr, ok := client.(reviewPRClient); ok {
			return pr.ReviewGetPR(ctx, threadID, number, path)
		}
		return ReviewSnapshot{Scope: scope, Number: number, Unavailable: "PR 审阅不可用"}, nil
	}
	return client.ReviewGet(ctx, threadID, scope, turnID, path)
}

type ReviewRevertHunkParams struct {
	ThreadID, Scope, TurnID, Path string
	BaseRevision, HeadRevision    string
	StartLine, LineCount          int
	HunkHash, ExpectedFileHash    string
}

type ReviewRevertHunkResult struct{ FileHash string }

type reviewHunkReverter interface {
	ReviewRevertHunk(context.Context, ReviewRevertHunkParams) (ReviewRevertHunkResult, error)
}
