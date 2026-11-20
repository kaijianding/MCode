package remote

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"mcode/internal/harness"
	"mcode/internal/remote/wire"
	"mcode/internal/store"
	"mcode/internal/testenv"
)

func TestAgentSessionIDPublishedAfterSessionEvent(t *testing.T) {
	testenv.TempHome(t)
	ctx := context.Background()

	hub := NewStreamHub(StreamHubOptions{Interval: 5 * time.Millisecond})
	t.Cleanup(hub.Close)

	backend := NewMemoryBackend(hub)
	t.Cleanup(backend.Close)

	sessionEmitted := make(chan struct{})
	releaseTurn := make(chan struct{})
	h := &customRunHarness{
		kind: "old",
		runFn: func(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
			emit(harness.Event{Type: harness.EventSession, SessionID: "sess-live-1"})
			close(sessionEmitted)
			select {
			case <-releaseTurn:
			case <-ctx.Done():
				return ctx.Err()
			}
			emit(harness.Event{Type: harness.EventText, Text: "done"})
			emit(harness.Event{Type: harness.EventDone})
			return nil
		},
	}
	backend.SetHarness(h, t.TempDir(), harness.ModeAgent)

	threadID := newUUID()
	backend.AddThread(wire.ThreadSummary{ID: threadID, Title: "AgentSessionID Live", Provider: "old"}, nil)

	pushed := make(chan wire.ThreadSummary, 8)
	stopObserver := hub.AddObserver(func(id, method string, params any) {
		if id != threadID || method != "thread.summary" {
			return
		}
		if summary, ok := params.(wire.ThreadSummary); ok && summary.AgentSessionID == "sess-live-1" {
			select {
			case pushed <- summary:
			default:
			}
		}
	})
	defer stopObserver()

	if _, err := backend.SendMessage(ctx, wire.SendMessageParams{
		ThreadID: threadID,
		Text:     "hello",
		ActionID: newUUID(),
	}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	select {
	case <-sessionEmitted:
	case <-time.After(2 * time.Second):
		close(releaseTurn)
		t.Fatal("timed out waiting for EventSession")
	}

	select {
	case summary := <-pushed:
		if summary.AgentSessionID != "sess-live-1" {
			close(releaseTurn)
			t.Fatalf("pushed AgentSessionID = %q, want %q", summary.AgentSessionID, "sess-live-1")
		}
	case <-time.After(2 * time.Second):
		close(releaseTurn)
		t.Fatal("timed out waiting for thread.summary push with AgentSessionID")
	}

	close(releaseTurn)
	waitThreadIdle(t, backend, ctx, threadID)

	got, err := backend.GetThread(ctx, threadID)
	if err != nil {
		t.Fatalf("GetThread failed: %v", err)
	}
	if got.AgentSessionID != "sess-live-1" {
		t.Fatalf("GetThread AgentSessionID = %q, want %q", got.AgentSessionID, "sess-live-1")
	}

	list, err := backend.ListThreads(ctx, wire.ThreadListParams{})
	if err != nil {
		t.Fatalf("ListThreads failed: %v", err)
	}
	var found bool
	for _, th := range list.Threads {
		if th.ID == threadID {
			found = true
			if th.AgentSessionID != "sess-live-1" {
				t.Fatalf("ListThreads AgentSessionID = %q, want %q", th.AgentSessionID, "sess-live-1")
			}
		}
	}
	if !found {
		t.Fatalf("thread %s not found in ListThreads", threadID)
	}
}

func TestAgentSessionIDClearedOnProviderSwitch(t *testing.T) {
	testenv.TempHome(t)
	ctx := context.Background()

	hub := NewStreamHub(StreamHubOptions{Interval: 5 * time.Millisecond})
	t.Cleanup(hub.Close)

	st, err := store.Open(filepath.Join(t.TempDir(), "agent_session.db"))
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	backend := NewMemoryBackend(hub)
	t.Cleanup(backend.Close)

	oldHarness := &customRunHarness{
		kind: "old",
		runFn: func(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
			emit(harness.Event{Type: harness.EventSession, SessionID: "sess-old-1"})
			emit(harness.Event{Type: harness.EventText, Text: "reply"})
			emit(harness.Event{Type: harness.EventDone})
			return nil
		},
	}
	newHarness := &customRunHarness{
		kind: "new",
		runFn: func(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
			return nil
		},
	}
	backend.SetHarness(oldHarness, t.TempDir(), harness.ModeAgent)
	backend.RegisterHarness(newHarness)
	if err := backend.AttachStore(ctx, st); err != nil {
		t.Fatalf("AttachStore failed: %v", err)
	}

	projDir := t.TempDir()
	backend.AddProject(wire.ProjectSummary{ID: "p-agent-session", Path: projDir})
	threadID := newUUID()
	backend.AddThread(wire.ThreadSummary{ID: threadID, ProjectID: "p-agent-session", Title: "Provider Switch", Provider: "old"}, nil)

	if _, err := backend.SendMessage(ctx, wire.SendMessageParams{
		ThreadID: threadID,
		Text:     "first",
		ActionID: newUUID(),
	}); err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}
	waitThreadIdle(t, backend, ctx, threadID)

	before, err := backend.GetThread(ctx, threadID)
	if err != nil {
		t.Fatalf("GetThread before switch failed: %v", err)
	}
	if before.AgentSessionID != "sess-old-1" {
		t.Fatalf("GetThread before switch AgentSessionID = %q, want %q", before.AgentSessionID, "sess-old-1")
	}

	newProvider := "new"
	upRes, err := backend.UpdateThread(ctx, wire.ThreadUpdateParams{
		ThreadID: threadID,
		Provider: &newProvider,
	})
	if err != nil {
		t.Fatalf("UpdateThread failed: %v", err)
	}
	if upRes.Thread.AgentSessionID != "" {
		t.Fatalf("UpdateThread AgentSessionID = %q, want empty", upRes.Thread.AgentSessionID)
	}

	after, err := backend.GetThread(ctx, threadID)
	if err != nil {
		t.Fatalf("GetThread after switch failed: %v", err)
	}
	if after.AgentSessionID != "" {
		t.Fatalf("GetThread after switch AgentSessionID = %q, want empty", after.AgentSessionID)
	}

	list, err := backend.ListThreads(ctx, wire.ThreadListParams{})
	if err != nil {
		t.Fatalf("ListThreads failed: %v", err)
	}
	var found bool
	for _, th := range list.Threads {
		if th.ID == threadID {
			found = true
			if th.AgentSessionID != "" {
				t.Fatalf("ListThreads after switch AgentSessionID = %q, want empty", th.AgentSessionID)
			}
		}
	}
	if !found {
		t.Fatalf("thread %s not found in ListThreads", threadID)
	}
}
