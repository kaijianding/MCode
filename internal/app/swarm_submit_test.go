package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mcode/internal/git"
	"mcode/internal/harness"
	"mcode/internal/mcp"
	"mcode/internal/remote"
	"mcode/internal/remote/wire"
	"mcode/internal/swarm"
	"mcode/internal/testenv"
)

// submitHarness 是 submit 测试用的派发器：dispatch 记录收到的 prompt，轮次计数由 rounds 驱动。
type submitHarness struct {
	mu         sync.Mutex
	prompts    []string
	rounds     int
	maxRounds  int
	dispatcher *swarmStreamDispatcher
}

func newSubmitHarness(t *testing.T, maxRounds int) *submitHarness {
	t.Helper()
	h := &submitHarness{maxRounds: maxRounds}
	h.dispatcher = newSwarmStreamDispatcher(context.Background(),
		func(ctx context.Context, req swarm.SingleEntryRequest) (swarm.Bee, []string, error) {
			h.mu.Lock()
			h.prompts = append(h.prompts, req.Entry.Prompt)
			h.mu.Unlock()
			return swarm.Bee{}, nil, nil
		},
		func(string) int { h.mu.Lock(); defer h.mu.Unlock(); return h.rounds },
		func(string) int { h.mu.Lock(); defer h.mu.Unlock(); h.rounds++; return h.rounds },
		func(string) (string, bool) { return t.TempDir(), true },
		func(string, string) error { return nil },
		func() int { return h.maxRounds },
	)
	return h
}

func (h *submitHarness) dispatched() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.prompts...)
}

func delegations(prompts ...string) []swarm.Delegation {
	entries := make([]swarm.Delegation, 0, len(prompts))
	for _, prompt := range prompts {
		entries = append(entries, swarm.Delegation{Task: prompt, Prompt: prompt})
	}
	return entries
}

func TestSubmitDispatchesAndDedupesAgainstBlock(t *testing.T) {
	h := newSubmitHarness(t, 0)
	d := h.dispatcher

	d.reset("lead")
	if got := d.submit("lead", "project", delegations("alpha", "beta")); got != "Started 2 bee(s)." {
		t.Fatalf("结果文本异常: %q", got)
	}
	// 同一 prompt 再提交：跳过并说明原因。
	if got := d.submit("lead", "project", delegations("alpha")); !strings.Contains(got, "Started 0 bee(s).") || !strings.Contains(got, "already dispatched") {
		t.Fatalf("重复条目应被跳过: %q", got)
	}
	// 回合末整段回复里又写了同样的块：补派必须认出已经派过的条目。
	d.settleTurn("lead", "project", "```swarm\n[{\"prompt\":\"alpha\"},{\"prompt\":\"gamma\"}]\n```")

	waitSwarm(t, func() bool { return len(h.dispatched()) == 3 })
	want := []string{"alpha", "beta", "gamma"}
	got := h.dispatched()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("派发顺序或内容不对: %v", got)
		}
	}
	if h.rounds != 1 {
		t.Fatalf("同一回合工具与块应只计一轮，实际 %d", h.rounds)
	}
}

func TestSubmitHonorsEntryCap(t *testing.T) {
	h := newSubmitHarness(t, 0)
	d := h.dispatcher
	d.maxEntries = func() int { return 1 }

	d.reset("lead")
	got := d.submit("lead", "project", delegations("alpha", "beta"))
	if !strings.HasPrefix(got, "Started 1 bee(s).") || !strings.Contains(got, "1 entry(ies) not started: at most 1 bees per turn") {
		t.Fatalf("超出条目上限的说明异常: %q", got)
	}
	waitSwarm(t, func() bool { return len(h.dispatched()) == 1 })
	if h.dispatched()[0] != "alpha" {
		t.Fatalf("应只派发第一条: %v", h.dispatched())
	}
}

func TestSubmitHonorsRoundLimit(t *testing.T) {
	h := newSubmitHarness(t, 1)
	h.rounds = 1 // 本次请求已经用掉唯一的一轮
	d := h.dispatcher

	d.reset("lead")
	got := d.submit("lead", "project", delegations("alpha", "beta"))
	if !strings.HasPrefix(got, "Started 0 bee(s).") || !strings.Contains(got, "2 entry(ies) not started: the request reached its limit of 1 delegation rounds") {
		t.Fatalf("超出轮次上限的说明异常: %q", got)
	}
	if n := len(h.dispatched()); n != 0 {
		t.Fatalf("达到轮次上限后不应派发: %v", h.dispatched())
	}
}

