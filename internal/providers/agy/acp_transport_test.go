package agy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"mcode/internal/harness"
	"mcode/internal/providers/acp"
	"mcode/internal/providers/multiaccount"
)

func TestAgyChannelSelect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ setting, version, want string }{
		{ChannelAuto, "1.5.7", ChannelPrintMode}, {ChannelAuto, "1.5.8", ChannelACP}, {ChannelAuto, "1.6.0", ChannelACP},
		{ChannelAuto, "", ChannelPrintMode}, {ChannelAuto, "unknown", ChannelPrintMode},
		{ChannelACP, "1.5.7", ChannelPrintMode}, {ChannelACP, "", ChannelPrintMode}, {ChannelACP, "invalid", ChannelPrintMode},
		{ChannelACP, "v1.5.8-beta", ChannelACP}, {ChannelACP, "1.10.0", ChannelACP},
		{ChannelPrintMode, "1.5.8", ChannelPrintMode}, {ChannelPrintMode, "", ChannelPrintMode}, {"other", "1.5.8", ChannelPrintMode},
	} {
		t.Run(tc.setting+"/"+tc.version, func(t *testing.T) {
			if got := selectChannel(tc.setting, tc.version); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAgyACPLaunchArgs(t *testing.T) {
	t.Parallel()
	want := []string{"acp", "--disable-telemetry"}
	for _, mode := range []harness.PermissionMode{harness.ModeFullAccess, harness.ModeAuto, harness.ModeAutoAcceptEdits, harness.ModeSupervised, harness.ModeReadOnly} {
		if got := acp.LaunchArgs(acp.AgentAgy, mode); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v, want %v", mode, got, want)
		}
	}
	if acp.DefaultBinary(acp.AgentAgy) != "agy" || acp.DisplayName(acp.AgentAgy) != "Antigravity" {
		t.Fatal("invalid agy agent metadata")
	}
	for _, agent := range acp.Agents() {
		if agent == acp.AgentAgy {
			t.Fatal("agy must not be registered as another ACP provider")
		}
	}
}

const acpRateLimitError = "RESOURCE_EXHAUSTED 429 reset at 2099-01-02T03:04:05Z"

func runFakeAgyACP() {
	reader := bufio.NewReader(os.Stdin)
	var setup map[string]any
	failure := os.Getenv("MCODE_TEST_ACP_FAILURE")
	limited := failure != "" && (os.Getenv("MCODE_TEST_ROUTED_ACCOUNT") == "account-0" || failure == "always")
	promptCount := 0
	currentMode := ""
	rateLimit := func(id any) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": 429, "message": acpRateLimitError}})
	}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(line, &msg)
		params, _ := msg["params"].(map[string]any)
		result := map[string]any{}
		switch msg["method"] {
		case "initialize":
			result["agentCapabilities"] = map[string]any{"loadSession": true, "mcpCapabilities": map[string]any{"http": true}, "sessionCapabilities": map[string]any{"resume": map[string]any{}, "close": map[string]any{}}}
		case "session/new", "session/resume":
			if limited && failure == "setup" {
				rateLimit(msg["id"])
				return
			}
			setup = params
			result["sessionId"] = "new-session"
			if failure == "unknown_model" {
				result["configOptions"] = []map[string]any{
					{"id": "model", "category": "model", "currentValue": "default-model"},
				}
			}
			if os.Getenv("MCODE_TEST_ACP_PLAN_FILE") != "" {
				currentMode = "default"
				result["modes"] = map[string]any{
					"currentModeId":  currentMode,
					"availableModes": []map[string]any{{"id": "default"}, {"id": "plan"}, {"id": "auto"}, {"id": "accept-edits"}},
				}
			}
		case "session/set_mode":
			if modeID, _ := params["modeId"].(string); modeID != "" {
				currentMode = modeID
			}
		case "session/set_config_option":
			if limited && failure == "unknown_model" {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"error":   map[string]any{"code": -32602, "message": "Unknown model ID: x"},
				})
				return
			}
			if id, _ := params["configId"].(string); id != "" {
				result["configOptions"] = []map[string]any{
					{"id": id, "category": id, "currentValue": params["value"]},
				}
			}
		case "session/prompt":
			promptCount++
			if os.Getenv("MCODE_TEST_ACP_PLAN_FILE") != "" {
				handleFakeAgyACPPlanPrompt(msg["id"], currentMode, params)
				continue
			}
			if os.Getenv("MCODE_TEST_ACP_HOLD") != "" {
				if promptCount > 1 {
					rateLimit(msg["id"])
					return
				}
				for _, update := range []map[string]any{
					{"sessionUpdate": "tool_call", "toolCallId": "hold-tool", "name": "run_command"},
					{"sessionUpdate": "tool_call_update", "toolCallId": "hold-tool", "status": "completed", "output": "Tool is running as a background task with task id: 12345678-1234-1234-1234-123456789abc/task-1"},
				} {
					_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": update}})
				}
				result["stopReason"] = "end_turn"
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
				continue
			}
			if limited && failure != "after_text" {
				if failure == "after_tool" {
					_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "tool-1", "name": "bash"}}})
				}
				rateLimit(msg["id"])
				return
			}
			payload, _ := json.Marshal(map[string]any{"args": os.Args[1:], "prompt": params["prompt"], "setup": setup, "env": os.Getenv("MCODE_TEST_ROUTED_ACCOUNT"), "firstOnly": os.Getenv("MCODE_TEST_ACP_FIRST_ONLY")})
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": string(payload)}}}})
			if limited {
				rateLimit(msg["id"])
				return
			}
			result["stopReason"] = "end_turn"
		case "session/close":
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
			return
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
	}
}

