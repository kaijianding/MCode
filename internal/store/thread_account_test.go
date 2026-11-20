package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestThreadAccountRoundTrip 验证 Thread.Account 在 CreateThread、GetThread、ListThreads 与 UpdateThreadAccount 下正确读写。
func TestThreadAccountRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := OpenForTest(t)

	if err := s.UpsertProject(ctx, Project{ID: "p1", Name: "Project 1", Path: "/tmp/p1"}); err != nil {
		t.Fatalf("UpsertProject 失败: %v", err)
	}

	t1 := Thread{
		ID:        "t-acc-1",
		ProjectID: "p1",
		Title:     "指定账号会话",
		Provider:  "agy",
		Model:     "gemini-2.5-pro",
		Account:   "acc-work-1",
	}
	if err := s.CreateThread(ctx, t1); err != nil {
		t.Fatalf("CreateThread t1 失败: %v", err)
	}

	t2 := Thread{
		ID:        "t-acc-2",
		ProjectID: "p1",
		Title:     "自动路由会话",
		Provider:  "agy",
		Model:     "gemini-2.5-pro",
	}
	if err := s.CreateThread(ctx, t2); err != nil {
		t.Fatalf("CreateThread t2 失败: %v", err)
	}

	got1, err := s.GetThread(ctx, "t-acc-1")
	if err != nil {
		t.Fatalf("GetThread t1 失败: %v", err)
	}
	if got1.Account != "acc-work-1" {
		t.Fatalf("期望 Account=acc-work-1，实际 %q", got1.Account)
	}

	got2, err := s.GetThread(ctx, "t-acc-2")
	if err != nil {
		t.Fatalf("GetThread t2 失败: %v", err)
	}
	if got2.Account != "" {
		t.Fatalf("期望空 Account，实际 %q", got2.Account)
	}

	list, err := s.ListThreads(ctx, "p1", false)
	if err != nil {
		t.Fatalf("ListThreads 失败: %v", err)
	}
	found := false
	for _, item := range list {
		if item.ID == "t-acc-1" {
			found = true
			if item.Account != "acc-work-1" {
				t.Fatalf("ListThreads 期望 Account=acc-work-1，实际 %q", item.Account)
			}
		}
	}
	if !found {
		t.Fatal("ListThreads 未找到 t-acc-1")
	}

	if err := s.UpdateThreadAccount(ctx, "t-acc-1", "acc-work-2"); err != nil {
		t.Fatalf("UpdateThreadAccount 失败: %v", err)
	}
	updated, err := s.GetThread(ctx, "t-acc-1")
	if err != nil {
		t.Fatalf("GetThread 更新后失败: %v", err)
	}
	if updated.Account != "acc-work-2" {
		t.Fatalf("更新后期望 Account=acc-work-2，实际 %q", updated.Account)
	}

	if err := s.UpdateThreadAccount(ctx, "t-acc-1", ""); err != nil {
		t.Fatalf("UpdateThreadAccount 清空失败: %v", err)
	}
	cleared, err := s.GetThread(ctx, "t-acc-1")
	if err != nil {
		t.Fatalf("GetThread 清空后失败: %v", err)
	}
	if cleared.Account != "" {
		t.Fatalf("清空后期望 Account 为空，实际 %q", cleared.Account)
	}

	if err := s.UpdateThreadAccount(ctx, "missing", "acc-1"); err != ErrNotFound {
		t.Fatalf("不存在会话期望 ErrNotFound，实际 %v", err)
	}
}

// TestThreadAccountMigrateLegacyDB 验证旧库（无 account 列且已有存量会话）迁移后 Account 默认为空串。
func TestThreadAccountMigrateLegacyDB(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy_account.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if err := s.UpsertProject(ctx, Project{ID: "p1", Name: "Project 1", Path: "/tmp/p1"}); err != nil {
		t.Fatalf("UpsertProject 失败: %v", err)
	}
	if err := s.CreateThread(ctx, Thread{ID: "legacy-thread", ProjectID: "p1", Title: "旧库会话", Provider: "agy"}); err != nil {
		t.Fatalf("CreateThread 失败: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, "ALTER TABLE threads DROP COLUMN account;"); err != nil {
		t.Fatalf("删除 account 列失败: %v", err)
	}
	migrationIndex := 0
	for i, migration := range migrations {
		if strings.Contains(migration, "ADD COLUMN account") {
			migrationIndex = i
			break
		}
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d;", migrationIndex)); err != nil {
		t.Fatalf("回退 user_version 失败: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("关闭旧库失败: %v", err)
	}

	s, err = Open(dbPath)
	if err != nil {
		t.Fatalf("旧库迁移后重新 Open 失败: %v", err)
	}
	defer s.Close()

	loaded, err := s.GetThread(ctx, "legacy-thread")
	if err != nil {
		t.Fatalf("迁移后 GetThread 失败: %v", err)
	}
	if loaded.Account != "" {
		t.Fatalf("期望老行 Account 为空串，实际为 %q", loaded.Account)
	}
}