// TestDelegateToolFollowsSetting 验证 Swarm.DelegateTool 同时控制主代理提示词里的工具说明
// 与 swarm_delegate 在 tools/list 里的可见性。
func TestDelegateToolFollowsSetting(t *testing.T) {
	testenv.TempHome(t)
	ctx := context.Background()
	dir := swarmRepo(t)
	hub := remote.NewStreamHub(remote.StreamHubOptions{})
	defer hub.Close()
	b := remote.NewMemoryBackend(hub)
	defer b.Close()

	h := newPromptCaptureHarness()
	b.SetHarness(h, dir, harness.ModeFullAccess)
	b.AddProject(wire.ProjectSummary{ID: "p", Name: "P", Path: dir})
	// 每个线程只发一条消息：同一线程上一轮没结束时再发会被拒绝。
	for _, id := range []string{"lead-off", "lead-on"} {
		b.AddThread(wire.ThreadSummary{ID: id, ProjectID: "p", Provider: "prompt-capture"}, nil)
	}

	mgr := swarm.NewManager(swarm.Options{Git: git.NewRunner("", 4), Driver: b, WorktreeDir: t.TempDir(), PollInterval: testSwarmPollInterval})
	var delegateTool atomic.Bool
	mgr.SetSettingsFunc(func() swarm.Settings {
		return swarm.Settings{Enabled: true, AutoDelegate: true, DelegateTool: delegateTool.Load()}
	})
	mgr.SetEnabled("lead-off", true)
	mgr.SetEnabled("lead-on", true)

	srv := mcp.New(mcp.Options{})
	wireSwarmWithModes(ctx, mgr, b, srv, nil, nil)

	listed := func(threadID string) bool {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.ServeThread(rec, req, threadID)
		return strings.Contains(rec.Body.String(), `"swarm_delegate"`)
	}
	prompt := func(threadID string) string {
		if _, err := b.SendMessage(ctx, wire.SendMessageParams{ThreadID: threadID, Text: "go"}); err != nil {
			t.Fatalf("SendMessage failed: %v", err)
		}
		select {
		case captured := <-h.prompts:
			return captured
		case <-time.After(5 * time.Second):
			t.Fatal("等待捕获 prompt 超时")
			return ""
		}
	}

	if listed("lead-off") || strings.Contains(prompt("lead-off"), "swarm_delegate") {
		t.Fatal("DelegateTool 关闭时不应列出工具，提示词也不应带工具说明")
	}

	delegateTool.Store(true)
	if !listed("lead-on") {
		t.Fatal("DelegateTool 开启后应列出 swarm_delegate")
	}
	if captured := prompt("lead-on"); !strings.Contains(captured, swarm.LeadPolicy(false, "off", true)) {
		t.Fatalf("DelegateTool 开启后提示词应以工具为首选:\n%s", captured)
	}
}

func TestSwarmPlanGate(t *testing.T) {
	settings := swarm.Settings{DefaultTier: "edit", Tiers: []swarm.Tier{{ID: "edit"}, {ID: "read", ReadOnly: true}}}
	for _, tc := range []struct {
		tier, def, want string
		allowed         bool
	}{
		{"read", "edit", "read", true}, {"edit", "edit", "edit", false},
		{"missing", "edit", "missing", false}, {"", "edit", "read", true},
		{"", "read", "", true}, {"", "missing", "read", true},
	} {
		settings.DefaultTier = tc.def
		got, ok := swarmPlanGate(settings, swarm.Delegation{Tier: tc.tier})
		if ok != tc.allowed || got.Tier != tc.want {
			t.Errorf("tier=%q default=%q: got %q, %v", tc.tier, tc.def, got.Tier, ok)
		}
	}
	settings.Tiers = nil
	if _, ok := swarmPlanGate(settings, swarm.Delegation{}); ok {
		t.Fatal("没有只读档时必须拒绝")
	}
}

func TestSwarmPlanDispatcher(t *testing.T) {
	h, d := newRoundLimitHarness(t, 1)
	settings := swarm.Settings{DefaultTier: "edit", Tiers: []swarm.Tier{{ID: "edit"}, {ID: "read", ReadOnly: true}}}
	d.planGate = func(_ string, entry swarm.Delegation) (swarm.Delegation, bool) { return swarmPlanGate(settings, entry) }
	d.reset("lead")
	blocked := "```swarm\n[{\"prompt\":\"blocked\",\"tier\":\"edit\"}]\n```"
	d.feed("lead", "project", blocked)
	d.settleTurn("lead", "project", blocked)
	if h.rounds != 0 || h.dispatched != 0 || len(h.notices) != 1 || !strings.Contains(h.notices[0], "Plan 模式") {
		t.Fatalf("拒绝结果异常: %+v", h)
	}
	d.reset("lead")
	full := "```swarm\n[{\"prompt\":\"allowed\",\"tier\":\"read\"},{\"prompt\":\"blocked2\",\"tier\":\"edit\"}]\n```"
	d.feed("lead", "project", full)
	d.settleTurn("lead", "project", full)
	if h.rounds != 1 || h.dispatched != 1 || len(h.notices) != 2 {
		t.Fatalf("混合派发结果异常: %+v", h)
	}
}

