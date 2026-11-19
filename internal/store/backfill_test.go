package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestBackfillSwarmLeadHelpers 验证 hydra_leads.heads_json 中的 bee 线程回填 parent_thread_id 与 is_helper。
func TestBackfillSwarmLeadHelpers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "backfill_test.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}

	if err := s.UpsertProject(ctx, Project{ID: "p1", Name: "Project 1", Path: "/tmp/p1"}); err != nil {
		t.Fatalf("UpsertProject 失败: %v", err)
	}

	leadThread := Thread{
		ID:        "lead-1",
		ProjectID: "p1",
		Title:     "主会话",
		Provider:  "codex",
	}
	if err := s.CreateThread(ctx, leadThread); err != nil {
		t.Fatalf("CreateThread lead 失败: %v", err)
	}

	// 历史 bee 会话 1：parent_thread_id 为空，is_helper 为 0
	bee1 := Thread{
		ID:             "bee-1",
		ProjectID:      "p1",
		Title:          "Aspen: 子任务 1",
		Provider:       "agy",
		RuntimeMode:    "readonly",
		ParentThreadID: "",
		IsHelper:       false,
	}
	if err := s.CreateThread(ctx, bee1); err != nil {
		t.Fatalf("CreateThread bee1 失败: %v", err)
	}

	// 历史 bee 会话 2：parent_thread_id 为空，is_helper 为 0
	bee2 := Thread{
		ID:             "bee-2",
		ProjectID:      "p1",
		Title:          "Birch: 子任务 2",
		Provider:       "agy",
		RuntimeMode:    "readonly",
		ParentThreadID: "",
		IsHelper:       false,
	}
	if err := s.CreateThread(ctx, bee2); err != nil {
		t.Fatalf("CreateThread bee2 失败: %v", err)
	}

	// 独立已有父级的会话 3：parent_thread_id 已指定，不应被覆盖
	bee3 := Thread{
		ID:             "bee-3",
		ProjectID:      "p1",
		Title:          "Cove: 已有关联",
		Provider:       "agy",
		RuntimeMode:    "readonly",
		ParentThreadID: "existing-parent",
		IsHelper:       true,
	}
	if err := s.CreateThread(ctx, bee3); err != nil {
		t.Fatalf("CreateThread bee3 失败: %v", err)
	}

	// 写入 hydra_leads 记录，关联 bee-1 与 bee-2 到 lead-1
	beesJSON := `[{"threadId":"bee-1","name":"Aspen"},{"threadId":"bee-2","name":"Birch"},{"threadId":"bee-3","name":"Cove"}]`
	rec := SwarmLeadRecord{
		LeadThreadID: "lead-1",
		Enabled:      true,
		ProjectID:    "p1",
		BeesJSON:     beesJSON,
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.SaveSwarmLead(ctx, rec); err != nil {
		t.Fatalf("SaveSwarmLead 失败: %v", err)
	}

	// 模拟旧库：把版本退回一次性回填迁移之前，再通过 Open 触发升级。
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d;", len(migrations)-1)); err != nil {
		t.Fatalf("回退 user_version 失败: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("关闭旧库失败: %v", err)
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatalf("升级旧库失败: %v", err)
	}
	defer s.Close()

	// 验证 bee-1 被回填
	got1, err := s.GetThread(ctx, "bee-1")
	if err != nil {
		t.Fatalf("GetThread bee-1 失败: %v", err)
	}
	if got1.ParentThreadID != "lead-1" || !got1.IsHelper {
		t.Errorf("bee-1 回填后 ParentThreadID=%q, IsHelper=%v, 期望 lead-1 / true", got1.ParentThreadID, got1.IsHelper)
	}

	// 验证 bee-2 被回填
	got2, err := s.GetThread(ctx, "bee-2")
	if err != nil {
		t.Fatalf("GetThread bee-2 失败: %v", err)
	}
	if got2.ParentThreadID != "lead-1" || !got2.IsHelper {
		t.Errorf("bee-2 回填后 ParentThreadID=%q, IsHelper=%v, 期望 lead-1 / true", got2.ParentThreadID, got2.IsHelper)
	}

	// 验证已有父会话的 bee-3 未被覆盖
	got3, err := s.GetThread(ctx, "bee-3")
	if err != nil {
		t.Fatalf("GetThread bee-3 失败: %v", err)
	}
	if got3.ParentThreadID != "existing-parent" {
		t.Errorf("bee-3 不应被覆盖，ParentThreadID=%q", got3.ParentThreadID)
	}

	// 验证 ListThreads 也会正确读出这两列
	list, err := s.ListThreads(ctx, "p1", false)
	if err != nil {
		t.Fatalf("ListThreads 失败: %v", err)
	}
	foundBee1 := false
	for _, th := range list {
		if th.ID == "bee-1" {
			foundBee1 = true
			if th.ParentThreadID != "lead-1" || !th.IsHelper {
				t.Errorf("ListThreads bee-1 ParentThreadID=%q, IsHelper=%v", th.ParentThreadID, th.IsHelper)
			}
		}
	}
	if !foundBee1 {
		t.Error("ListThreads 未能找到 bee-1")
	}
}

func TestBackfillSwarmLeadHelpersDuplicateAndInvalidJSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "backfill_duplicate.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProject(ctx, Project{ID: "p", Name: "p", Path: "/p"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bee", "untouched"} {
		if err := s.CreateThread(ctx, Thread{ID: id, ProjectID: "p", Title: id, Provider: "codex"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, lead := range []SwarmLeadRecord{
		{LeadThreadID: "z-first", BeesJSON: `[{"threadId":"bee"}]`},
		{LeadThreadID: "a-second", BeesJSON: `[{"threadId":"bee"}]`},
		{LeadThreadID: "invalid", BeesJSON: `[{"threadId":"untouched"}`},
	} {
		if err := s.SaveSwarmLead(ctx, lead); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d;", len(migrations)-1)); err != nil {
		t.Fatalf("回退 user_version 失败: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatalf("升级旧库失败: %v", err)
	}
	defer s.Close()
	bee, err := s.GetThread(ctx, "bee")
	if err != nil {
		t.Fatal(err)
	}
	if bee.ParentThreadID != "z-first" || !bee.IsHelper {
		t.Fatalf("bee: %+v", bee)
	}
	untouched, err := s.GetThread(ctx, "untouched")
	if err != nil {
		t.Fatal(err)
	}
	if untouched.ParentThreadID != "" || untouched.IsHelper {
		t.Fatalf("untouched: %+v", untouched)
	}
}

// TestBackfillSwarmLeadHelpersRunsOnce 验证新库不会在后续启动重复回填新写入的数据。
func TestBackfillSwarmLeadHelpersRunsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "backfill_once.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertProject(ctx, Project{ID: "p", Name: "p", Path: "/p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateThread(ctx, Thread{ID: "bee", ProjectID: "p", Title: "bee", Provider: "codex"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSwarmLead(ctx, SwarmLeadRecord{
		LeadThreadID: "lead",
		ProjectID:    "p",
		BeesJSON:     `[{"threadId":"bee"}]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bee, err := s.GetThread(ctx, "bee")
	if err != nil {
		t.Fatal(err)
	}
	if bee.ParentThreadID != "" || bee.IsHelper {
		t.Fatalf("新库重启后不应回填 bee: %+v", bee)
	}
}
