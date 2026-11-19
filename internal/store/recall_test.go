package store

import (
	"context"
	"errors"
	"testing"

	"mcode/internal/testenv"
)

func TestRecallMessage_GuardsLatestMessageAndRollsBackOnFailure(t *testing.T) {
	testenv.TempHome(t)
	ctx := context.Background()
	s := OpenForTest(t)
	if err := s.UpsertProject(ctx, Project{ID: "p", Name: "project"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateThread(ctx, Thread{ID: "t", ProjectID: "p", Title: "thread", Provider: "mock"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"older", "latest"} {
		if _, err := s.AppendMessage(ctx, Message{ID: id, ThreadID: "t", Role: "user", Text: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecallMessage(ctx, "t", "older", "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("must not remove a non-latest message: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_recall BEFORE UPDATE ON threads
		BEGIN SELECT RAISE(ABORT, 'write failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecallMessage(ctx, "t", "latest", "prior context", 0); err == nil {
		t.Fatal("failed thread update must fail take back")
	}
	messages, _, err := s.ListMessages(ctx, "t", 0, 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("failed transaction lost messages: %+v, %v", messages, err)
	}
}