func TestSwarmPlanSubmitAndSettle(t *testing.T) {
	h := newSubmitHarness(t, 0)
	d := h.dispatcher
	d.planGate = func(_ string, entry swarm.Delegation) (swarm.Delegation, bool) { return entry, entry.Tier != "edit" }
	d.reset("lead")
	got := d.submit("lead", "project", []swarm.Delegation{{Prompt: "blocked", Tier: "edit"}})
	if !strings.Contains(got, "plan mode allows only read-only") || h.rounds != 0 {
		t.Fatalf("工具拒绝结果异常: %q", got)
	}
	d.settleTurn("lead", "project", "```swarm\n[{\"prompt\":\"blocked\",\"tier\":\"edit\"},{\"prompt\":\"read\",\"tier\":\"read\"}]\n```")
	if got := h.dispatched(); len(got) != 1 || got[0] != "read" || h.rounds != 1 {
		t.Fatalf("补派结果异常: %v", got)
	}
	d.planGate = nil
	d.reset("lead")
	d.submit("lead", "project", []swarm.Delegation{{Prompt: "edit", Tier: "edit"}})
	d.settleTurn("lead", "project", "")
	if got := h.dispatched(); len(got) != 2 || got[1] != "edit" {
		t.Fatalf("非 Plan 派发异常: %v", got)
	}
}

func TestSwarmPlanSubmitRetryRead(t *testing.T) {
	h := newSubmitHarness(t, 0)
	d := h.dispatcher
	d.planGate = func(_ string, entry swarm.Delegation) (swarm.Delegation, bool) { return entry, entry.Tier == "read" }
	d.reset("lead")
	if got := d.submit("lead", "project", []swarm.Delegation{{Prompt: "same", Tier: "edit"}}); !strings.Contains(got, "plan mode allows only read-only") {
		t.Fatalf("编辑档应被拒绝: %q", got)
	}
	if got := d.submit("lead", "project", []swarm.Delegation{{Prompt: "same", Tier: "read"}}); got != "Started 1 bee(s)." {
		t.Fatalf("只读档应被派发: %q", got)
	}
	waitSwarm(t, func() bool { return len(h.dispatched()) == 1 })
	if got := h.dispatched(); got[0] != "same" {
		t.Fatalf("派发内容异常: %v", got)
	}
}

// Plan 模式拒绝的条目不进跨回合去重记录，主代理下一回合改派只读档时能正常启动。
func TestSwarmPlanStreamRetryRead(t *testing.T) {
	h, d := newRoundLimitHarness(t, 1)
	d.planGate = func(_ string, entry swarm.Delegation) (swarm.Delegation, bool) { return entry, entry.Tier == "read" }
	d.reset("lead")
	blocked := "```swarm\n[{\"prompt\":\"same\",\"tier\":\"edit\"}]\n```"
	d.feed("lead", "project", blocked)
	d.settleTurn("lead", "project", blocked)
	if h.dispatched != 0 || len(h.notices) != 1 || !strings.Contains(h.notices[0], "本回合有 1 个其他档的条目没有派发") {
		t.Fatalf("Plan 模式拒绝计数异常: %+v", h)
	}
	d.reset("lead")
	read := "```swarm\n[{\"prompt\":\"same\",\"tier\":\"read\"}]\n```"
	d.feed("lead", "project", read)
	d.settleTurn("lead", "project", read)
	if h.dispatched != 1 || len(h.notices) != 1 || h.rounds != 1 {
		t.Fatalf("下一回合改派只读档异常: %+v", h)
	}
}

