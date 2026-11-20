package accounts

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcode/internal/providers/agy/quota"
)

func setupExplicitRouteTestManager(t *testing.T, now time.Time) (*Manager, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	hostHome := t.TempDir()
	mgr, err := NewManager(Options{
		DataDir:  dataDir,
		HostHome: hostHome,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewManager 失败: %v", err)
	}
	accA := createTestAccountWithToken(t, mgr, "a@example.com", "tok-a", "ref-a", now.Add(time.Hour))
	accB := createTestAccountWithToken(t, mgr, "b@example.com", "tok-b", "ref-b", now.Add(time.Hour))

	labelA := "工作账号A"
	labelB := "测试账号B"
	_ = mgr.Update(accA, UpdatePatch{Label: &labelA})
	_ = mgr.Update(accB, UpdatePatch{Label: &labelB})

	// 默认账号设为 accA（非系统账号、非 accB）
	if err := mgr.SetDefault(accA); err != nil {
		t.Fatalf("SetDefault 失败: %v", err)
	}
	return mgr, accA, accB
}

func TestRouteExplicit_SelectsTargetEvenWhenDefaultIsOther(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	mgr, _, accB := setupExplicitRouteTestManager(t, now)

	// 即使系统账号和 accA 都是默认/候选，显式指定 accB 仍必须选中 accB。
	env, args, err := mgr.RouteExplicit("thread:t-explicit-1", accB, "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("RouteExplicit 失败: %v", err)
	}

	wantHome := mgr.accountHomeDir(accB)
	wantGemini := filepath.Join(wantHome, ".gemini")
	if !containsStr(env, "HOME="+wantHome) {
		t.Fatalf("env 缺少目标账号 HOME=%s: %v", wantHome, env)
	}
	if !containsStr(args, "--gemini_dir="+wantGemini) {
		t.Fatalf("args 缺少目标账号 --gemini_dir=%s: %v", wantGemini, args)
	}
	if bound, ok := mgr.BoundAccountID("thread:t-explicit-1"); !ok || bound != accB {
		t.Fatalf("期望绑定账号 %q，实际 bound=%q ok=%v", accB, bound, ok)
	}
}

func TestRouteExplicit_Errors(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

	t.Run("not_found", func(t *testing.T) {
		mgr, _, _ := setupExplicitRouteTestManager(t, now)
		_, _, err := mgr.RouteExplicit("thread:t1", "acc-missing", "gemini-2.5-pro")
		if err == nil {
			t.Fatal("不存在账号应报错")
		}
		if !strings.Contains(err.Error(), "acc-missing") || !strings.Contains(err.Error(), "不存在") {
			t.Fatalf("错误文案应包含账号标识与不存在说明: %v", err)
		}
	})

	t.Run("disabled_on_false", func(t *testing.T) {
		mgr, _, accB := setupExplicitRouteTestManager(t, now)
		off := false
		if err := mgr.Update(accB, UpdatePatch{On: &off}); err != nil {
			t.Fatal(err)
		}
		_, _, err := mgr.RouteExplicit("thread:t2", accB, "gemini-2.5-pro")
		if err == nil {
			t.Fatal("停用账号应报错")
		}
		if !strings.Contains(err.Error(), "测试账号B") || !strings.Contains(err.Error(), "停用") {
			t.Fatalf("错误文案应含账号显示名与停用说明: %v", err)
		}
	})

	t.Run("disabled_routing_override", func(t *testing.T) {
		mgr, _, accB := setupExplicitRouteTestManager(t, now)
		off := false
		mgr.mu.Lock()
		if mgr.routing.Accounts == nil {
			mgr.routing.Accounts = make(map[string]AccountRoutingOverride)
		}
		mgr.routing.Accounts[accB] = AccountRoutingOverride{On: &off}
		mgr.mu.Unlock()

		_, _, err := mgr.RouteExplicit("thread:t2b", accB, "gemini-2.5-pro")
		if err == nil {
			t.Fatal("路由覆盖停用的账号应报错")
		}
		if !strings.Contains(err.Error(), "测试账号B") || !strings.Contains(err.Error(), "停用") {
			t.Fatalf("错误文案应含账号显示名与停用说明: %v", err)
		}
	})

	t.Run("disabled_status", func(t *testing.T) {
		mgr, _, accB := setupExplicitRouteTestManager(t, now)
		mgr.mu.Lock()
		for i := range mgr.index.Accounts {
			if mgr.index.Accounts[i].ID == accB {
				mgr.index.Accounts[i].Status = StatusForbidden
			}
		}
		mgr.mu.Unlock()

		_, _, err := mgr.RouteExplicit("thread:t3", accB, "gemini-2.5-pro")
		if err == nil {
			t.Fatal("异常状态账号应报错")
		}
		if !strings.Contains(err.Error(), "测试账号B") {
			t.Fatalf("错误文案应含账号显示名: %v", err)
		}
	})

	t.Run("cooling", func(t *testing.T) {
		mgr, _, accB := setupExplicitRouteTestManager(t, now)
		mgr.MarkLimited("conv-x", accB, now.Add(15*time.Minute))

		_, _, err := mgr.RouteExplicit("thread:t4", accB, "gemini-2.5-pro")
		if err == nil {
			t.Fatal("冷却中账号应报错")
		}
		if !strings.Contains(err.Error(), "测试账号B") || !strings.Contains(err.Error(), "冷却") {
			t.Fatalf("错误文案应含账号显示名与冷却说明: %v", err)
		}
	})

	t.Run("quota_full", func(t *testing.T) {
		mgr, _, accB := setupExplicitRouteTestManager(t, now)
		mgr.mu.Lock()
		for i := range mgr.index.Accounts {
			if mgr.index.Accounts[i].ID == accB {
				mgr.index.Accounts[i].QuotaReadAt = now.Format(time.RFC3339)
				mgr.index.Accounts[i].Quota = map[string]QuotaGroup{
					quota.GroupGemini: {
						UsedPercent: 100,
						ResetAt:     now.Add(time.Hour).Format(time.RFC3339),
					},
				}
			}
		}
		mgr.mu.Unlock()

		_, _, err := mgr.RouteExplicit("thread:t5", accB, "gemini-2.5-pro")
		if err == nil {
			t.Fatal("满额账号应报错")
		}
		if !strings.Contains(err.Error(), "测试账号B") || !strings.Contains(err.Error(), "满") {
			t.Fatalf("错误文案应含账号显示名与满额说明: %v", err)
		}
	})
}

func containsStr(slice []string, want string) bool {
	for _, s := range slice {
		if s == want {
			return true
		}
	}
	return false
}
