package agy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mcode/internal/harness"
	"mcode/internal/providers/multiaccount"
)

type explicitAccountTestRouter struct {
	mu                 sync.Mutex
	routeCalls         int
	routeExplicitCalls []string
	bound              map[string]string
	markLimitedCalls   []string
	allowSwitch        bool
	displayNames       map[string]string
	explicitErr        error
	envForAccount      map[string][]string
	argsForAccount     map[string][]string
}

func (r *explicitAccountTestRouter) Route(conversationID, model string, exclude []string) (string, []string, []string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routeCalls++
	return "acc-auto", nil, nil, nil
}

func (r *explicitAccountTestRouter) RouteExplicit(conversationID, accountID, model string) ([]string, []string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routeExplicitCalls = append(r.routeExplicitCalls, conversationID+"="+accountID)
	if r.explicitErr != nil {
		return nil, nil, r.explicitErr
	}
	return append([]string(nil), r.envForAccount[accountID]...), append([]string(nil), r.argsForAccount[accountID]...), nil
}

func (r *explicitAccountTestRouter) Bind(conversationID, accountID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bound == nil {
		r.bound = make(map[string]string)
	}
	r.bound[conversationID] = accountID
}

func (r *explicitAccountTestRouter) MarkLimited(conversationID, accountID string, resetAt time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markLimitedCalls = append(r.markLimitedCalls, accountID)
	return r.allowSwitch
}

func (r *explicitAccountTestRouter) AccountDisplayName(accountID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.displayNames[accountID]
}

func TestExplicitAccount_PrintModeEnvAndNoFailover(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	t.Run("uses_explicit_env", func(t *testing.T) {
		router := &explicitAccountTestRouter{
			envForAccount: map[string][]string{
				"acc-explicit": {"HOME=/mock/isolated/home", "GEMINI_FORCE_FILE_STORAGE=true"},
			},
			argsForAccount: map[string][]string{
				"acc-explicit": {"--gemini_dir=/mock/isolated/home/.gemini"},
			},
		}
		h := New(Options{
			Binary:  exe,
			Channel: func() string { return ChannelPrintMode },
			Router:  router,
		})
		turn := harness.Turn{
			ThreadID: "th-1",
			Account:  "acc-explicit",
			Prompt:   "Check explicit env",
			Mode:     harness.ModeAutoAcceptEdits,
			Env:      []string{fakeAgyEnv + "=check_env"},
		}
		if err := h.Run(context.Background(), turn, nil); err != nil {
			t.Fatalf("Run 失败: %v", err)
		}
		if router.routeCalls != 0 {
			t.Fatalf("显式账号不应调用自动 Route，实际调用 %d 次", router.routeCalls)
		}
		if len(router.routeExplicitCalls) != 1 || router.routeExplicitCalls[0] != "thread:th-1=acc-explicit" {
			t.Fatalf("RouteExplicit 调用记录不符: %v", router.routeExplicitCalls)
		}
		if router.bound["thread:th-1"] != "acc-explicit" || router.bound["env-conv-123"] != "acc-explicit" {
			t.Fatalf("绑定记录不符: %+v", router.bound)
		}
	})

	t.Run("rate_limit_fails_without_switch_even_when_router_allows_switch", func(t *testing.T) {
		router := &explicitAccountTestRouter{
			allowSwitch:  true, // 非 strict 亲和性下，仍禁止显式账号自动换号
			displayNames: map[string]string{"acc-locked": "锁定测试号"},
		}
		h := New(Options{
			Binary:  exe,
			Channel: func() string { return ChannelPrintMode },
			Router:  router,
		})
		turn := harness.Turn{
			ThreadID: "th-2",
			Account:  "acc-locked",
			Prompt:   "Rate limit test",
			Mode:     harness.ModeSupervised,
			Env:      []string{fakeAgyEnv + "=strict_rate_limit"},
		}
		err := h.Run(context.Background(), turn, nil)
		if err == nil {
			t.Fatal("限流时必须报错，不能静默换号")
		}
		if !strings.Contains(err.Error(), "锁定测试号") || !strings.Contains(err.Error(), "限流") || !strings.Contains(err.Error(), "重置时间") {
			t.Fatalf("错误文案应说明账号与重置时间: %v", err)
		}
		if len(router.routeExplicitCalls) != 1 || router.routeCalls != 0 {
			t.Fatalf("不应重试或切换自动路由: explicit=%v auto=%d", router.routeExplicitCalls, router.routeCalls)
		}
		if len(router.markLimitedCalls) != 1 || router.markLimitedCalls[0] != "acc-locked" {
			t.Fatalf("应记录限流冷却: %v", router.markLimitedCalls)
		}
	})

	t.Run("route_explicit_error_stops_immediately", func(t *testing.T) {
		wantErr := errors.New("agy: 指定的账号「停用账号」已停用")
		router := &explicitAccountTestRouter{explicitErr: wantErr}
		h := New(Options{
			Binary:  exe,
			Channel: func() string { return ChannelPrintMode },
			Router:  router,
		})
		err := h.Run(context.Background(), harness.Turn{
			ThreadID: "th-3",
			Account:  "acc-disabled",
			Prompt:   "hi",
			Mode:     harness.ModeSupervised,
		}, nil)
		if !errors.Is(err, wantErr) {
			t.Fatalf("期望返回 %v，实际 %v", wantErr, err)
		}
		if router.routeCalls != 0 {
			t.Fatalf("不应回退自动路由: %d", router.routeCalls)
		}
	})
}

