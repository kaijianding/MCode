package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolOutputBytesMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	if err := s.UpsertProject(ctx, Project{ID: "p", Name: "p", Path: t.TempDir(), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateThread(ctx, Thread{ID: "t", ProjectID: "p", Title: "t", Provider: "mock", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	m, err := s.AppendMessage(ctx, Message{ID: "old", ThreadID: "t", Role: "tool", Text: "preview", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE messages DROP COLUMN output_bytes"); err != nil {
		t.Fatal(err)
	}
	version := -1
	for i, script := range migrations {
		if strings.Contains(script, "ALTER TABLE messages ADD COLUMN output_bytes") {
			version = i
			break
		}
	}
	if version < 0 {
		t.Fatal("output_bytes migration missing")
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, err := s.GetMessage(ctx, m.ID)
	if err != nil || old.OutputBytes != 0 {
		t.Fatalf("old message=%+v err=%v", old, err)
	}
	old.OutputBytes = 6003
	if err := s.UpdateMessage(ctx, old); err != nil {
		t.Fatal(err)
	}
	m.ID, m.OutputBytes = "new", 9000
	if _, err := s.AppendMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int64{"old": 6003, "new": 9000} {
		got, err := s.GetMessage(ctx, id)
		if err != nil || got.OutputBytes != want {
			t.Fatalf("message %s=%+v want bytes=%d err=%v", id, got, want, err)
		}
	}
	msgs, _, err := s.ListMessages(ctx, "t", 0, 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("messages=%+v err=%v", msgs, err)
	}
	payload, _, err := encodeThreadArchive(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO thread_archives (thread_id, archived_at, message_count, first_seq, last_seq, encoding, payload, raw_bytes) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "t", formatTime(now), len(msgs), msgs[0].Seq, msgs[1].Seq, threadArchiveEncoding, payload, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM messages WHERE thread_id = 't'"); err != nil {
		t.Fatal(err)
	}
	archived, found, err := loadThreadArchive(ctx, s.db, "t")
	if err != nil || !found || len(archived) != 2 || archived[0].OutputBytes != 6003 || archived[1].OutputBytes != 9000 {
		t.Fatalf("archive=%+v found=%v err=%v", archived, found, err)
	}
	if err := s.HydrateThread(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMessage(ctx, "new")
	if err != nil || got.OutputBytes != 9000 {
		t.Fatalf("hydrated=%+v err=%v", got, err)
	}
}