type acpTestRouter struct {
	mu         sync.Mutex
	bound      map[string]string
	uses, ends int
	routes     int
}

func (r *acpTestRouter) Route(session, _ string, _ []string) (string, []string, []string, error) {
	r.mu.Lock()
	r.routes++
	r.mu.Unlock()
	return session, []string{fakeAgyEnv + "=acp_transport", "MCODE_TEST_ROUTED_ACCOUNT=" + session}, []string{"--gemini_dir", session}, nil
}
func (r *acpTestRouter) RouteExplicit(session, account, _ string) ([]string, []string, error) {
	r.mu.Lock()
	r.routes++
	r.mu.Unlock()
	return []string{fakeAgyEnv + "=acp_transport", "MCODE_TEST_ROUTED_ACCOUNT=" + account}, []string{"--gemini_dir", account}, nil
}
func (r *acpTestRouter) Bind(session, account string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bound[session] = account
}
func (r *acpTestRouter) MarkLimited(string, string, time.Time) bool { panic("ACP must not retry") }
func (r *acpTestRouter) QuotaUse(string) func() {
	r.mu.Lock()
	r.uses++
	r.mu.Unlock()
	return func() { r.mu.Lock(); r.ends++; r.mu.Unlock() }
}

func TestAgyACPRouting(t *testing.T) {
	router := &acpTestRouter{bound: map[string]string{}}
	h := New(Options{Binary: os.Args[0], Router: router, BuiltinMCP: harness.BuiltinMCP{}})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			account := fmt.Sprintf("account-%d", i)
			originalEnv := []string{"MCODE_TEST_ROUTED_ACCOUNT=original"}
			turn := harness.Turn{SessionID: account, ThreadID: account, Prompt: "raw prompt", Env: originalEnv, WorkDir: t.TempDir()}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := h.runACP(ctx, turn, func(event harness.Event) {
				if event.Type == harness.EventSession {
					child := h.acp.ProcessRootPIDs(account)
					if len(child) != 1 {
						t.Errorf("active ACP roots = %v", child)
						return
					}
					h.roots.Set(account, os.Getpid())
					if roots := h.ProcessRootPIDs(account); len(roots) != 2 {
						t.Errorf("root union = %v", roots)
					}
					h.roots.Clear(account, os.Getpid())
					h.roots.Set(account, child[0])
					if roots := h.ProcessRootPIDs(account); len(roots) != 1 {
						t.Errorf("duplicate roots = %v", roots)
					}
					h.roots.Clear(account, child[0])
				}
				if event.Type != harness.EventText {
					return
				}
				var got struct {
					Args   []string `json:"args"`
					Prompt []struct {
						Text string `json:"text"`
					} `json:"prompt"`
					Env string `json:"env"`
				}
				if err := json.Unmarshal([]byte(event.Text), &got); err != nil {
					t.Error(err)
					return
				}
				if !reflect.DeepEqual(got.Args, []string{"acp", "--disable-telemetry", "--gemini_dir", account}) || got.Env != account || len(got.Prompt) != 1 || got.Prompt[0].Text != prependIsolatedHomeHint(account, "raw prompt") {
					t.Errorf("wrong ACP route: %+v", got)
				}
				if turn.Prompt != "raw prompt" {
					t.Errorf("mutated caller prompt: %q", turn.Prompt)
				}
			})
			if err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(originalEnv, []string{"MCODE_TEST_ROUTED_ACCOUNT=original"}) {
				t.Error("mutated caller env")
			}
			if roots := h.ProcessRootPIDs(account); len(roots) > 0 {
				t.Errorf("leaked process roots: %v", roots)
			}
		}(i)
	}
	wg.Wait()
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.uses != 2 || router.ends != 2 || router.bound["account-0"] != "account-0" || router.bound["account-1"] != "account-1" {
		t.Fatalf("quota/bind mismatch: %+v", router)
	}
	count := 0
	h.acpArgs.Range(func(_, _ any) bool { count++; return true })
	if count != 0 {
		t.Fatal("leaked route args")
	}
}