func TestExplicitAccount_ACPRateLimitAndEvictOnAccountChange(t *testing.T) {
	t.Parallel()

	t.Run("acp_rate_limit_no_failover", func(t *testing.T) {
		t.Parallel()
		// AffinitySession 下 MarkLimited 返回 true，但指定了 Account 时仍必须直接报错且不换号。
		router := newACPRetryRouter(multiaccount.AffinitySession)
		h := New(Options{Binary: os.Args[0], Router: router})
		turn := acpRetryTurn(t, "always")
		turn.Account = "account-0"

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := h.runACP(ctx, turn, nil)
		if err == nil {
			t.Fatal("ACP 显式账号限流必须报错")
		}
		if !strings.Contains(err.Error(), "account-0") || !strings.Contains(err.Error(), "2099-01-02T03:04:05Z") {
			t.Fatalf("错误文案应含账号与重置时间: %v", err)
		}
		if len(router.routes) != 1 || len(router.limited) != 1 {
			t.Fatalf("不应重试换号，且应记入冷却: routes=%v limited=%v", router.routes, router.limited)
		}
		if !router.router.Cooling("account-0", time.Now()) {
			t.Fatal("account-0 应已进入冷却状态")
		}
	})

	t.Run("acp_evicts_resident_process_when_explicit_account_differs", func(t *testing.T) {
		t.Parallel()
		router := newACPRetryRouter(multiaccount.AffinitySession)
		router.geminiDir = t.TempDir()
		h := New(Options{Binary: os.Args[0], Router: router})
		threadID := "thread-evict-" + filepath.Base(t.TempDir())
		t.Cleanup(func() { h.EvictThread(threadID) })

		turn1 := harness.Turn{
			ThreadID: threadID,
			Account:  "account-1",
			Prompt:   "first",
			WorkDir:  t.TempDir(),
			Env:      []string{fakeAgyEnv + "=acp_transport", "MCODE_TEST_ACP_HOLD=1"},
		}
		ctx1, cancel1 := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel1()
		if err := h.runACP(ctx1, turn1, nil); err != nil {
			t.Fatalf("首轮失败: %v", err)
		}
		if !h.acp.HasResidentProcess(threadID) {
			t.Fatal("首轮后应保留常驻 ACP 进程")
		}
		if got, ok := h.acpAccounts.Load(threadID); !ok || got != "account-1" {
			t.Fatalf("常驻进程账号记录应为 account-1，实际 %v (%v)", got, ok)
		}

		// 第二轮显式指定不同账号 account-2：必须先驱逐旧常驻进程，再用 account-2 启动新进程。
		var gotEnvAccount string
		turn2 := harness.Turn{
			ThreadID: threadID,
			Account:  "account-2",
			Prompt:   "second",
			WorkDir:  turn1.WorkDir,
			Env:      []string{fakeAgyEnv + "=acp_transport"},
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel2()
		err := h.runACP(ctx2, turn2, func(ev harness.Event) {
			if ev.Type != harness.EventText {
				return
			}
			var got struct {
				Env string `json:"env"`
			}
			if json.Unmarshal([]byte(ev.Text), &got) == nil {
				gotEnvAccount = got.Env
			}
		})
		if err != nil {
			t.Fatalf("第二轮失败: %v", err)
		}
		if gotEnvAccount != "account-2" {
			t.Fatalf("期望使用 account-2 启动新进程，实际 env=%q", gotEnvAccount)
		}
		if len(router.routes) != 2 {
			t.Fatalf("期望驱逐后重新走 RouteExplicit，实际 routes=%v", router.routes)
		}
	})
}