func TestSubmitAndBlockRejectBadAcceptance(t *testing.T) {
	// 1. swarm_delegate 工具路径（submit）：坏条目被拒且工具结果带回原因，好条目照常启动，不占去重和轮次。
	sh := newSubmitHarness(t, 1)
	d := sh.dispatcher
	d.reset("lead")
	got := d.submit("lead", "project", []swarm.Delegation{
		{Task: "坏条目", Prompt: "bad prompt", Acceptance: []string{"go test ./internal/ui/"}},
		{Task: "好条目", Prompt: "good prompt", Acceptance: []string{"go test -run TestGood ./internal/ui/"}},
	})
	if !strings.HasPrefix(got, "Started 1 bee(s).") ||
		!strings.Contains(got, "条目「坏条目」未启动") ||
		!strings.Contains(got, "go test ./internal/ui/") ||
		!strings.Contains(got, "改正后重新派发") {
		t.Fatalf("submit 拒绝提示异常: %q", got)
	}
	waitSwarm(t, func() bool { return len(sh.dispatched()) == 1 })
	if sh.dispatched()[0] != "good prompt" {
		t.Fatalf("正常条目未启动: %v", sh.dispatched())
	}

	// 2. ```swarm 块路径（feed + settleTurn）：通过 reportBlockProblem 回送主代理，改正后可重派同 prompt。
	bh, bd := newRoundLimitHarness(t, 1)
	bd.reset("lead")
	badBlock := "```swarm\n[{\"task\":\"块坏条目\",\"prompt\":\"same prompt\",\"acceptance\":[\"timeout 10 go test -run X ./p\"]}]\n```"
	bd.feed("lead", "project", badBlock)
	bd.settleTurn("lead", "project", badBlock)
	if bh.dispatched != 0 || bh.rounds != 0 {
		t.Fatalf("被拒块条目不应消耗轮次或派发: %+v", bh)
	}
	if len(bh.notices) != 1 || !strings.Contains(bh.notices[0], "条目「块坏条目」未启动") || !strings.Contains(bh.notices[0], "macOS 没有 timeout 命令") {
		t.Fatalf("块拒绝回送异常: %+v", bh.notices)
	}
	bd.reset("lead")
	fixedBlock := "```swarm\n[{\"task\":\"块坏条目\",\"prompt\":\"same prompt\",\"acceptance\":[\"go test -run X ./p\"]}]\n```"
	bd.feed("lead", "project", fixedBlock)
	bd.settleTurn("lead", "project", fixedBlock)
	if bh.dispatched != 1 || bh.rounds != 1 {
		t.Fatalf("改正验收命令后应正常派发: %+v", bh)
	}
}

func TestSubmitAndBlockRerunAcceptance(t *testing.T) {
	var rerunErr error
	var rerunCalls int
	var dispatchedCalls int
	d := newSwarmStreamDispatcher(context.Background(),
		func(ctx context.Context, req swarm.SingleEntryRequest) (swarm.Bee, []string, error) {
			if req.Entry.Rerun {
				rerunCalls++
				if rerunErr != nil {
					return swarm.Bee{}, []string{rerunErr.Error()}, rerunErr
				}
				return swarm.Bee{}, []string{"已在 Aspen 保留的副本上重跑验收（1 条），结果稍后汇报"}, nil
			}
			dispatchedCalls++
			return swarm.Bee{}, nil, nil
		},
		func(string) int { return 0 },
		func(string) int { return 1 },
		func(string) (string, bool) { return t.TempDir(), true },
		func(string, string) error { return nil },
		func() int { return 0 },
	)
	var blockProblems []string
	d.report = func(_ string, text string) error {
		blockProblems = append(blockProblems, text)
		return nil
	}

	// 1. submit 路径：先测成功，再测后端拒绝错误回传到工具结果。
	d.reset("lead")
	got := d.submit("lead", "project", []swarm.Delegation{{
		Prompt: "重跑验收", ReworkOf: "Aspen", Rerun: true, Acceptance: []string{"go test -run TestFoo ./p"},
	}})
	if !strings.Contains(got, "已在 Aspen 保留的副本上重跑验收（1 条），结果稍后汇报") || dispatchedCalls != 0 || rerunCalls != 1 {
		t.Fatalf("submit 成功重跑结果异常: %q dispatched=%d rerun=%d", got, dispatchedCalls, rerunCalls)
	}

	d.reset("lead")
	rerunErr = errors.New("重跑验收失败：Aspen 的副本已回收，只能派新 bee 按补丁重做")
	got = d.submit("lead", "project", []swarm.Delegation{{
		Prompt: "重跑验收", ReworkOf: "Aspen", Rerun: true, Acceptance: []string{"go test -run TestFoo ./p"},
	}})
	if !strings.Contains(got, "重跑验收失败：Aspen 的副本已回收，只能派新 bee 按补丁重做") {
		t.Fatalf("submit 失败结果未带回错误: %q", got)
	}

	// 2. ```swarm 块路径：后端失败经 reportBlockProblem 回送主代理。
	d.reset("lead")
	block := "```swarm\n[{\"prompt\":\"块重跑\",\"reworkOf\":\"Aspen\",\"rerun\":true,\"acceptance\":[\"go test -run TestFoo ./p\"]}]\n```"
	d.feed("lead", "project", block)
	d.settleTurn("lead", "project", block)
	if len(blockProblems) != 1 || !strings.Contains(blockProblems[0], "重跑验收失败：Aspen 的副本已回收，只能派新 bee 按补丁重做") {
		t.Fatalf("块路径未回送重跑失败原因: %+v", blockProblems)
	}
	if dispatchedCalls != 0 {
		t.Fatalf("重跑不应触发普通派发: %d", dispatchedCalls)
	}
}