func TestAgyACPVersionCache(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "agy")
	count := filepath.Join(dir, "count")
	version := filepath.Join(dir, "version")
	if err := os.WriteFile(version, []byte("1.5.8"), 0600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\necho call >> %q\ncat %q\n", count, version)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	h := New(Options{Binary: bin})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := h.channel(ctx); got != ChannelACP {
				t.Errorf("channel = %s", got)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "call") != 1 {
		t.Fatalf("probes = %s", data)
	}
	if err := os.WriteFile(version, []byte("1.5.7"), 0600); err != nil {
		t.Fatal(err)
	}
	h.ResetVersionCache()
	if got := h.channel(ctx); got != ChannelPrintMode {
		t.Fatalf("after reset = %s", got)
	}
	data, err = os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "call") != 2 {
		t.Fatalf("probes after reset = %s", data)
	}
}

type acpRetryRouter struct {
	mu        sync.Mutex
	router    *multiaccount.Router
	config    multiaccount.Config
	accounts  []multiaccount.AccountState
	geminiDir string
	routes    []string
	excludes  [][]string
	bindings  [][2]string
	limited   [][2]string
	resetAt   time.Time
	uses      []string
	ends      []string
	onRoute   func()
}

func newACPRetryRouter(affinity multiaccount.Affinity) *acpRetryRouter {
	r := &acpRetryRouter{
		router: multiaccount.NewRouter(nil, nil),
		config: multiaccount.Config{Strategy: multiaccount.StrategyOrder, Affinity: affinity},
	}
	for i := 0; i <= maxRetries; i++ {
		r.accounts = append(r.accounts, multiaccount.AccountState{ID: fmt.Sprintf("account-%d", i), Enabled: true})
	}
	return r
}

func (r *acpRetryRouter) Route(session, _ string, exclude []string) (string, []string, []string, error) {
	if r.onRoute != nil {
		r.onRoute()
	}
	r.mu.Lock()
	r.routes = append(r.routes, session)
	r.excludes = append(r.excludes, append([]string(nil), exclude...))
	r.mu.Unlock()
	id, err := r.router.Pick(r.config, session, r.accounts, exclude, time.Now())
	if err != nil {
		return "", nil, nil, err
	}
	env := []string{"MCODE_TEST_ROUTED_ACCOUNT=" + id}
	if id == "account-0" {
		env = append(env, "MCODE_TEST_ACP_FIRST_ONLY=first-account")
	}
	dir := id
	if r.geminiDir != "" {
		dir = filepath.Join(r.geminiDir, id)
	}
	return id, env, []string{"--gemini_dir", dir}, nil
}

