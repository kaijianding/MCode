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
	"mcode/internal/kanban"
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

func TestStartSessionInheritsLeadBeesAccount(t *testing.T) {
	ctx := context.Background()
	b, _, projID, accID := setupThreadAccountBackend(t)
	b.RegisterHarness(&mockHarness{kind: "claude"})

	claudeProv := "claude"
	leadRes, err := b.CreateThread(ctx, wire.ThreadCreateParams{
		ProjectID: projID,
		ActionID:  newUUID(),
		Options:   &wire.ComposerOptions{Provider: &claudeProv},
	})
	if err != nil {
		t.Fatalf("CreateThread 失败: %v", err)
	}
	if _, err := b.SetThreadComposerPrefs(ctx, leadRes.ThreadID, ThreadComposerPrefs{
		InteractionMode: "build",
		BeesProvider:    "agy",
	}); err != nil {
		t.Fatalf("SetThreadComposerPrefs 失败: %v", err)
	}
	if _, err := b.SetThreadBeesAccount(ctx, leadRes.ThreadID, accID); err != nil {
		t.Fatalf("SetThreadBeesAccount 失败: %v", err)
	}

	agyBeeID, err := b.StartSession(ctx, kanban.SessionRequest{
		LeadThreadID: leadRes.ThreadID,
		ProjectID:    projID,
		WorkDir:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("StartSession(agy) 失败: %v", err)
	}
	agyBee, err := b.GetThread(ctx, agyBeeID)
	if err != nil {
		t.Fatalf("GetThread(agyBee) 失败: %v", err)
	}
	if agyBee.Provider != "agy" || agyBee.Account != accID {
		t.Fatalf("agy bee 期望 Provider=agy Account=%q，实际 Provider=%q Account=%q", accID, agyBee.Provider, agyBee.Account)
	}

	codexBeeID, err := b.StartSession(ctx, kanban.SessionRequest{
		LeadThreadID: leadRes.ThreadID,
		ProjectID:    projID,
		WorkDir:      t.TempDir(),
		Provider:     "codex",
	})
	if err != nil {
		t.Fatalf("StartSession(codex) 失败: %v", err)
	}
	codexBee, err := b.GetThread(ctx, codexBeeID)
	if err != nil {
		t.Fatalf("GetThread(codexBee) 失败: %v", err)
	}
	if codexBee.Provider != "codex" || codexBee.Account != "" {
		t.Fatalf("非 agy bee 期望 Provider=codex Account 为空，实际 Provider=%q Account=%q", codexBee.Provider, codexBee.Account)
	}
}

func TestSetThreadBeesAccountPersists(t *testing.T) {
	ctx := context.Background()
	b, s, projID, accID := setupThreadAccountBackend(t)
	b.RegisterHarness(&mockHarness{kind: "claude"})

	claudeProv := "claude"
	leadRes, err := b.CreateThread(ctx, wire.ThreadCreateParams{
		ProjectID: projID,
		ActionID:  newUUID(),
		Options:   &wire.ComposerOptions{Provider: &claudeProv},
	})
	if err != nil {
		t.Fatalf("CreateThread 失败: %v", err)
	}

	// 主会话运行中也允许设置 BeesAccount。
	b.mu.Lock()
	b.threads[leadRes.ThreadID].summary.IsRunning = true
	b.mu.Unlock()

	updated, err := b.SetThreadBeesAccount(ctx, leadRes.ThreadID, accID)
	if err != nil {
		t.Fatalf("SetThreadBeesAccount 失败: %v", err)
	}
	if derefString(updated.SwarmBeesAccount) != accID {
		t.Fatalf("返回值期望 SwarmBeesAccount=%q，实际 %q", accID, derefString(updated.SwarmBeesAccount))
	}

	got, err := b.GetThread(ctx, leadRes.ThreadID)
	if err != nil {
		t.Fatalf("GetThread 失败: %v", err)
	}
	if derefString(got.SwarmBeesAccount) != accID {
		t.Fatalf("GetThread 期望 SwarmBeesAccount=%q，实际 %q", accID, derefString(got.SwarmBeesAccount))
	}

	stored, err := s.GetThread(ctx, leadRes.ThreadID)
	if err != nil {
		t.Fatalf("store.GetThread 失败: %v", err)
	}
	if stored.BeesAccount != accID {
		t.Fatalf("store 期望 BeesAccount=%q，实际 %q", accID, stored.BeesAccount)
	}

	// 重建 backend 从同一 store 恢复后仍能读到 SwarmBeesAccount。
	hub2 := NewStreamHub(StreamHubOptions{Interval: 5 * time.Millisecond})
	defer hub2.Close()
	b2 := NewMemoryBackend(hub2)
	defer b2.Close()
	if err := b2.AttachStore(ctx, s); err != nil {
		t.Fatalf("重建 AttachStore 失败: %v", err)
	}
	reloaded, err := b2.GetThread(ctx, leadRes.ThreadID)
	if err != nil {
		t.Fatalf("重建后 GetThread 失败: %v", err)
	}
	if derefString(reloaded.SwarmBeesAccount) != accID {
		t.Fatalf("重建后期望 SwarmBeesAccount=%q，实际 %q", accID, derefString(reloaded.SwarmBeesAccount))
	}

	// 清空后同样落盘并可从 GetThread 与 store 读回空串。
	if _, err := b.SetThreadBeesAccount(ctx, leadRes.ThreadID, ""); err != nil {
		t.Fatalf("SetThreadBeesAccount 清空失败: %v", err)
	}
	cleared, err := b.GetThread(ctx, leadRes.ThreadID)
	if err != nil {
		t.Fatalf("清空后 GetThread 失败: %v", err)
	}
	if derefString(cleared.SwarmBeesAccount) != "" {
		t.Fatalf("清空后期望 SwarmBeesAccount 为空，实际 %q", derefString(cleared.SwarmBeesAccount))
	}
	storedCleared, err := s.GetThread(ctx, leadRes.ThreadID)
	if err != nil {
		t.Fatalf("清空后 store.GetThread 失败: %v", err)
	}
	if storedCleared.BeesAccount != "" {
		t.Fatalf("清空后 store 期望 BeesAccount 为空，实际 %q", storedCleared.BeesAccount)
	}
}

func assertObjectMatchesSchemaDef(t *testing.T, defs map[string]json.RawMessage, defName string, obj map[string]any) {
	t.Helper()
	raw, ok := defs[defName]
	if !ok {
		t.Fatalf("schema 缺少 %s", defName)
	}
	var def struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(raw, &def); err != nil {
		t.Fatalf("解析 %s 失败: %v", defName, err)
	}
	for _, req := range def.Required {
		if _, exists := obj[req]; !exists {
			t.Fatalf("%s 缺少必填字段 %q: %+v", defName, req, obj)
		}
	}
	if def.AdditionalProperties != nil && !*def.AdditionalProperties {
		for k := range obj {
			if _, exists := def.Properties[k]; !exists {
				t.Fatalf("%s 不允许未知字段 %q: %+v", defName, k, obj)
			}
		}
	}
}

func TestThreadsUpdateSwarmBeesAccount(t *testing.T) {
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
	for _, defName := range []string{"RemoteThreadsUpdateParams", "RemoteThreadSummary"} {
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
		if _, has := def.Properties["swarmBeesAccount"]; !has {
			t.Fatalf("%s 缺少 swarmBeesAccount 属性", defName)
		}
		if def.AdditionalProperties == nil || *def.AdditionalProperties {
			t.Fatalf("%s 必须保持 additionalProperties=false", defName)
		}
	}

	ctx := context.Background()
	b, s, projID, accID := setupThreadAccountBackend(t)
	b.RegisterHarness(&mockHarness{kind: "claude"})
	d := NewDispatcher(&fakeAuth{}, b)
	conn := &fakeConn{info: ClientInfo{Authenticated: true}}

	claudeProv := "claude"
	cRes, err := b.CreateThread(ctx, wire.ThreadCreateParams{
		ProjectID: projID,
		ActionID:  newUUID(),
		Options:   &wire.ComposerOptions{Provider: &claudeProv},
	})
	if err != nil {
		t.Fatalf("CreateThread 失败: %v", err)
	}

	// 1. 经 dispatcher 设置 swarmBeesAccount 成功并落库，且请求/响应均符合 schema。
	paramsMap := map[string]any{
		"threadID":         cRes.ThreadID,
		"swarmBeesAccount": accID,
	}
	assertObjectMatchesSchemaDef(t, doc.Defs, "RemoteThreadsUpdateParams", paramsMap)

	reqBytes, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "threads.update",
		"params":  paramsMap,
		"id":      1,
	})
	respRaw := d.Handle(ctx, conn, reqBytes)
	var rpcEnv struct {
		Result json.RawMessage `json:"result"`
		Error  *wire.RPCError  `json:"error"`
	}
	if err := json.Unmarshal(respRaw, &rpcEnv); err != nil || rpcEnv.Error != nil {
		t.Fatalf("threads.update RPC 失败: err=%v rpc=%+v", err, rpcEnv.Error)
	}
	var resultMap map[string]any
	if err := json.Unmarshal(rpcEnv.Result, &resultMap); err != nil {
		t.Fatalf("解析 result map 失败: %v", err)
	}
	assertObjectMatchesSchemaDef(t, doc.Defs, "RemoteThreadsUpdateResult", resultMap)
	threadMap, ok := resultMap["thread"].(map[string]any)
	if !ok {
		t.Fatalf("result.thread 类型错误: %T", resultMap["thread"])
	}
	assertObjectMatchesSchemaDef(t, doc.Defs, "RemoteThreadSummary", threadMap)

	var updateRes wire.ThreadUpdateResult
	if err := json.Unmarshal(rpcEnv.Result, &updateRes); err != nil {
		t.Fatalf("解析 ThreadUpdateResult 失败: %v", err)
	}
	if derefString(updateRes.Thread.SwarmBeesAccount) != accID {
		t.Fatalf("返回 SwarmBeesAccount 期望 %q，实际 %q", accID, derefString(updateRes.Thread.SwarmBeesAccount))
	}
	stored, err := s.GetThread(ctx, cRes.ThreadID)
	if err != nil {
		t.Fatalf("store.GetThread 失败: %v", err)
	}
	if stored.BeesAccount != accID {
		t.Fatalf("store 期望 BeesAccount=%q，实际 %q", accID, stored.BeesAccount)
	}

	// 2. 会话 IsRunning 时也允许经 threads.update 修改（例如清空为自动路由）。
	b.mu.Lock()
	b.threads[cRes.ThreadID].summary.IsRunning = true
	b.mu.Unlock()

	clearParams := map[string]any{
		"threadID":         cRes.ThreadID,
		"swarmBeesAccount": "",
	}
	assertObjectMatchesSchemaDef(t, doc.Defs, "RemoteThreadsUpdateParams", clearParams)
	clearReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "threads.update",
		"params":  clearParams,
		"id":      2,
	})
	clearRaw := d.Handle(ctx, conn, clearReq)
	var clearResp struct {
		Result wire.ThreadUpdateResult `json:"result"`
		Error  *wire.RPCError          `json:"error"`
	}
	if err := json.Unmarshal(clearRaw, &clearResp); err != nil || clearResp.Error != nil {
		t.Fatalf("运行中 threads.update 清空 swarmBeesAccount 失败: err=%v rpc=%+v", err, clearResp.Error)
	}
	if derefString(clearResp.Result.Thread.SwarmBeesAccount) != "" {
		t.Fatalf("清空后期望 SwarmBeesAccount 为空，实际 %q", derefString(clearResp.Result.Thread.SwarmBeesAccount))
	}
	storedCleared, err := s.GetThread(ctx, cRes.ThreadID)
	if err != nil {
		t.Fatalf("清空后 store.GetThread 失败: %v", err)
	}
	if storedCleared.BeesAccount != "" {
		t.Fatalf("清空后 store 期望 BeesAccount 为空，实际 %q", storedCleared.BeesAccount)
	}

	// 3. 不存在的账号返回 InvalidParams。
	badReq, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "threads.update",
		"params": map[string]any{
			"threadID":         cRes.ThreadID,
			"swarmBeesAccount": "acc-nonexistent",
		},
		"id": 3,
	})
	badRaw := d.Handle(ctx, conn, badReq)
	var badResp struct {
		Error *wire.RPCError `json:"error"`
	}
	if err := json.Unmarshal(badRaw, &badResp); err != nil {
		t.Fatalf("解析错误响应失败: %v", err)
	}
	if badResp.Error == nil || badResp.Error.Code != wire.InvalidParams {
		t.Fatalf("不存在账号应返回 InvalidParams，实际 %+v", badResp.Error)
	}
}
