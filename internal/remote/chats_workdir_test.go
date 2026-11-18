package remote

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mcode/internal/harness"
	"mcode/internal/remote/wire"
	"mcode/internal/store"
)

func TestChatsDayWorkDir(t *testing.T) {
	ctx := context.Background()
	hub := NewStreamHub(StreamHubOptions{Interval: 10 * time.Millisecond})
	defer hub.Close()

	backend := NewMemoryBackend(hub)
	defer backend.Close()

	mockH := &mockHarness{kind: "mock"}
	backend.RegisterHarness(mockH)

	chatsPath := t.TempDir()
	normalPath := t.TempDir()
	chatsProjID := newUUID()
	normalProjID := newUUID()
	backend.AddProject(wire.ProjectSummary{ID: chatsProjID, Name: "对话", Path: chatsPath})
	backend.AddProject(wire.ProjectSummary{ID: normalProjID, Name: "普通项目", Path: normalPath})
	backend.SetChatsProjectID(chatsProjID)

	chatsRes, err := backend.CreateThread(ctx, wire.ThreadCreateParams{ProjectID: chatsProjID, ActionID: newUUID()})
	if err != nil {
		t.Fatalf("CreateThread(chats) failed: %v", err)
	}
	normalRes, err := backend.CreateThread(ctx, wire.ThreadCreateParams{ProjectID: normalProjID, ActionID: newUUID()})
	if err != nil {
		t.Fatalf("CreateThread(normal) failed: %v", err)
	}

	wantChatsDir := filepath.Join(chatsPath, time.Now().Local().Format("2006-01-02"))
	gotChatsDir, ok := backend.ThreadWorkDir(chatsRes.ThreadID)
	if !ok || gotChatsDir != wantChatsDir {
		t.Fatalf("对话会话 ThreadWorkDir = (%q, %v), want (%q, true)", gotChatsDir, ok, wantChatsDir)
	}
	if _, err := os.Stat(wantChatsDir); !os.IsNotExist(err) {
		t.Fatalf("CreateThread 不应提前创建按日目录 %q (stat err=%v)", wantChatsDir, err)
	}

	gotNormalDir, ok := backend.ThreadWorkDir(normalRes.ThreadID)
	if !ok || gotNormalDir != normalPath {
		t.Fatalf("普通会话 ThreadWorkDir = (%q, %v), want (%q, true)", gotNormalDir, ok, normalPath)
	}

	t.Run("RunCreatesDayDirAndReportsMkdirFailure", func(t *testing.T) {
		if _, err := backend.SendMessage(ctx, wire.SendMessageParams{
			ThreadID: chatsRes.ThreadID,
			Text:     "hello",
			ActionID: newUUID(),
		}); err != nil {
			t.Fatalf("SendMessage(chats) failed: %v", err)
		}
		summary := waitThreadIdle(t, backend, ctx, chatsRes.ThreadID)
		if summary.Attention == "failed" {
			t.Fatalf("正常对话轮次不应失败: %+v", summary)
		}
		if fi, err := os.Stat(wantChatsDir); err != nil || !fi.IsDir() {
			t.Fatalf("首轮启动后按日目录应被创建: err=%v fi=%v", err, fi)
		}
		calls := mockH.getTurnCalls()
		if len(calls) == 0 || calls[len(calls)-1].WorkDir != wantChatsDir {
			t.Fatalf("provider 收到的 WorkDir 不符: calls=%+v want=%q", calls, wantChatsDir)
		}

		// 当 chats 项目根路径被普通文件占用时，MkdirAll 失败应终止轮次、不调用 provider 且置失败态。
		blockedRoot := filepath.Join(t.TempDir(), "blocked-chats")
		if err := os.WriteFile(blockedRoot, []byte("file"), 0o644); err != nil {
			t.Fatal(err)
		}
		blockedHub := NewStreamHub(StreamHubOptions{Interval: 10 * time.Millisecond})
		defer blockedHub.Close()
		blockedBackend := NewMemoryBackend(blockedHub)
		defer blockedBackend.Close()
		blockedHarness := &mockHarness{kind: "mock"}
		blockedBackend.RegisterHarness(blockedHarness)

		blockedProjID := newUUID()
		blockedBackend.AddProject(wire.ProjectSummary{ID: blockedProjID, Name: "对话", Path: blockedRoot})
		blockedBackend.SetChatsProjectID(blockedProjID)
		blockedRes, err := blockedBackend.CreateThread(ctx, wire.ThreadCreateParams{ProjectID: blockedProjID, ActionID: newUUID()})
		if err != nil {
			t.Fatalf("CreateThread(blocked) failed: %v", err)
		}
		if _, err := blockedBackend.SendMessage(ctx, wire.SendMessageParams{
			ThreadID: blockedRes.ThreadID,
			Text:     "should fail",
			ActionID: newUUID(),
		}); err != nil {
			t.Fatalf("SendMessage(blocked) failed: %v", err)
		}
		failedSummary := waitThreadIdle(t, blockedBackend, ctx, blockedRes.ThreadID)
		if failedSummary.Attention != "failed" || failedSummary.IsRunning {
			t.Fatalf("MkdirAll 失败后应结束运行并标为 failed, got %+v", failedSummary)
		}
		if gotCalls := blockedHarness.getTurnCalls(); len(gotCalls) != 0 {
			t.Fatalf("MkdirAll 失败时不应启动 provider, got %d calls", len(gotCalls))
		}
	})
}

