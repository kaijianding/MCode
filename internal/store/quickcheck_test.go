package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestOpenSkipsSyncQuickCheck 验证常规启动不同步执行 quick_check，标记文件存在时才同步检查并在通过后删除标记。
func TestOpenSkipsSyncQuickCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skip_sync.db")

	var syncCalls int
	origRun := runQuickCheck
	t.Cleanup(func() { runQuickCheck = origRun })
	runQuickCheck = func(ctx context.Context, db *sql.DB) error {
		syncCalls++
		return scanQuickCheck(ctx, db)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if syncCalls != 0 {
		t.Fatalf("常规启动不应同步执行 quick_check，实际调用 %d 次", syncCalls)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 标记文件存在时，下次打开应同步检查并在通过后删除标记
	if err := os.WriteFile(quickCheckMarkerPath(path), []byte("pending\n"), 0o644); err != nil {
		t.Fatalf("写入标记文件失败: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("带标记 Open 失败: %v", err)
	}
	if syncCalls != 1 {
		t.Fatalf("标记文件存在时应同步执行一次 quick_check，实际调用 %d 次", syncCalls)
	}
	if quickCheckMarkerExists(path) {
		t.Fatalf("同步检查通过后应删除标记文件")
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
}

// TestOpenQuarantinesNonDatabaseFile 验证文件头损坏（不是数据库）时仍会隔离旧文件并自愈重建。
func TestOpenQuarantinesNonDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database at all"), 0o644); err != nil {
		t.Fatalf("写入损坏文件失败: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("损坏文件应隔离并自愈重建，Open 返回错误: %v", err)
	}
	defer s.Close()
	if s.RecoveredBackupPath() == "" {
		t.Fatalf("期望记录自愈备份路径")
	}
	matches, globErr := filepath.Glob(path + ".corrupt-*")
	if globErr != nil {
		t.Fatalf("列举隔离文件失败: %v", globErr)
	}
	if len(matches) != 1 {
		t.Fatalf("期望存在 1 个隔离备份文件，实际: %v", matches)
	}
}

// TestBackgroundQuickCheckWritesMarker 验证后台检查失败时写入标记文件，让下次启动走同步检查与自愈。
func TestBackgroundQuickCheckWritesMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bg_fail.db")

	origBg := backgroundQuickCheck
	t.Cleanup(func() { backgroundQuickCheck = origBg })
	checked := make(chan struct{})
	backgroundQuickCheck = func(ctx context.Context, filePath string) error {
		defer close(checked)
		return errors.New("quick_check: page 3 is malformed (corrupt)")
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	<-checked

	deadline := time.Now().Add(2 * time.Second)
	for !quickCheckMarkerExists(path) {
		if time.Now().After(deadline) {
			_ = s.Close()
			t.Fatalf("后台检查失败后应写入标记文件 %s", quickCheckMarkerPath(path))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
}

// TestOpenOptionsFastTestMode 验证 OpenOptions 的快速测试选项跳过后台 quick_check 并关闭 synchronous，
// 同时保持默认 Open 的后台 quick_check 与默认 synchronous 行为不变。
func TestOpenOptionsFastTestMode(t *testing.T) {
	ctx := context.Background()
	origBg := backgroundQuickCheck
	t.Cleanup(func() { backgroundQuickCheck = origBg })

	var bgCalls atomic.Int32
	var bgDoneOnce sync.Once
	bgDone := make(chan struct{})
	backgroundQuickCheck = func(ctx context.Context, filePath string) error {
		bgCalls.Add(1)
		defer bgDoneOnce.Do(func() { close(bgDone) })
		return origBg(ctx, filePath)
	}

	fastPath := filepath.Join(t.TempDir(), "fast.db")
	sFast, err := OpenWithOptions(fastPath, OpenOptions{
		SkipQuickCheck: true,
		FastSync:       true,
	})
	if err != nil {
		t.Fatalf("OpenWithOptions 失败: %v", err)
	}
	if sFast.bgCheckDone != nil {
		_ = sFast.Close()
		t.Fatalf("SkipQuickCheck=true 不应启动后台 quick_check")
	}

	var fastSync int
	if err := sFast.db.QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&fastSync); err != nil {
		_ = sFast.Close()
		t.Fatalf("读取 fast synchronous 失败: %v", err)
	}
	if fastSync != 0 {
		_ = sFast.Close()
		t.Fatalf("FastSync=true 期望 synchronous=0，实际为 %d", fastSync)
	}

	now := time.Now().UTC()
	if err := sFast.UpsertProject(ctx, Project{ID: "p1", Name: "P1", Path: "/p1", CreatedAt: now, UpdatedAt: now}); err != nil {
		_ = sFast.Close()
		t.Fatalf("UpsertProject 失败: %v", err)
	}
	if err := sFast.CreateThread(ctx, Thread{ID: "t1", ProjectID: "p1", Title: "T1", Provider: "test", CreatedAt: now, UpdatedAt: now}); err != nil {
		_ = sFast.Close()
		t.Fatalf("CreateThread 失败: %v", err)
	}

	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 5 {
				id := fmt.Sprintf("m-%d-%d", g, i)
				if _, err := sFast.AppendMessage(ctx, Message{
					ID: id, ThreadID: "t1", Role: "user", Kind: "text", Text: id, TurnIndex: i, CreatedAt: now,
				}); err != nil {
					t.Errorf("并发 AppendMessage %s 失败: %v", id, err)
					return
				}
				if _, _, err := sFast.ListMessages(ctx, "t1", 0, 20); err != nil {
					t.Errorf("并发 ListMessages 失败: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
	if err := sFast.Close(); err != nil {
		t.Fatalf("关闭 sFast 失败: %v", err)
	}
	if got := bgCalls.Load(); got != 0 {
		t.Fatalf("SkipQuickCheck=true 期望后台 quick_check 调用 0 次，实际 %d 次", got)
	}

	// 验证 OpenForTest 快捷封装同样应用了快速测试选项。
	sHelper := OpenForTest(t)
	var helperSync int
	if err := sHelper.db.QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&helperSync); err != nil {
		t.Fatalf("读取 OpenForTest synchronous 失败: %v", err)
	}
	if helperSync != 0 || sHelper.bgCheckDone != nil {
		t.Fatalf("OpenForTest 未应用快速选项: sync=%d bgCheckDone=%v", helperSync, sHelper.bgCheckDone != nil)
	}

	// 验证默认 Open 保持原后台 quick_check 与默认 synchronous 行为不变。
	defaultPath := filepath.Join(t.TempDir(), "default.db")
	sDefault, err := Open(defaultPath)
	if err != nil {
		t.Fatalf("默认 Open 失败: %v", err)
	}
	defer func() {
		if err := sDefault.Close(); err != nil {
			t.Errorf("关闭 sDefault 失败: %v", err)
		}
	}()

	var defaultSync int
	if err := sDefault.db.QueryRowContext(ctx, "PRAGMA synchronous;").Scan(&defaultSync); err != nil {
		t.Fatalf("读取默认 synchronous 失败: %v", err)
	}
	if defaultSync == 0 {
		t.Fatalf("默认 Open 不应关闭 synchronous，实际为 %d", defaultSync)
	}

	select {
	case <-bgDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("默认 Open 未在超时前触发后台 quick_check")
	}
	if got := bgCalls.Load(); got != 1 {
		t.Fatalf("默认 Open 期望后台 quick_check 调用 1 次，实际 %d 次", got)
	}
}
