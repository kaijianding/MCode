package mcodeui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mcode/internal/testenv"
)

// /export 接线测试：runLocalSlashCommand 必须清空草稿并调用 exportThread，
// 在注入的保存对话框配合下把会话历史写成 Markdown 文件。
func TestRunLocalSlashCommand_ExportWritesMarkdown(t *testing.T) {
	testenv.TempHome(t)

	fake := NewFakeClient()
	s := newChatState(fake)
	s.threads = []Thread{{
		ID:        "t1",
		ProjectID: "p1",
		Title:     "导出接线会话",
		Provider:  "claude",
		Model:     "claude-sonnet-4",
	}}
	s.selectedThreadID = "t1"
	fake.SetMessages("t1", []Message{
		{ID: "m1", Role: "user", Kind: "text", Text: "导出这条消息"},
		{ID: "m2", Role: "assistant", Kind: "text", Text: "收到"},
	})

	out := filepath.Join(t.TempDir(), "out.md")
	dialogCalled := make(chan struct{}, 1)
	old := saveExportDialog
	saveExportDialog = func(defaultPath string) (string, error) {
		select {
		case dialogCalled <- struct{}{}:
		default:
		}
		return out, nil
	}
	t.Cleanup(func() { saveExportDialog = old })

	s.draft = "/export"
	if !s.runLocalSlashCommand("t1", "/export") {
		t.Fatalf("runLocalSlashCommand('/export') expected true")
	}
	s.mu.Lock()
	draft := s.draft
	s.mu.Unlock()
	if draft != "" {
		t.Fatalf("expected draft cleared after /export, got %q", draft)
	}

	// 导出在后台 goroutine 中执行：先等保存对话框被调用（同时保证注入变量已被读取）
	select {
	case <-dialogCalled:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected saveExportDialog to be called within 2s")
	}

	// 再轮询等待导出流程完成且文件非空
	var content string
	wantToast := "已导出到 " + out
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		toast := s.toastMessage
		s.mu.Unlock()
		if strings.HasPrefix(toast, "导出失败") {
			t.Fatalf("export failed unexpectedly with toast %q", toast)
		}

		data, err := os.ReadFile(out)
		if err == nil && len(data) > 0 && toast == wantToast {
			content = string(data)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected export file %s and toast %q within 2s (len=%d, toast=%q, err=%v)", out, wantToast, len(data), toast, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !strings.Contains(content, "# 导出接线会话") {
		t.Fatalf("expected markdown to contain thread title, got:\n%s", content)
	}
	if !strings.Contains(content, "导出这条消息") {
		t.Fatalf("expected markdown to contain message text, got:\n%s", content)
	}
}

// TestExportThreadAtomicNoPartialRead 验证并发读导出目标路径时不会观测到 0 字节或半写内容。
func TestExportThreadAtomicNoPartialRead(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 19, 16, 0, 0, 0, time.Local)
	thread := Thread{
		ID:        "t-atomic",
		ProjectID: "p1",
		Title:     "原子导出测试",
		Provider:  "claude",
		Model:     "claude-sonnet-4",
	}
	msgsA := []Message{
		{ID: "m1", Role: "user", Kind: "text", Text: strings.Repeat("版本A-", 2048)},
	}
	msgsB := []Message{
		{ID: "m2", Role: "assistant", Kind: "text", Text: strings.Repeat("版本B-", 2048)},
	}

	refA := filepath.Join(dir, "ref-a.md")
	refB := filepath.Join(dir, "ref-b.md")
	if err := writeThreadExport(refA, thread, msgsA, now); err != nil {
		t.Fatalf("write refA: %v", err)
	}
	if err := writeThreadExport(refB, thread, msgsB, now); err != nil {
		t.Fatalf("write refB: %v", err)
	}
	wantA, err := os.ReadFile(refA)
	if err != nil {
		t.Fatalf("read refA: %v", err)
	}
	wantB, err := os.ReadFile(refB)
	if err != nil {
		t.Fatalf("read refB: %v", err)
	}

	out := filepath.Join(dir, "export.md")
	stop := make(chan struct{})
	errCh := make(chan error, 4)
	var wg sync.WaitGroup
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				data, err := os.ReadFile(out)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					errCh <- fmt.Errorf("unexpected read error: %w", err)
					return
				}
				if len(data) == 0 {
					errCh <- fmt.Errorf("observed 0-byte export file")
					return
				}
				if !bytes.Equal(data, wantA) && !bytes.Equal(data, wantB) {
					errCh <- fmt.Errorf("observed partial export content: len=%d (want %d or %d)", len(data), len(wantA), len(wantB))
					return
				}
			}
		}()
	}

	const iterations = 20
	for i := 0; i < iterations; i++ {
		msgs := msgsA
		if i%2 == 1 {
			msgs = msgsB
		}
		if err := writeThreadExport(out, thread, msgs, now); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("writeThreadExport iteration %d: %v", i, err)
		}
	}

	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	finalData, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read final export: %v", err)
	}
	if !bytes.Equal(finalData, wantB) {
		t.Fatalf("final export content mismatch: got len=%d, want len=%d", len(finalData), len(wantB))
	}
}
