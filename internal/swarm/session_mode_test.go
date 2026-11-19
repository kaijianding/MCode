package swarm

import (
	"context"
	"testing"

	"mcode/internal/kanban"
)

// bee 会话无人值守：无论档位是否只读，都必须以 build 交互模式启动，不继承 lead 的 plan。
func TestBeeSessionStartsInBuildMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runner, dir := initTestRepo(t)

	driver := newFakeDriver()
	var started []kanban.SessionRequest
	driver.onStart = func(req kanban.SessionRequest) {
		started = append(started, req)
	}

	mgr := NewManager(Options{Git: runner, Driver: driver, WorktreeDir: t.TempDir()})
	mgr.SetSettingsFunc(func() Settings {
		return Settings{Enabled: true, AutoDelegate: true, WorkspaceMode: "shared"}
	})

	_, err := mgr.Dispatch(ctx, DispatchRequest{
		LeadThreadID: "lead-build-mode",
		ProjectID:    "proj-1",
		LeadWorkDir:  dir,
		Bees: []BeeSpec{
			{Name: "Normal", Task: "normal task", Prompt: "P1"},
			{Name: "ReadOnly", Task: "readonly task", Prompt: "P2", ReadOnly: true},
		},
	})
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	if len(started) != 2 {
		t.Fatalf("expected 2 started requests, got %d", len(started))
	}
	for i, req := range started {
		if req.InteractionMode != "build" {
			t.Errorf("bee %d 会话必须以 build 交互模式启动: %q", i, req.InteractionMode)
		}
	}
}
