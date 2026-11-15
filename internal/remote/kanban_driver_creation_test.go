package remote

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mcode/internal/harness"
	"mcode/internal/kanban"
	"mcode/internal/remote/wire"
	"mcode/internal/store"
)

func TestStartSessionPublishesInitializedChild(t *testing.T) {
	ctx := context.Background()
	hub := NewStreamHub(StreamHubOptions{Interval: time.Hour})
	defer hub.Close()
	b := NewMemoryBackend(hub)
	defer b.Close()
	b.SetHarness(&mockHarness{kind: "claude"}, t.TempDir(), harness.ModeFullAccess)
	st, err := store.Open(filepath.Join(t.TempDir(), "thread.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := b.AttachStore(ctx, st); err != nil {
		t.Fatal(err)
	}
	b.AddProject(wire.ProjectSummary{ID: "p1", Path: t.TempDir()})
	lead, err := b.CreateThread(ctx, wire.ThreadCreateParams{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}

	type publication struct {
		summary wire.ThreadSummary
		stored  store.Thread
		err     error
	}
	var mu sync.Mutex
	first := make(map[string]publication)
	remove := hub.AddObserver(func(id, method string, params any) {
		summary, ok := params.(wire.ThreadSummary)
		if method != "thread.summary" || !ok || id == lead.ThreadID {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if _, exists := first[id]; !exists {
			stored, err := st.GetThread(ctx, id)
			first[id] = publication{summary: summary, stored: stored, err: err}
		}
	})
	defer remove()

	const title = "子代理任务"
	id, err := b.StartSession(ctx, kanban.SessionRequest{ProjectID: "p1", LeadThreadID: lead.ThreadID, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got, ok := first[id]
	mu.Unlock()
	if !ok {
		t.Fatal("未收到子会话摘要")
	}
	if derefString(got.summary.ParentThreadID) != lead.ThreadID || !got.summary.IsHelper || got.summary.Title != title {
		t.Fatalf("首次发布的摘要不正确: %+v", got.summary)
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.stored.ParentThreadID != lead.ThreadID || !got.stored.IsHelper || got.stored.Title != title {
		t.Fatalf("首次发布时的持久化线程不正确: %+v", got.stored)
	}
}
