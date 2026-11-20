package remote

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mcode/internal/harness"
	"mcode/internal/providers/agy/accounts"
	"mcode/internal/remote/wire"
	"mcode/internal/store"
)

func setupThreadAccountBackend(t *testing.T) (*MemoryBackend, *store.Store, string, string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "account.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	hub := NewStreamHub(StreamHubOptions{Interval: 5 * time.Millisecond})
	t.Cleanup(func() { hub.Close() })

	b := NewMemoryBackend(hub)
	t.Cleanup(func() { b.Close() })
	if err := b.AttachStore(ctx, s); err != nil {
		t.Fatalf("AttachStore: %v", err)
	}

	mgr, err := accounts.NewManager(accounts.Options{
		DataDir:  t.TempDir(),
		HostHome: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("accounts.NewManager: %v", err)
	}
	b.SetAgyAccounts(mgr)
	b.RegisterHarness(&mockHarness{kind: "agy"})
	b.RegisterHarness(&mockHarness{kind: "codex"})

	projID := newUUID()
	b.AddProject(wire.ProjectSummary{ID: projID, Name: "p", Path: t.TempDir()})
	return b, s, projID, accounts.SystemAccountID
}

func TestThreadAccount_CreateUpdatePersistAndValidation(t *testing.T) {
	ctx := context.Background()
	b, s, projID, accID := setupThreadAccountBackend(t)

	// 1. CreateThread 带 account 成功落盘，并在重启 AttachStore 后保留。
	agyProv := "agy"
	cRes, err := b.CreateThread(ctx, wire.ThreadCreateParams{
		ProjectID: projID,
		ActionID:  newUUID(),
		Options: &wire.ComposerOptions{
			Provider: &agyProv,
			Account:  &accID,
		},
	})
	if err != nil {
		t.Fatalf("CreateThread 带 account 失败: %v", err)
	}
	th, err := b.GetThread(ctx, cRes.ThreadID)
	if err != nil {
		t.Fatalf("GetThread 失败: %v", err)
	}
	if th.Account != accID {
		t.Fatalf("期望 Account=%q，实际 %q", accID, th.Account)
	}

	// 验证落盘与重加载恢复。
	hub2 := NewStreamHub(StreamHubOptions{Interval: 5 * time.Millisecond})
	defer hub2.Close()
	b2 := NewMemoryBackend(hub2)
	defer b2.Close()
	if err := b2.AttachStore(ctx, s); err != nil {
		t.Fatalf("重载 AttachStore 失败: %v", err)
	}
	reloaded, err := b2.GetThread(ctx, cRes.ThreadID)
	if err != nil {
		t.Fatalf("重载 GetThread 失败: %v", err)
	}
	if reloaded.Account != accID {
		t.Fatalf("重载后期望 Account=%q，实际 %q", accID, reloaded.Account)
	}

	// 2. CreateThread 非 agy 供应商带 account 应报错。
	codexProv := "codex"
	_, err = b.CreateThread(ctx, wire.ThreadCreateParams{
		ProjectID: projID,
		ActionID:  newUUID(),
		Options: &wire.ComposerOptions{
			Provider: &codexProv,
			Account:  &accID,
		},
	})
	var rpcErr *wire.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != wire.InvalidParams {
		t.Fatalf("非 agy 指定 account 应返回 InvalidParams，实际 %v", err)
	}

	// 3. CreateThread 账号不存在应报错。
	badAcc := "acc-nonexistent"
	_, err = b.CreateThread(ctx, wire.ThreadCreateParams{
		ProjectID: projID,
		ActionID:  newUUID(),
		Options: &wire.ComposerOptions{
			Provider: &agyProv,
			Account:  &badAcc,
		},
	})
	if !errors.As(err, &rpcErr) || rpcErr.Code != wire.InvalidParams {
		t.Fatalf("不存在账号应返回 InvalidParams，实际 %v", err)
	}

	// 4. UpdateThread 清空与重新设置成功落盘，且不存在账号或非 agy 报错。
	emptyAcc := ""
	uRes, err := b.UpdateThread(ctx, wire.ThreadUpdateParams{
		ThreadID: cRes.ThreadID,
		Account:  &emptyAcc,
	})
	if err != nil || uRes.Thread.Account != "" {
		t.Fatalf("UpdateThread 清空失败: res=%+v err=%v", uRes, err)
	}
	uRes, err = b.UpdateThread(ctx, wire.ThreadUpdateParams{
		ThreadID: cRes.ThreadID,
		Account:  &accID,
	})
	if err != nil || uRes.Thread.Account != accID {
		t.Fatalf("UpdateThread 设置失败: res=%+v err=%v", uRes, err)
	}
	if _, err := b.UpdateThread(ctx, wire.ThreadUpdateParams{
		ThreadID: cRes.ThreadID,
		Account:  &badAcc,
	}); !errors.As(err, &rpcErr) || rpcErr.Code != wire.InvalidParams {
		t.Fatalf("UpdateThread 不存在账号应返回 InvalidParams，实际 %v", err)
	}

	codexThread, err := b.CreateThread(ctx, wire.ThreadCreateParams{
		ProjectID: projID,
		ActionID:  newUUID(),
		Options:   &wire.ComposerOptions{Provider: &codexProv},
	})
	if err != nil {
		t.Fatalf("创建 codex 会话失败: %v", err)
	}
	if _, err := b.UpdateThread(ctx, wire.ThreadUpdateParams{
		ThreadID: codexThread.ThreadID,
		Account:  &accID,
	}); !errors.As(err, &rpcErr) || rpcErr.Code != wire.InvalidParams {
		t.Fatalf("UpdateThread 非 agy 会话指定 account 应返回 InvalidParams，实际 %v", err)
	}

	// 5. 运行中拒绝 UpdateThread 修改 Account。
	b.mu.Lock()
	b.threads[cRes.ThreadID].summary.IsRunning = true
	b.mu.Unlock()
	if _, err := b.UpdateThread(ctx, wire.ThreadUpdateParams{
		ThreadID: cRes.ThreadID,
		Account:  &emptyAcc,
	}); !errors.As(err, &rpcErr) || rpcErr.Code != wire.InvalidParams {
		t.Fatalf("运行中改账号应返回 InvalidParams，实际 %v", err)
	}
}

func TestThreadAccount_TurnCarriesAccount(t *testing.T) {
	ctx := context.Background()
	b, _, projID, accID := setupThreadAccountBackend(t)

	agyMock := &mockHarness{
		kind: "agy",
		runFunc: func(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
			emit(harness.Event{Type: harness.EventText, Text: "ok"})
			emit(harness.Event{Type: harness.EventDone})
			return nil
		},
	}
	b.RegisterHarness(agyMock)

	agyProv := "agy"
	cRes, err := b.CreateThread(ctx, wire.ThreadCreateParams{
		ProjectID: projID,
		ActionID:  newUUID(),
		Options: &wire.ComposerOptions{
			Provider: &agyProv,
			Account:  &accID,
		},
	})
	if err != nil {
		t.Fatalf("CreateThread 失败: %v", err)
	}

	if _, err := b.SendMessage(ctx, wire.SendMessageParams{
		ThreadID: cRes.ThreadID,
		Text:     "hello",
		ActionID: newUUID(),
	}); err != nil {
		t.Fatalf("SendMessage 失败: %v", err)
	}
	waitThreadIdle(t, b, ctx, cRes.ThreadID)

	calls := agyMock.getTurnCalls()
	if len(calls) != 1 || calls[0].Account != accID {
		t.Fatalf("Turn 未携带 Account=%q: %+v", accID, calls)
	}
}

func TestThreadAccount_SchemaAndDispatcherValidation(t *testing.T) {
	data, err := os.ReadFile("../../docs/remote-control.schema.json")
	if err != nil {
		t.Fatalf("读取 schema 失败: %v", err)
	}
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("解析 schema 失败: %v", err)
	}
	for _, defName := range []string{"RemoteThreadSummary", "RemoteThreadsUpdateParams", "RemoteComposerOptions"} {
		raw, ok := doc.Defs[defName]
		if !ok {
			t.Fatalf("schema 缺少 %s", defName)
		}
		var def struct {
			Properties           map[string]json.RawMessage `json:"properties"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
		}
		if err := json.Unmarshal(raw, &def); err != nil {
			t.Fatalf("解析 %s 失败: %v", defName, err)
		}
		accRaw, hasAccount := def.Properties["account"]
		if !hasAccount {
			t.Fatalf("%s 缺少 account 属性", defName)
		}
		var prop struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(accRaw, &prop); err != nil || prop.Type != "string" {
			t.Fatalf("%s account 属性应为 string: %s (%v)", defName, string(accRaw), err)
		}
		if def.AdditionalProperties == nil || *def.AdditionalProperties {
			t.Fatalf("%s 必须保持 additionalProperties=false", defName)
		}
	}

	// 验证 Dispatcher RPC 端对 threads.create / threads.update 的 account 字段接收与返回。
	ctx := context.Background()
	b, _, projID, accID := setupThreadAccountBackend(t)
	d := NewDispatcher(&fakeAuth{}, b)
	conn := &fakeConn{info: ClientInfo{Authenticated: true}}

	createReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "threads.create",
		"params": map[string]any{
			"projectID": projID,
			"actionID":  newUUID(),
			"options": map[string]any{
				"provider": "agy",
				"account":  accID,
			},
		},
		"id": 1,
	})
	respRaw := d.Handle(ctx, conn, createReq)
	var createResp struct {
		Result wire.ThreadCreateResult `json:"result"`
		Error  *wire.RPCError          `json:"error"`
	}
	if err := json.Unmarshal(respRaw, &createResp); err != nil || createResp.Error != nil {
		t.Fatalf("threads.create RPC 失败: err=%v rpc=%+v", err, createResp.Error)
	}

	updateReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "threads.update",
		"params": map[string]any{
			"threadID": createResp.Result.ThreadID,
			"account":  "",
		},
		"id": 2,
	})
	respRaw = d.Handle(ctx, conn, updateReq)
	var updateResp struct {
		Result wire.ThreadUpdateResult `json:"result"`
		Error  *wire.RPCError          `json:"error"`
	}
	if err := json.Unmarshal(respRaw, &updateResp); err != nil || updateResp.Error != nil {
		t.Fatalf("threads.update RPC 失败: err=%v rpc=%+v", err, updateResp.Error)
	}
	if updateResp.Result.Thread.Account != "" {
		t.Fatalf("threads.update 清空后期望空串，实际 %q", updateResp.Result.Thread.Account)
	}
}