func (r *acpRetryRouter) RouteExplicit(session, account, _ string) ([]string, []string, error) {
	if r.onRoute != nil {
		r.onRoute()
	}
	r.mu.Lock()
	r.routes = append(r.routes, session)
	r.mu.Unlock()
	env := []string{"MCODE_TEST_ROUTED_ACCOUNT=" + account}
	dir := account
	if r.geminiDir != "" {
		dir = filepath.Join(r.geminiDir, account)
	}
	return env, []string{"--gemini_dir", dir}, nil
}

func (r *acpRetryRouter) Bind(session, account string) {
	r.mu.Lock()
	r.bindings = append(r.bindings, [2]string{session, account})
	r.mu.Unlock()
	_ = r.router.Bind(session, account)
}

func (r *acpRetryRouter) MarkLimited(session, account string, resetAt time.Time) bool {
	r.mu.Lock()
	r.limited = append(r.limited, [2]string{session, account})
	r.resetAt = resetAt
	r.mu.Unlock()
	r.router.MarkLimited(account, resetAt, time.Now())
	return r.config.Affinity != multiaccount.AffinityStrict
}

func (r *acpRetryRouter) QuotaUse(account string) func() {
	r.mu.Lock()
	r.uses = append(r.uses, account)
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		r.ends = append(r.ends, account)
		r.mu.Unlock()
	}
}

func newACPRetryHarness(t *testing.T, affinity multiaccount.Affinity) (*Harness, *acpRetryRouter) {
	t.Helper()
	r := newACPRetryRouter(affinity)
	h := New(Options{Binary: os.Args[0], Router: r})
	r.onRoute = func() {
		t.Helper()
		checkACPRetryCleanup(t, h, r)
	}
	t.Cleanup(func() { checkACPRetryCleanup(t, h, r) })
	return h, r
}

