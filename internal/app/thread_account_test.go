package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mcode/internal/providers/agy/accounts"
	"mcode/internal/providers/agy/quota"
	mcodeui "mcode/internal/ui"
)

func TestThreadAccount_QuotaLookupBeforeFirstTurnAndClientRoundTrip(t *testing.T) {
	dir := t.TempDir()
	index := accounts.Index{
		Version:          1,
		DefaultAccountID: accounts.SystemAccountID,
		Accounts: []accounts.Account{
			{
				ID:     accounts.SystemAccountID,
				Label:  "系统账号",
				Status: accounts.StatusActive,
				On:     true,
				Order:  0,
				Weight: 100,
				Quota:  map[string]accounts.QuotaGroup{quota.GroupGemini: {UsedPercent: 10}},
			},
			{
				ID:     "acc-explicit",
				Label:  "显式账号",
				Status: accounts.StatusActive,
				On:     true,
				Order:  1,
				Weight: 100,
				Quota:  map[string]accounts.QuotaGroup{quota.GroupGemini: {UsedPercent: 30}},
			},
		},
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agy-accounts.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	mgr, err := accounts.NewManager(accounts.Options{DataDir: dir, HostHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	// 首轮前（SessionID 为空），agyBoundAccountForThread 必须优先返回线程上显式指定的 Account。
	client := &Client{agy: mgr}
	client.SetAgySessionLookup(func(context.Context, string) string { return "" })
	client.SetAgyAccountLookup(func(_ context.Context, threadID string) string {
		if threadID == "thread-explicit" {
			return "acc-explicit"
		}
		return ""
	})

	gotAcc, ok := client.agyBoundAccountForThread(mcodeui.WithQuotaThread(context.Background(), "thread-explicit"))
	if !ok || gotAcc != "acc-explicit" {
		t.Fatalf("期望首轮前返回线程 Account=acc-explicit，实际 got=%q ok=%v", gotAcc, ok)
	}
	if _, ok := client.agyBoundAccountForThread(mcodeui.WithQuotaThread(context.Background(), "thread-auto")); ok {
		t.Fatal("未指定 Account 且 SessionID 为空时不应命中绑定")
	}

	windows, err := client.ProviderQuota(mcodeui.WithQuotaThread(context.Background(), "thread-explicit"), "agy")
	if err != nil || len(windows) != 2 {
		t.Fatalf("ProviderQuota 失败: windows=%+v err=%v", windows, err)
	}
	if windows[0].Account != "系统账号" || windows[0].Active {
		t.Fatalf("系统账号不应为 Active: %+v", windows[0])
	}
	if windows[1].Account != "显式账号" || !windows[1].Active {
		t.Fatalf("显式账号应为 Active: %+v", windows[1])
	}
}

func TestThreadAccount_CreateWithOptionsAndSetThreadAccount(t *testing.T) {
	ctx := context.Background()
	app, err := Open(Options{
		DataDir:  t.TempDir(),
		Provider: "memory",
		WorkDir:  t.TempDir(),
		Mode:     "agent",
	})
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer app.Close()

	mgr, err := accounts.NewManager(accounts.Options{
		DataDir:  t.TempDir(),
		HostHome: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("accounts.NewManager 失败: %v", err)
	}
	app.backend.RegisterHarness(&testMockHarness{kind: "agy"})
	app.backend.SetAgyAccounts(mgr)

	client := app.Client()
	client.SetAgyAccounts(mgr)
	projects, err := client.Projects(ctx)
	if err != nil || len(projects) == 0 {
		t.Fatalf("Projects 失败: %v", err)
	}
	projID := projects[0].ID

	thID, err := client.CreateThreadWithOptions(ctx, projID, mcodeui.NewThreadOptions{
		Provider: "agy",
		Account:  accounts.SystemAccountID,
	})
	if err != nil {
		t.Fatalf("CreateThreadWithOptions 失败: %v", err)
	}

	gotAcc, ok := client.agyBoundAccountForThread(mcodeui.WithQuotaThread(ctx, thID))
	if !ok || gotAcc != accounts.SystemAccountID {
		t.Fatalf("Open 装配的 SetAgyAccountLookup 应在首轮前查到 %q，实际 got=%q ok=%v", accounts.SystemAccountID, gotAcc, ok)
	}

	threads, err := client.Threads(ctx, projID)
	if err != nil {
		t.Fatalf("Threads 失败: %v", err)
	}
	var found bool
	for _, th := range threads {
		if th.ID == thID {
			found = true
			if th.Account != accounts.SystemAccountID {
				t.Fatalf("期望 ui.Thread.Account=%q，实际 %q", accounts.SystemAccountID, th.Account)
			}
		}
	}
	if !found {
		t.Fatal("Threads 未返回新建会话")
	}

	if err := client.SetThreadAccount(thID, ""); err != nil {
		t.Fatalf("SetThreadAccount 清空失败: %v", err)
	}
	if _, ok := client.agyBoundAccountForThread(mcodeui.WithQuotaThread(ctx, thID)); ok {
		t.Fatal("清空 Account 且未跑过首轮后，不应再返回绑定账号")
	}
}

func TestProviderQuotaAgyAccountID(t *testing.T) {
	dir := t.TempDir()
	index := accounts.Index{
		Version:          1,
		DefaultAccountID: accounts.SystemAccountID,
		Accounts: []accounts.Account{
			{
				ID:     accounts.SystemAccountID,
				Label:  "系统账号",
				Status: accounts.StatusActive,
				On:     true,
				Order:  0,
				Weight: 100,
				Quota:  map[string]accounts.QuotaGroup{quota.GroupGemini: {UsedPercent: 15}},
			},
			{
				ID:     "acc-work",
				Label:  "工作账号",
				Status: accounts.StatusActive,
				On:     true,
				Order:  1,
				Weight: 100,
				Quota:  map[string]accounts.QuotaGroup{quota.GroupGemini: {UsedPercent: 45}},
			},
		},
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agy-accounts.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	mgr, err := accounts.NewManager(accounts.Options{DataDir: dir, HostHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	client := &Client{agy: mgr}
	windows, err := client.ProviderQuota(context.Background(), "agy")
	if err != nil {
		t.Fatalf("ProviderQuota 失败: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("期望返回 2 个窗口，实际 %d: %+v", len(windows), windows)
	}
	if windows[0].AccountID != accounts.SystemAccountID || windows[1].AccountID != "acc-work" {
		t.Fatalf("窗口 AccountID 不正确: got=[%q, %q] want=[%q, %q]",
			windows[0].AccountID, windows[1].AccountID, accounts.SystemAccountID, "acc-work")
	}
}
