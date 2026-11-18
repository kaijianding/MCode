package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mcode/internal/remote"
	"mcode/internal/store"
)

func TestChatsProjectOpenCreatesAndCachesID(t *testing.T) {
	if got := ((*Client)(nil)).ChatsProjectID(); got != "" {
		t.Fatalf("nil Client 应返回空串，实际 %q", got)
	}
	if got := (&Client{}).ChatsProjectID(); got != "" {
		t.Fatalf("未初始化 Client 应返回空串，实际 %q", got)
	}

	dataDir := t.TempDir()
	workDir := t.TempDir()
	a, err := Open(Options{
		DataDir:  dataDir,
		Provider: "memory",
		WorkDir:  workDir,
		Mode:     "agent",
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer a.Close()

	chatsDir := filepath.Clean(filepath.Join(dataDir, "chats"))
	fi, err := os.Stat(chatsDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("内置对话目录应存在且为目录: %v", err)
	}

	projects, err := a.Client().Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects failed: %v", err)
	}
	var foundID string
	var count int
	for _, p := range projects {
		if filepath.Clean(p.Path) == chatsDir {
			count++
			foundID = p.ID
			if p.Name != "对话" {
				t.Fatalf("对话项目名称 = %q，期望 %q", p.Name, "对话")
			}
		}
	}
	if count != 1 || foundID == "" {
		t.Fatalf("应存在 1 个路径为 %q 的对话项目，实际 count=%d id=%q", chatsDir, count, foundID)
	}
	if got := a.Client().ChatsProjectID(); got != foundID {
		t.Fatalf("ChatsProjectID() = %q，期望 %q", got, foundID)
	}
}

func TestChatsProjectEnsureBuiltinIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := remote.NewStreamHub(remote.StreamHubOptions{})
	defer hub.Close()
	backend := remote.NewMemoryBackend(hub)
	defer backend.Close()
	if err := backend.AttachStore(ctx, db); err != nil {
		t.Fatal(err)
	}

	chatsDir := filepath.Join(dir, "chats")
	first, err := ensureBuiltinProject(ctx, backend, chatsDir, "对话")
	if err != nil || first == "" {
		t.Fatalf("首次创建对话项目失败: id=%q err=%v", first, err)
	}
	second, err := ensureBuiltinProject(ctx, backend, chatsDir, "对话")
	if err != nil {
		t.Fatalf("重复调用 ensureBuiltinProject 失败: %v", err)
	}
	if second != first {
		t.Fatalf("重复调用应返回同一 ID: %q != %q", second, first)
	}

	list, err := backend.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Projects) != 1 {
		t.Fatalf("重复调用不应新建项目，实际项目数 %d", len(list.Projects))
	}
	if list.Projects[0].Name != "对话" {
		t.Fatalf("项目名称 = %q，期望 %q", list.Projects[0].Name, "对话")
	}
}

func TestChatsProjectStewardBuiltinUnchanged(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := remote.NewStreamHub(remote.StreamHubOptions{})
	defer hub.Close()
	backend := remote.NewMemoryBackend(hub)
	defer backend.Close()
	if err := backend.AttachStore(ctx, db); err != nil {
		t.Fatal(err)
	}

	stewardDir := filepath.Clean(filepath.Join(dir, "steward"))
	id, err := ensureBuiltinProject(ctx, backend, stewardDir, "总管")
	if err != nil || id == "" {
		t.Fatalf("创建总管项目失败: id=%q err=%v", id, err)
	}
	again, err := ensureBuiltinProject(ctx, backend, stewardDir, "总管")
	if err != nil || again != id {
		t.Fatalf("重复创建总管项目失败: id=%q again=%q err=%v", id, again, err)
	}

	list, err := backend.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Projects) != 1 {
		t.Fatalf("总管项目数 = %d，期望 1", len(list.Projects))
	}
	if list.Projects[0].Name != "总管" || filepath.Clean(list.Projects[0].Path) != stewardDir {
		t.Fatalf("总管项目信息异常: %+v", list.Projects[0])
	}
}

func TestChatsProjectEnsureFailureDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chats"), []byte("not a dir"), 0644); err != nil {
		t.Fatal(err)
	}
	hub := remote.NewStreamHub(remote.StreamHubOptions{})
	defer hub.Close()
	backend := remote.NewMemoryBackend(hub)
	defer backend.Close()
	client := NewClient(backend)

	if id := ensureChatsProject(ctx, backend, client, dir); id != "" {
		t.Fatalf("目录被占用时应返回空串，实际 %q", id)
	}
	if got := client.ChatsProjectID(); got != "" {
		t.Fatalf("失败时缓存应为空串，实际 %q", got)
	}
}