func TestChatsDayWorkDirFromStore(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "chats.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	chatsPath := t.TempDir()
	normalPath := t.TempDir()
	chatsProjID := newUUID()
	normalProjID := newUUID()

	pastUTC := time.Date(2024, 5, 1, 12, 30, 0, 0, time.UTC)
	if err := s.UpsertProject(ctx, store.Project{
		ID:        chatsProjID,
		Name:      "对话",
		Path:      chatsPath,
		CreatedAt: pastUTC,
		UpdatedAt: pastUTC,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProject(ctx, store.Project{
		ID:        normalProjID,
		Name:      "普通",
		Path:      normalPath,
		CreatedAt: pastUTC,
		UpdatedAt: pastUTC,
	}); err != nil {
		t.Fatal(err)
	}

	chatsThreadID := newUUID()
	normalThreadID := newUUID()
	if err := s.CreateThread(ctx, store.Thread{
		ID:        chatsThreadID,
		ProjectID: chatsProjID,
		Title:     "历史对话",
		Provider:  "mock",
		CreatedAt: pastUTC,
		UpdatedAt: pastUTC,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateThread(ctx, store.Thread{
		ID:        normalThreadID,
		ProjectID: normalProjID,
		Title:     "历史普通",
		Provider:  "mock",
		CreatedAt: pastUTC,
		UpdatedAt: pastUTC,
	}); err != nil {
		t.Fatal(err)
	}

	hub := NewStreamHub(StreamHubOptions{Interval: 10 * time.Millisecond})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	if err := backend.AttachStore(ctx, s); err != nil {
		t.Fatalf("AttachStore: %v", err)
	}
	backend.SetChatsProjectID(chatsProjID)

	wantChatsDir := filepath.Join(chatsPath, pastUTC.Local().Format("2006-01-02"))
	gotChatsDir, ok := backend.ThreadWorkDir(chatsThreadID)
	if !ok || gotChatsDir != wantChatsDir {
		t.Fatalf("从 store 恢复的对话会话 ThreadWorkDir = (%q, %v), want (%q, true)", gotChatsDir, ok, wantChatsDir)
	}
	gotNormalDir, ok := backend.ThreadWorkDir(normalThreadID)
	if !ok || gotNormalDir != normalPath {
		t.Fatalf("从 store 恢复的普通会话 ThreadWorkDir = (%q, %v), want (%q, true)", gotNormalDir, ok, normalPath)
	}
}

func TestListProjectsKind(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "projects_kind.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	hub := NewStreamHub(StreamHubOptions{Interval: 10 * time.Millisecond})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	backend.RegisterHarness(&mockHarness{kind: harness.Kind("mock")})
	if err := backend.AttachStore(ctx, s); err != nil {
		t.Fatalf("AttachStore: %v", err)
	}

	chatsProjID := newUUID()
	stewardProjID := newUUID()
	normalProjID := newUUID()

	backend.AddProject(wire.ProjectSummary{ID: chatsProjID, Name: "对话", Path: t.TempDir()})
	backend.AddProject(wire.ProjectSummary{ID: stewardProjID, Name: "总管", Path: t.TempDir()})
	backend.AddProject(wire.ProjectSummary{ID: normalProjID, Name: "普通项目", Path: t.TempDir()})

	backend.SetChatsProjectID(chatsProjID)
	if _, err := backend.EnsureStewardFeedThread(ctx, stewardProjID); err != nil {
		t.Fatalf("EnsureStewardFeedThread: %v", err)
	}

	list, err := backend.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list.Projects) != 3 {
		t.Fatalf("ListProjects 返回 %d 个项目, want 3", len(list.Projects))
	}

	kinds := make(map[string]string, len(list.Projects))
	for _, p := range list.Projects {
		kinds[p.ID] = p.Kind
	}
	if kinds[chatsProjID] != "chats" {
		t.Fatalf("chats 项目 Kind = %q, want %q", kinds[chatsProjID], "chats")
	}
	if kinds[stewardProjID] != "steward" {
		t.Fatalf("steward 项目 Kind = %q, want %q", kinds[stewardProjID], "steward")
	}
	if kinds[normalProjID] != "" {
		t.Fatalf("普通项目 Kind = %q, want empty", kinds[normalProjID])
	}
}