func checkACPRetryCleanup(t *testing.T, h *Harness, r *acpRetryRouter) {
	t.Helper()
	h.acpArgs.Range(func(key, value any) bool {
		t.Errorf("leaked route args: %v = %v", key, value)
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	if !reflect.DeepEqual(r.uses, r.ends) {
		t.Errorf("quota lifecycle: uses %v, ends %v", r.uses, r.ends)
	}
}

func acpRetryTurn(t *testing.T, failure string) harness.Turn {
	t.Helper()
	return harness.Turn{
		ThreadID: "thread-1", Prompt: "hello", WorkDir: t.TempDir(),
		Env: []string{fakeAgyEnv + "=acp_transport", "MCODE_TEST_ACP_FAILURE=" + failure, "MCODE_TEST_ROUTED_ACCOUNT=original"},
	}
}

func TestRunACPRateLimitSwitchesAccount(t *testing.T) {
	for _, failure := range []string{"before_stream", "setup"} {
		t.Run(failure, func(t *testing.T) {
			h, router := newACPRetryHarness(t, multiaccount.AffinitySession)
			turn := acpRetryTurn(t, failure)
			originalEnv := append([]string(nil), turn.Env...)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var text strings.Builder
			if err := h.runACP(ctx, turn, func(event harness.Event) {
				if event.Type == harness.EventText {
					text.WriteString(event.Text)
				}
			}); err != nil {
				t.Fatal(err)
			}
			wantRetryKey := "new-session"
			if failure == "setup" {
				wantRetryKey = "thread:thread-1"
			}
			if !reflect.DeepEqual(router.routes, []string{"thread:thread-1", wantRetryKey}) ||
				!reflect.DeepEqual(router.excludes, [][]string{nil, {"account-0"}}) ||
				!reflect.DeepEqual(router.uses, []string{"account-0", "account-1"}) {
				t.Fatalf("routes %v, excludes %v, uses %v", router.routes, router.excludes, router.uses)
			}
			if !reflect.DeepEqual(router.limited, [][2]string{{wantRetryKey, "account-0"}}) || router.resetAt.Format(time.RFC3339) != "2099-01-02T03:04:05Z" {
				t.Fatalf("limited %v, reset %v", router.limited, router.resetAt)
			}
			var payload struct {
				Args      []string       `json:"args"`
				Env       string         `json:"env"`
				FirstOnly string         `json:"firstOnly"`
				Setup     map[string]any `json:"setup"`
			}
			if err := json.Unmarshal([]byte(text.String()), &payload); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(payload.Args, []string{"acp", "--disable-telemetry", "--gemini_dir", "account-1"}) || payload.Env != "account-1" || payload.FirstOnly != "" {
				t.Fatalf("retry account params: %+v", payload)
			}
			if failure == "before_stream" && payload.Setup["sessionId"] != "new-session" {
				t.Fatalf("retry did not resume session: %v", payload.Setup)
			}
			for _, key := range []string{"thread:thread-1", "new-session"} {
				if id, _ := router.router.Bound(key); id != "account-1" {
					t.Errorf("binding %s = %s", key, id)
				}
			}
			if !reflect.DeepEqual(turn.Env, originalEnv) {
				t.Error("mutated caller env")
			}
		})
	}
}

func TestRunACPRateLimitStrictKeepsAccount(t *testing.T) {
	h, router := newACPRetryHarness(t, multiaccount.AffinityStrict)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.runACP(ctx, acpRetryTurn(t, "before_stream"), nil)
	if err == nil || !strings.Contains(err.Error(), "严格绑定模式下不会换号") || !strings.Contains(err.Error(), acpRateLimitError) || !strings.Contains(err.Error(), "重置时间：2099-01-02T03:04:05Z") {
		t.Fatalf("strict error = %v", err)
	}
	if len(router.routes) != 1 || !reflect.DeepEqual(router.uses, []string{"account-0"}) || !reflect.DeepEqual(router.limited, [][2]string{{"new-session", "account-0"}}) {
		t.Fatalf("routes %v, uses %v, limited %v", router.routes, router.uses, router.limited)
	}
}

func TestRunACPRateLimitAfterStreamNoRetry(t *testing.T) {
	for _, tc := range []struct {
		failure string
		event   harness.EventType
	}{{"after_text", harness.EventText}, {"after_tool", harness.EventToolStart}} {
		t.Run(tc.failure, func(t *testing.T) {
			h, router := newACPRetryHarness(t, multiaccount.AffinitySession)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var got int
			err := h.runACP(ctx, acpRetryTurn(t, tc.failure), func(event harness.Event) {
				if event.Type == tc.event {
					got++
				}
			})
			if err == nil || !strings.Contains(err.Error(), acpRateLimitError) || strings.Contains(err.Error(), "严格绑定") {
				t.Fatalf("streamed error = %v", err)
			}
			if got != 1 || len(router.routes) != 1 || len(router.uses) != 1 || len(router.limited) != 1 {
				t.Fatalf("events %d, routes %v, uses %v, limited %v", got, router.routes, router.uses, router.limited)
			}
		})
	}
}

func TestRunACPRateLimitRetryLimit(t *testing.T) {
	h, router := newACPRetryHarness(t, multiaccount.AffinitySession)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.runACP(ctx, acpRetryTurn(t, "always"), nil)
	if err == nil || !strings.Contains(err.Error(), acpRateLimitError) {
		t.Fatalf("retry limit error = %v", err)
	}
	if len(router.routes) != maxRetries+1 || len(router.uses) != maxRetries+1 || len(router.limited) != maxRetries+1 {
		t.Fatalf("routes %v, uses %v, limited %v", router.routes, router.uses, router.limited)
	}
	for i, exclude := range router.excludes {
		if !reflect.DeepEqual(exclude, router.uses[:i]) && !(i == 0 && len(exclude) == 0) {
			t.Errorf("attempt %d exclude %v, used %v", i, exclude, router.uses[:i])
		}
	}
}

func TestRunACPRateLimitNoAccountCleanup(t *testing.T) {
	h, router := newACPRetryHarness(t, multiaccount.AffinitySession)
	router.accounts = router.accounts[:1]
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.runACP(ctx, acpRetryTurn(t, "before_stream"), nil)
	if !errors.Is(err, multiaccount.ErrNoAccount) {
		t.Fatalf("routing error = %v", err)
	}
	if len(router.routes) != 2 || !reflect.DeepEqual(router.uses, []string{"account-0"}) || len(router.limited) != 1 {
		t.Fatalf("routes %v, uses %v, limited %v", router.routes, router.uses, router.limited)
	}
}

func TestAgyThreadAffinityKey(t *testing.T) {
	t.Parallel()
	for _, channels := range [][2]string{
		{ChannelACP, ChannelACP}, {ChannelPrintMode, ChannelPrintMode},
		{ChannelACP, ChannelPrintMode}, {ChannelPrintMode, ChannelACP},
	} {
		for _, session := range []string{"", "legacy-session"} {
			t.Run(channels[0]+"/"+channels[1]+"/"+session, func(t *testing.T) {
				t.Parallel()
				h, router := newACPRetryHarness(t, multiaccount.AffinityStrict)
				turn := acpRetryTurn(t, "")
				turn.SessionID = session
				want := "account-0"
				firstKey := "thread:thread-1"
				if session != "" {
					firstKey, want = session, "account-1"
					_ = router.router.Bind(session, want)
				}
				for i, channel := range channels {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					var err error
					if channel == ChannelACP {
						turn.Env = []string{fakeAgyEnv + "=acp_transport"}
						err = h.runACP(ctx, turn, nil)
					} else {
						h.opts.Channel = func() string { return ChannelPrintMode }
						turn.Env = []string{fakeAgyEnv + "=normal"}
						err = h.Run(ctx, turn, nil)
					}
					cancel()
					if err != nil {
						t.Fatal(err)
					}
					if id, _ := router.router.Bound("thread:thread-1"); id != want {
						t.Fatalf("thread binding = %s, want %s", id, want)
					}
					if i == 0 {
						// 模拟 provider 会话失效自愈，同时让原账号冷却且停用。
						turn.SessionID = ""
						router.router.MarkLimited(want, time.Now().Add(time.Hour), time.Now())
						for j := range router.accounts {
							if router.accounts[j].ID == want {
								router.accounts[j].Enabled = false
							}
						}
					}
				}
				if !reflect.DeepEqual(router.routes, []string{firstKey, "thread:thread-1"}) || !reflect.DeepEqual(router.uses, []string{want, want}) {
					t.Fatalf("routes %v, accounts %v", router.routes, router.uses)
				}
				var threadBinds int
				for _, binding := range router.bindings {
					if binding == [2]string{"thread:thread-1", want} {
						threadBinds++
					}
				}
				if threadBinds != 2 {
					t.Fatalf("thread Bind calls = %d", threadBinds)
				}
			})
		}
	}
}

func TestAgyHoldForwardsInterfaces(t *testing.T) {
	h := New(Options{Binary: os.Args[0]})
	var _ io.Closer = h
	var _ harness.UnsolicitedTurnNotifier = h
	var _ harness.ProcessExitNotifier = h
	var _ harness.ThreadEvictor = h
	var _ interface{ HasUnsolicitedOutput(string) bool } = h
	h.SetUnsolicitedTurnHandler(func(string) {})
	h.SetProcessExitHandler(func(string, time.Time) {})
	if h.HasUnsolicitedOutput("missing-thread") {
		t.Fatal("空线程不应有待上报输出")
	}
	h.EvictThread("missing-thread")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Run(ctx, harness.Turn{ThreadID: "missing-thread", Attach: true}, func(harness.Event) { t.Error("空 Attach 不应产生事件") }); err != nil {
		t.Fatal(err)
	}
	h.acpAccounts.Store("thread", "account")
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.acpAccounts.Load("thread"); ok {
		t.Fatal("Close 未清除 ACP 常驻账号映射")
	}
}

func TestRunACPResidentDoesNotRetry(t *testing.T) {
	// MarkLimited 会 panic；复用轮遇到限流时必须直接返回，不能进入换号路径。
	router := &acpTestRouter{bound: map[string]string{}}
	h := New(Options{Binary: os.Args[0], Router: router})
	turn := harness.Turn{ThreadID: "resident-rate", SessionID: "bound-account", WorkDir: t.TempDir(), Env: []string{"MCODE_TEST_ACP_HOLD=1"}}
	t.Cleanup(func() { h.EvictThread(turn.ThreadID) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.runACP(ctx, turn, func(harness.Event) {}); err != nil {
		t.Fatal(err)
	}
	if len(h.ProcessRootPIDs(turn.ThreadID)) != 1 {
		t.Fatal("没有保留 ACP 进程")
	}
	if err := h.runACP(ctx, turn, func(harness.Event) {}); err == nil || !isRateLimited(err.Error()) {
		t.Fatalf("复用轮限流错误 = %v", err)
	}
	if len(h.ProcessRootPIDs(turn.ThreadID)) != 0 {
		t.Fatal("限流失败后未清理进程")
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.routes != 1 || router.uses != 2 || router.ends != 2 {
		t.Fatalf("复用轮重新路由或遗漏额度统计: routes=%d, uses=%d, ends=%d", router.routes, router.uses, router.ends)
	}
}

func TestRunACPAttachRateLimitReturnsError(t *testing.T) {
	h, router := newACPRetryHarness(t, multiaccount.AffinitySession)
	geminiDir := t.TempDir()
	router.geminiDir = geminiDir
	const threadID = "attach-rate-limit"
	t.Cleanup(func() { h.EvictThread(threadID) })

	unsolicitedCh := make(chan string, 4)
	exitCh := make(chan string, 4)
	h.SetUnsolicitedTurnHandler(func(id string) { unsolicitedCh <- id })
	h.SetProcessExitHandler(func(id string, _ time.Time) { exitCh <- id })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	turn1 := harness.Turn{
		ThreadID: threadID, Prompt: "start", WorkDir: t.TempDir(),
		Env: []string{fakeAgyEnv + "=acp_transport", "MCODE_TEST_ACP_HOLD=1", "MCODE_TEST_ROUTED_ACCOUNT=original"},
	}
	if err := h.runACP(ctx, turn1, func(harness.Event) {}); err != nil {
		t.Fatal(err)
	}
	pids := h.ProcessRootPIDs(threadID)
	if len(pids) != 1 {
		t.Fatalf("round 1 roots = %v", pids)
	}

	messagesDir := filepath.Join(geminiDir, "account-0", "antigravity-cli", "brain", "new-session", ".system_generated", "messages")
	if err := os.MkdirAll(messagesDir, 0700); err != nil {
		t.Fatal(err)
	}
	msgData, err := json.Marshal(map[string]any{
		"content":   `Task id "12345678-1234-1234-1234-123456789abc/task-1" finished with result:` + "\nok",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(messagesDir, "1.json"), msgData, 0600); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-unsolicitedCh:
		if got != threadID {
			t.Fatalf("unsolicited thread = %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等待后台任务完成通知超时")
	}
	if !h.HasUnsolicitedOutput(threadID) {
		t.Fatal("应存在待上报的后台任务")
	}

	proc, err := os.FindProcess(pids[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-exitCh:
		if got != threadID {
			t.Fatalf("exit thread = %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等待常驻进程退出通知超时")
	}
	if h.acp.HasResidentProcess(threadID) || len(h.ProcessRootPIDs(threadID)) != 0 || !h.HasUnsolicitedOutput(threadID) {
		t.Fatalf("前置状态不符合预期: resident=%v roots=%v unsolicited=%v", h.acp.HasResidentProcess(threadID), h.ProcessRootPIDs(threadID), h.HasUnsolicitedOutput(threadID))
	}

	attachTurn := harness.Turn{
		ThreadID: threadID, SessionID: "new-session", Attach: true, WorkDir: turn1.WorkDir,
		Env: []string{fakeAgyEnv + "=acp_transport", "MCODE_TEST_ACP_FAILURE=before_stream", "MCODE_TEST_ROUTED_ACCOUNT=original"},
	}
	var replayedEnded int
	err = h.runACP(ctx, attachTurn, func(event harness.Event) {
		if event.Type == harness.EventBackgroundTaskEnd {
			replayedEnded++
		}
	})
	if err == nil || !strings.Contains(err.Error(), acpRateLimitError) || strings.Contains(err.Error(), "严格绑定") {
		t.Fatalf("Attach 限流错误 = %v", err)
	}
	if replayedEnded != 1 {
		t.Fatalf("replayedEnded = %d, want 1", replayedEnded)
	}
	if !reflect.DeepEqual(router.routes, []string{"thread:" + threadID, "new-session"}) ||
		!reflect.DeepEqual(router.excludes, [][]string{nil, nil}) ||
		!reflect.DeepEqual(router.uses, []string{"account-0", "account-0"}) ||
		!reflect.DeepEqual(router.limited, [][2]string{{"new-session", "account-0"}}) {
		t.Fatalf("Attach 限流不应换号重试: routes=%v excludes=%v uses=%v limited=%v", router.routes, router.excludes, router.uses, router.limited)
	}
	if h.HasUnsolicitedOutput(threadID) || len(h.ProcessRootPIDs(threadID)) != 0 {
		t.Fatalf("Attach 失败后状态异常: unsolicited=%v roots=%v", h.HasUnsolicitedOutput(threadID), h.ProcessRootPIDs(threadID))
	}
}

func TestRunACPUnknownModelSwitchesAccount(t *testing.T) {
	h, router := newACPRetryHarness(t, multiaccount.AffinityStrict)
	turn := acpRetryTurn(t, "unknown_model")
	turn.Model = "x"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var text strings.Builder
	if err := h.runACP(ctx, turn, func(event harness.Event) {
		if event.Type == harness.EventText {
			text.WriteString(event.Text)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(router.routes, []string{"thread:thread-1", ""}) ||
		!reflect.DeepEqual(router.excludes, [][]string{nil, {"account-0"}}) ||
		!reflect.DeepEqual(router.uses, []string{"account-0", "account-1"}) {
		t.Fatalf("routes %v, excludes %v, uses %v", router.routes, router.excludes, router.uses)
	}
	if len(router.limited) != 0 {
		t.Fatalf("must not call MarkLimited: %v", router.limited)
	}
	var payload struct {
		Env   string         `json:"env"`
		Setup map[string]any `json:"setup"`
	}
	if err := json.Unmarshal([]byte(text.String()), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Env != "account-1" || payload.Setup["sessionId"] != nil {
		t.Fatalf("retry payload = %+v", payload)
	}
	for _, key := range []string{"thread:thread-1", "new-session"} {
		if id, _ := router.router.Bound(key); id != "account-1" {
			t.Errorf("binding %s = %s, want account-1", key, id)
		}
	}
	if h.acp.HasResidentProcess(turn.ThreadID) {
		t.Fatal("unexpected resident process after retry")
	}
}

func TestRunACPUnknownModelExistingSessionNoRetry(t *testing.T) {
	h, router := newACPRetryHarness(t, multiaccount.AffinityStrict)
	turn := acpRetryTurn(t, "unknown_model")
	turn.SessionID = "existing-session"
	turn.Model = "x"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.runACP(ctx, turn, nil)
	if err == nil || !strings.Contains(err.Error(), "Unknown model ID: x") {
		t.Fatalf("existing session error = %v", err)
	}
	if !reflect.DeepEqual(router.routes, []string{"existing-session"}) ||
		!reflect.DeepEqual(router.uses, []string{"account-0"}) ||
		len(router.limited) != 0 {
		t.Fatalf("routes %v, uses %v, limited %v", router.routes, router.uses, router.limited)
	}
}

func TestRunACPUnknownModelNoAccountReturnsOriginalError(t *testing.T) {
	h, router := newACPRetryHarness(t, multiaccount.AffinityStrict)
	router.accounts = router.accounts[:1]
	turn := acpRetryTurn(t, "unknown_model")
	turn.Model = "x"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.runACP(ctx, turn, nil)
	if err == nil || !strings.Contains(err.Error(), "Unknown model ID: x") || errors.Is(err, multiaccount.ErrNoAccount) {
		t.Fatalf("no-account fallback error = %v", err)
	}
	if !reflect.DeepEqual(router.routes, []string{"thread:thread-1", ""}) ||
		!reflect.DeepEqual(router.excludes, [][]string{nil, {"account-0"}}) ||
		!reflect.DeepEqual(router.uses, []string{"account-0"}) ||
		len(router.limited) != 0 {
		t.Fatalf("routes %v, excludes %v, uses %v, limited %v", router.routes, router.excludes, router.uses, router.limited)
	}
}
