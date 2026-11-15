package mcodeui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/egoist/mygo/ui"
)

type loadErrorClient struct {
	Client
	historyCalls  int
	failHistory   bool
	failApprovals bool
}

func (c *loadErrorClient) History(ctx context.Context, tid string) ([]Message, error) {
	c.historyCalls++
	if c.failHistory {
		return nil, errors.New("history error")
	}
	return c.Client.History(ctx, tid)
}

func (c *loadErrorClient) Approvals(ctx context.Context, tid string) ([]Approval, error) {
	if c.failApprovals {
		return nil, errors.New("approval error")
	}
	return c.Client.Approvals(ctx, tid)
}

func TestTimelineLoadErrorRetry(t *testing.T) {
	for _, which := range []string{"history", "approval"} {
		t.Run(which, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				client := &loadErrorClient{Client: NewFakeClient(), failHistory: which == "history", failApprovals: which == "approval"}
				s := newChatState(client)
				defer s.close()
				s.selectThread("t1")
				synctest.Wait()
				items := s.withTimelineLoadError(nil)
				if len(items) != 1 || items[0].kind != timelineItemLoadError || !strings.Contains(items[0].msg.Text, which+" error") {
					t.Fatalf("错误行: %+v", items)
				}
				if !s.composerDockDecisionFor(true, "t1").Docked {
					t.Fatal("错误行需要显示在时间线")
				}
				tt := ui.NewTester(func(c *ui.Context) { renderTimelineLoadError(c, s, items[0]) }, 800, 300)
				for i := 0; i < 3; i++ {
					tt.Frame()
				}
				client.failHistory, client.failApprovals = false, false
				if err := tt.Click("重试"); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if client.historyCalls != 2 || len(s.withTimelineLoadError(nil)) != 0 {
					t.Fatalf("重试次数 %d，错误未清除", client.historyCalls)
				}
			})
		})
	}
}

func TestTimelineReloadShowsSkeleton(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &loadErrorClient{Client: NewFakeClient()}
		s := newChatState(client)
		defer s.close()
		s.selectThread("t1")
		synctest.Wait()
		s.selectThread("t2")
		synctest.Wait()
		s.selectThread("t1")
		if s.timelineLoadingThread != "t1" || !s.composerDockDecisionFor(true, "t1").Docked {
			t.Fatal("重新进入时应显示骨架与时间线")
		}
		synctest.Wait()
	})
}

func TestImagePreviewLoadErrorState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.png")
	_, err := loadLocalImage(path)
	if err == nil {
		t.Fatal("缺失图片应加载失败")
	}
	imagePreviewCache.Store(path, &previewImageEntry{err: err})
	defer imagePreviewCache.Delete(path)
	s := newChatState(NewFakeClient())
	defer s.close()
	s.imagePreview = &imagePreviewState{url: path}
	tt := ui.NewTester(func(c *ui.Context) { renderImagePreviewOverlay(c, s) }, 800, 600)
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if !tt.HasText("图片加载失败") {
		t.Fatalf("缺少失败状态: %v", tt.Texts())
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if s.imagePreview != nil {
		t.Fatal("关闭后预览仍存在")
	}
}

func TestCloseChatFindFocusComposer(t *testing.T) {
	s := newChatState(NewFakeClient())
	defer s.close()
	getChatFindState(s).open = true
	s.closeChatFind()
	if !s.composerFocusRequest {
		t.Fatal("关闭查找后应请求输入框焦点")
	}
}
