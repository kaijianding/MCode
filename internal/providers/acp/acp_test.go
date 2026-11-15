package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"mcode/internal/harness"
	"mcode/internal/providers/loginenv"
)

const fakeACPEnv = "MCODE_TEST_FAKE_ACP"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeACPEnv); mode != "" {
		runFakeACP(mode)
		return
	}
	// 测试环境使用假 Agent，无需真实启动间隔，避免用例间相互等待。
	launchSpacing = 0
	os.Exit(m.Run())
}

// runFakeACP 实现模拟的 ACP CLI 子进程。
func runFakeACP(mode string) {
	if logPath := os.Getenv("MCODE_TEST_FAKE_ACP_ARGS_LOG"); logPath != "" {
		// 探测用例靠这条记录断言启动参数与环境变量确实传到了子进程。
		record := "argv=" + strings.Join(os.Args, " ") + "\n" +
			"marker=" + os.Getenv("MCODE_TEST_FAKE_ACP_ENV_MARKER") + "\n"
		_ = os.WriteFile(logPath, []byte(record), 0o600)
	}
	reader := bufio.NewReader(os.Stdin)

	readMsg := func() (map[string]any, error) {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, err
		}
		return msg, nil
	}

	writeMsg := func(v any) {
		data, _ := json.Marshal(v)
		data = append(data, '\n')
		_, _ = os.Stdout.Write(data)
	}

	// loadAttempts drives the load_fail_once mode: only the first session/load fails.
	loadAttempts := 0

	for {
		msg, err := readMsg()
		if err != nil {
			break
		}

		method, _ := msg["method"].(string)
		id := msg["id"]

		switch method {
		case "initialize":
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": 1,
					"agentCapabilities": map[string]any{
						"loadSession": mode != "no_load",
					},
					"authMethods": []any{},
				},
			})

		case "session/new":
			result := map[string]any{
				"sessionId": "fake-session-001",
			}
			if mode == "load_fail" || mode == "load_fail_once" {
				// A distinct ID lets the tests see that a new session was created.
				result["sessionId"] = "fake-session-new"
			}
			if mode == "catalog" {
				result["configOptions"] = []any{map[string]any{
					"id":           "model",
					"name":         "Model",
					"category":     "model",
					"type":         "select",
					"currentValue": "vendor/probed-model",
					"options": []any{
						map[string]any{"value": "vendor/probed-model", "name": "probed-model"},
					},
				}}
			}
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  result,
			})

		case "session/load":
			fail := mode == "load_fail"
			if mode == "load_fail_once" {
				loadAttempts++
				fail = loadAttempts == 1
			}
			if fail {
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"error": map[string]any{
						"code":    -32603,
						"message": "invalid rpi ready frame: EOF while parsing a value at line 1 column 0",
					},
				})
				continue
			}
			if mode == "load_replay" {
				updates := []map[string]any{
					{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": "REPLAYED-HISTORY"}},
					{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"text": "REPLAYED-THOUGHT"}},
					{"sessionUpdate": "tool_call", "toolCallId": "replay-call", "name": "bash"},
					{"sessionUpdate": "tool_call_update", "toolCallId": "replay-call", "status": "completed", "output": "replayed"},
					{"sessionUpdate": "tool_call_update", "path": "replayed.txt"},
					{"sessionUpdate": "usage_update", "used": 50, "size": 1000},
				}
				for _, update := range updates {
					writeMsg(map[string]any{
						"jsonrpc": "2.0",
						"method":  "session/update",
						"params":  map[string]any{"update": update},
					})
				}
			}
			loaded := "fake-session-001"
			if params, ok := msg["params"].(map[string]any); ok {
				if sid, ok := params["sessionId"].(string); ok && sid != "" {
					loaded = sid
				}
			}
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"sessionId": loaded,
				},
			})

		case "session/prompt":
			if mode == "hang_until_cancel" {
				// 挂起不回复，等待被取消或者接收 cancel 通知
				for {
					next, err := readMsg()
					if err != nil {
						os.Exit(0)
					}
					if next["method"] == "session/cancel" {
						time.Sleep(100 * time.Millisecond)
						os.Exit(0)
					}
				}
			}

			// 1. 发送 text chunk 通知
			if strings.Contains(mode, "usage") {
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"update": map[string]any{
							"sessionUpdate": "usage_update",
							"used":          100,
							"size":          1000,
						},
					},
				})
				writeMsg(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"update": map[string]any{
							"sessionUpdate": "usage_update",
							"used":          200,
							"size":          1000,
							"_meta": map[string]any{
								"inputTokens":       120,
								"cachedReadTokens":  80,
								"cachedWriteTokens": 10,
								"outputTokens":      40,
							},
							"cost": map[string]any{
								"amount":   0.02,
								"currency": "USD",
							},
						},
					},
				})
			}
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content": map[string]any{
							"text": "Hello from fake ACP!",
						},
					},
				},
			})

			// 2. 发送 reasoning/thought chunk 通知
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"update": map[string]any{
						"sessionUpdate": "agent_thought_chunk",
						"content": map[string]any{
							"text": "Thinking about plan...",
						},
					},
				},
			})

			// 3. 发送 tool_call 通知
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"update": map[string]any{
						"sessionUpdate": "tool_call",
						"toolCallId":    "call-999",
						"name":          "bash",
						"rawInput": map[string]any{
							"command": "echo test",
						},
					},
				},
			})

			// 4. ACP agent 主动向 client 发送权限审批请求
			reqID := 10001
			options := []any{
				map[string]any{"optionId": "opt_allow", "name": "Allow this action", "kind": "allow_once"},
				map[string]any{"optionId": "opt_reject", "name": "Reject this action", "kind": "reject_once"},
			}
			if mode == "reject_once_priority" {
				options = []any{
					map[string]any{"optionId": "opt_allow", "kind": "allow_once"},
					map[string]any{"optionId": "opt_always", "name": "Reject always", "kind": "reject_always"},
					map[string]any{"optionId": "opt_once", "kind": "reject_once"},
				}
			} else if mode == "reject_always_only" {
				options = []any{map[string]any{"optionId": "opt_always", "kind": "reject_always"}}
			}
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      reqID,
				"method":  "session/request_permission",
				"params": map[string]any{
					"toolCall": map[string]any{
						"toolCallId": "call-999",
						"name":       "bash",
					},
					"options": options,
				},
			})

			// 等待 client 回复审批结果
			reply, err := readMsg()
			if err != nil {
				os.Exit(1)
			}
			// 检查 reply 是否对应 reqID
			if fmt.Sprint(reply["id"]) != fmt.Sprint(reqID) {
				_, _ = fmt.Fprintf(os.Stderr, "unexpected approval response id: %v\n", reply["id"])
				os.Exit(2)
			}
			if mode == "reject_once_priority" || mode == "reject_always_only" {
				result, _ := reply["result"].(map[string]any)
				outcome, _ := result["outcome"].(map[string]any)
				want := "opt_once"
				if mode == "reject_always_only" {
					want = "opt_always"
				}
				if outcome["optionId"] != want {
					writeMsg(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": fmt.Sprintf("selected %v, want %s", outcome["optionId"], want)}})
					return
				}
			}

			// 5. 发送 tool_call_update 通知
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"update": map[string]any{
						"sessionUpdate": "tool_call_update",
						"toolCallId":    "call-999",
						"status":        "completed",
						"output":        "test output\n",
					},
				},
			})

			// 6. 回复 prompt
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"stopReason": "end_turn",
				},
			})
			if mode == "exit_after_prompt" {
				// 回复后立即退出：模拟一次性 Agent（响应与连接关闭几乎同时发生）
				os.Exit(0)
			}
			return
		}
	}
}

type mockApprover struct {
	called   bool
	decision harness.Decision
	mu       sync.Mutex
}

func (m *mockApprover) Approve(ctx context.Context, req harness.ApprovalRequest) (harness.Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.called = true
	return m.decision, nil
}

func TestACPHarnessRun(t *testing.T) {
	fakeBin := os.Args[0]

	h := New(Options{
		Agent:  AgentCline,
		Binary: fakeBin,
		Env:    []string{fakeACPEnv + "=normal"},
	})

	if h.Kind() != harness.Kind("cline") {
		t.Fatalf("expected Kind cline, got %s", h.Kind())
	}

	approver := &mockApprover{decision: harness.DecisionAllow}

	var events []harness.Event
	var evMu sync.Mutex
	emit := func(e harness.Event) {
		evMu.Lock()
		defer evMu.Unlock()
		events = append(events, e)
	}

	tmpDir := t.TempDir()

	turn := harness.Turn{
		ID:       "turn-1",
		Prompt:   "hello acp",
		WorkDir:  tmpDir,
		Mode:     harness.ModeSupervised,
		Approver: approver,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := h.Run(ctx, turn, emit)
	if err != nil {
		t.Fatalf("Harness.Run failed: %v", err)
	}

	approver.mu.Lock()
	if !approver.called {
		t.Error("expected approver to be called during session/request_permission")
	}
	approver.mu.Unlock()

	evMu.Lock()
	defer evMu.Unlock()

	var hasSession, hasText, hasReasoning, hasToolStart, hasToolEnd, hasDone bool
	for _, e := range events {
		switch e.Type {
		case harness.EventSession:
			if e.SessionID == "fake-session-001" {
				hasSession = true
			}
		case harness.EventText:
			if strings.Contains(e.Text, "Hello from fake ACP!") {
				hasText = true
			}
		case harness.EventReasoning:
			if strings.Contains(e.Text, "Thinking about plan...") {
				hasReasoning = true
			}
		case harness.EventToolStart:
			if e.ToolID == "call-999" && e.ToolName == "bash" {
				hasToolStart = true
			}
		case harness.EventToolEnd:
			if e.ToolID == "call-999" && !e.IsError && strings.Contains(e.Output, "test output") {
				hasToolEnd = true
			}
		case harness.EventDone:
			if e.StopReason == "end_turn" {
				hasDone = true
			}
		}
	}

	if !hasSession {
		t.Error("missing EventSession")
	}
	if !hasText {
		t.Error("missing EventText")
	}
	if !hasReasoning {
		t.Error("missing EventReasoning")
	}
	if !hasToolStart {
		t.Error("missing EventToolStart")
	}
	if !hasToolEnd {
		t.Error("missing EventToolEnd")
	}
	if !hasDone {
		t.Error("missing EventDone")
	}
}

// TestACPHarnessUsageUpdateMapsContextAndMeta 验证 usage_update 的 used/size 映射到上下文口径，
// 输入/输出/缓存 token 只从 _meta 约定键读取，并且同轮多帧时后帧沿用前帧字段。
func TestACPHarnessUsageUpdateMapsContextAndMeta(t *testing.T) {
	events, err := runFakeTurn(t, "usage", harness.Turn{
		WorkDir: t.TempDir(),
		Mode:    harness.ModeSupervised,
	})
	if err != nil {
		t.Fatalf("Harness.Run failed: %v", err)
	}

	var last *harness.Usage
	for _, e := range events {
		if e.Type == harness.EventUsage && e.Usage != nil {
			last = e.Usage
		}
	}
	if last == nil {
		t.Fatal("missing EventUsage")
	}
	if last.ContextTokens != 200 {
		t.Errorf("ContextTokens = %d, want 200", last.ContextTokens)
	}
	if last.ContextWindow != 1000 {
		t.Errorf("ContextWindow = %d, want 1000", last.ContextWindow)
	}
	if last.InputTokens != 120 {
		t.Errorf("InputTokens = %d, want 120", last.InputTokens)
	}
	if last.CachedTokens != 90 {
		t.Errorf("CachedTokens = %d, want 90", last.CachedTokens)
	}
	if last.OutputTokens != 40 {
		t.Errorf("OutputTokens = %d, want 40", last.OutputTokens)
	}
	if last.CostUSD != 0.02 {
		t.Errorf("CostUSD = %v, want 0.02", last.CostUSD)
	}
	if last.InputTokens == last.ContextTokens {
		t.Errorf("InputTokens must not mirror used: both are %d", last.InputTokens)
	}
}

func TestACPHarnessIgnoresLoadReplayUpdates(t *testing.T) {
	const sessionID = "fake-session-001"
	events, err := runFakeTurn(t, "load_replay", harness.Turn{
		WorkDir:   t.TempDir(),
		Mode:      harness.ModeSupervised,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Harness.Run failed: %v", err)
	}

	hasPromptText := false
	hasUsage := false
	hasSession := false
	for _, e := range events {
		switch e.Type {
		case harness.EventText:
			if strings.Contains(e.Text, "REPLAYED-HISTORY") {
				t.Error("load replay text reached harness")
			}
			if strings.Contains(e.Text, "Hello from fake ACP!") {
				hasPromptText = true
			}
		case harness.EventReasoning:
			if strings.Contains(e.Text, "REPLAYED-THOUGHT") {
				t.Error("load replay thought reached harness")
			}
		case harness.EventToolStart, harness.EventToolEnd:
			if e.ToolID == "replay-call" {
				t.Error("load replay tool event reached harness")
			}
		case harness.EventFileChange:
			if strings.Contains(e.Path, "replayed.txt") {
				t.Error("load replay file change reached harness")
			}
		case harness.EventUsage:
			if e.Usage != nil && e.Usage.ContextWindow == 1000 {
				hasUsage = true
			}
		case harness.EventSession:
			if e.SessionID == sessionID {
				hasSession = true
			}
		}
	}
	if !hasPromptText {
		t.Error("missing prompt EventText after load replay")
	}
	if !hasUsage {
		t.Error("missing replay-period EventUsage with ContextWindow=1000")
	}
	if !hasSession {
		t.Errorf("missing EventSession for %q", sessionID)
	}
}

func TestUsageFromUpdate(t *testing.T) {
	if got := usageFromUpdate(acpUsageUpdate{}, harness.Usage{}); !reflect.DeepEqual(got, harness.Usage{}) {
		t.Errorf("empty update = %#v, want zero usage", got)
	}

	nonUSD := usageFromUpdate(acpUsageUpdate{
		Cost: &acpUsageCost{Amount: 1.5, Currency: "EUR"},
	}, harness.Usage{})
	if nonUSD.CostUSD != 0 {
		t.Errorf("CostUSD = %v for non-USD cost, want 0", nonUSD.CostUSD)
	}

	windowOnly := usageFromUpdate(acpUsageUpdate{Size: 1000}, harness.Usage{})
	if windowOnly.ContextTokens != 0 || windowOnly.ContextWindow != 1000 {
		t.Errorf("used=0/size=1000 => ContextTokens=%d ContextWindow=%d, want 0/1000",
			windowOnly.ContextTokens, windowOnly.ContextWindow)
	}

	prev := harness.Usage{OutputTokens: 40, InputTokens: 120, ContextWindow: 1000}
	kept := usageFromUpdate(acpUsageUpdate{Used: 300}, prev)
	if kept.OutputTokens != 40 || kept.InputTokens != 120 || kept.ContextWindow != 1000 {
		t.Errorf("fields absent from frame must survive: %#v", kept)
	}
	if kept.ContextTokens != 300 {
		t.Errorf("ContextTokens = %d, want 300", kept.ContextTokens)
	}

	// cachedTokens 存在时优先于读写合计；字符串写法与 k/M 后缀共用同一套解析。
	stringForm := usageFromUpdate(acpUsageUpdate{Meta: map[string]any{
		"cachedTokens":     "1.5k",
		"cachedReadTokens": 10,
		"outputTokens":     "40 tokens",
	}}, harness.Usage{})
	if stringForm.CachedTokens != 1500 {
		t.Errorf("CachedTokens = %d, want 1500", stringForm.CachedTokens)
	}
	if stringForm.OutputTokens != 40 {
		t.Errorf("OutputTokens = %d, want 40", stringForm.OutputTokens)
	}
}

func TestACPHarnessCancellation(t *testing.T) {
	fakeBin := os.Args[0]

	h := New(Options{
		Agent:  AgentCline,
		Binary: fakeBin,
		Env:    []string{fakeACPEnv + "=hang_until_cancel"},
	})

	ctx, cancel := context.WithCancel(context.Background())

	turn := harness.Turn{
		ID:      "turn-cancel",
		Prompt:  "hang prompt",
		WorkDir: t.TempDir(),
		Mode:    harness.ModeSupervised,
	}

	go func() {
		// 延迟取消 context
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := h.Run(ctx, turn, func(e harness.Event) {})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error on context cancellation, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Harness.Run took %v to terminate, expected within 5s", elapsed)
	}
}

func TestACPHarnessAvailable(t *testing.T) {
	// 登录环境必须先加载完成：未完成时 Available 对“找不到的程序”只能给出临时的乐观结论。
	_ = loginenv.PATH()

	// 针对已知在 PATH 中的系统命令（如 sh）
	h := New(Options{
		Agent:  AgentCline,
		Binary: "sh",
	})
	avail, reason := h.Available()
	if !avail {
		t.Fatalf("expected sh to be available, got false with reason: %s", reason)
	}

	// 针对不存在的命令
	hMissing := New(Options{
		Agent:  AgentCursor,
		Binary: "non_existent_binary_xyz_999",
	})
	avail2, reason2 := hMissing.Available()
	if avail2 {
		t.Fatal("expected non_existent binary to be unavailable")
	}
	if !strings.Contains(reason2, "was not found in PATH") {
		t.Fatalf("unexpected unavailable reason: %s", reason2)
	}
}

// runFakeTurn 以假 Agent 执行一轮对话并返回收集到的事件。
func TestACPRejectOptionPriority(t *testing.T) {
	for _, mode := range []string{"reject_once_priority", "reject_always_only"} {
		t.Run(mode, func(t *testing.T) {
			_, err := runFakeTurn(t, mode, harness.Turn{WorkDir: t.TempDir(), Approver: &mockApprover{decision: harness.DecisionDeny}})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func runFakeTurn(t *testing.T, mode string, turn harness.Turn) ([]harness.Event, error) {
	t.Helper()
	h := New(Options{
		Agent:  AgentCline,
		Binary: os.Args[0],
		Env:    []string{fakeACPEnv + "=" + mode},
	})

	var (
		events []harness.Event
		evMu   sync.Mutex
	)
	emit := func(e harness.Event) {
		evMu.Lock()
		events = append(events, e)
		evMu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := h.Run(ctx, turn, emit)

	evMu.Lock()
	defer evMu.Unlock()
	cp := make([]harness.Event, len(events))
	copy(cp, events)
	return cp, err
}

func sessionIDOf(t *testing.T, events []harness.Event) string {
	t.Helper()
	for _, e := range events {
		if e.Type == harness.EventSession && e.SessionID != "" {
			return e.SessionID
		}
	}
	t.Fatal("no EventSession with a session ID in events")
	return ""
}

// TestACPHarnessResumeSupported 验证 loadSession=true 时第二轮以 session/load 恢复同一会话。
func TestACPHarnessResumeSupported(t *testing.T) {
	turn := harness.Turn{
		ID:      "turn-1",
		Prompt:  "hello acp",
		WorkDir: t.TempDir(),
		Mode:    harness.ModeSupervised,
	}
	events, err := runFakeTurn(t, "normal", turn)
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}
	sid := sessionIDOf(t, events)

	turn2 := turn
	turn2.ID = "turn-2"
	turn2.SessionID = sid
	events2, err := runFakeTurn(t, "normal", turn2)
	if err != nil {
		t.Fatalf("resume turn failed: %v", err)
	}
	if got := sessionIDOf(t, events2); got != sid {
		t.Fatalf("expected resumed session ID %q, got %q", sid, got)
	}
}

// TestACPHarnessResumeUnsupported 验证 Agent 不支持 session/load 时明确报错，而非静默新建会话。
func TestACPHarnessResumeUnsupported(t *testing.T) {
	turn := harness.Turn{
		ID:      "turn-1",
		Prompt:  "hello acp",
		WorkDir: t.TempDir(),
		Mode:    harness.ModeSupervised,
	}
	if _, err := runFakeTurn(t, "no_load", turn); err != nil {
		t.Fatalf("first turn failed: %v", err)
	}

	turn2 := turn
	turn2.ID = "turn-2"
	turn2.SessionID = "fake-session-001"
	_, err := runFakeTurn(t, "no_load", turn2)
	if err == nil {
		t.Fatal("expected resume on loadSession=false agent to fail")
	}
	if !strings.Contains(err.Error(), "cannot resume") {
		t.Fatalf("expected 'cannot resume' error, got: %v", err)
	}
}

// shortenSessionLoadRetry 把 session/load 的重试间隔压到最小，避免用例变慢。
func shortenSessionLoadRetry(t *testing.T) {
	t.Helper()
	prev := sessionLoadRetryDelay
	sessionLoadRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { sessionLoadRetryDelay = prev })
}

// TestACPHarnessFallsBackToNewSessionWhenLoadFails 验证 session/load 持续失败时回退新建会话，
// 而不是让整轮失败。
func TestACPHarnessFallsBackToNewSessionWhenLoadFails(t *testing.T) {
	shortenSessionLoadRetry(t)
	turn := harness.Turn{
		ID:        "turn-1",
		Prompt:    "hello acp",
		WorkDir:   t.TempDir(),
		Mode:      harness.ModeSupervised,
		SessionID: "fake-session-001",
	}
	events, err := runFakeTurn(t, "load_fail", turn)
	if err != nil {
		t.Fatalf("run failed although a fallback session could be created: %v", err)
	}
	if got := sessionIDOf(t, events); got != "fake-session-new" {
		t.Fatalf("expected fallback session %q, got %q", "fake-session-new", got)
	}
}

// TestACPHarnessRetriesLoadBeforeFallback 验证 session/load 首次失败后重试成功，不新建会话。
func TestACPHarnessRetriesLoadBeforeFallback(t *testing.T) {
	shortenSessionLoadRetry(t)
	turn := harness.Turn{
		ID:        "turn-1",
		Prompt:    "hello acp",
		WorkDir:   t.TempDir(),
		Mode:      harness.ModeSupervised,
		SessionID: "fake-session-001",
	}
	events, err := runFakeTurn(t, "load_fail_once", turn)
	if err != nil {
		t.Fatalf("run failed although the retried session/load succeeded: %v", err)
	}
	if got := sessionIDOf(t, events); got != "fake-session-001" {
		t.Fatalf("expected resumed session %q, got %q", "fake-session-001", got)
	}
}

// TestACPHarnessResponseBeforeExit 验证 Agent 回复后立即退出时，prompt 响应不会因连接关闭而丢失。
func TestACPHarnessResponseBeforeExit(t *testing.T) {
	turn := harness.Turn{
		ID:      "turn-exit",
		Prompt:  "hello acp",
		WorkDir: t.TempDir(),
		Mode:    harness.ModeSupervised,
	}
	events, err := runFakeTurn(t, "exit_after_prompt", turn)
	if err != nil {
		t.Fatalf("run failed although the agent answered before exiting: %v", err)
	}
	var sawDone bool
	for _, e := range events {
		if e.Type == harness.EventDone && e.StopReason == "end_turn" {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("missing EventDone after agent exited right after its reply")
	}
}

func TestWaitLaunchSlotSpacingAndCancel(t *testing.T) {
	old := launchSpacing
	launchSpacing = 40 * time.Millisecond
	defer func() { launchSpacing = old }()

	launchGate.mu.Lock()
	launchGate.next = time.Time{}
	launchGate.mu.Unlock()

	start := time.Now()
	if err := waitLaunchSlot(context.Background()); err != nil {
		t.Fatalf("first slot failed: %v", err)
	}
	if err := waitLaunchSlot(context.Background()); err != nil {
		t.Fatalf("second slot failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed < launchSpacing {
		t.Fatalf("expected two launches spaced by at least %v, got %v", launchSpacing, elapsed)
	}

	// ctx 取消时等待中的槽位立即返回
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitLaunchSlot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestResolveWorkDirPath(t *testing.T) {
	workDir := t.TempDir()
	absoluteOutside := "/etc/passwd"
	if runtime.GOOS == "windows" {
		absoluteOutside = filepath.VolumeName(workDir) + `\Windows\win.ini`
	}

	tests := []struct {
		name     string
		path     string
		wantRel  string
		wantFail bool
	}{
		{name: "relative file", path: "sub/file.txt", wantRel: filepath.Join("sub", "file.txt")},
		{name: "dot dot prefix name allowed", path: "..foo.txt", wantRel: "..foo.txt"},
		{name: "cleaned inside", path: "sub/../other.txt", wantRel: "other.txt"},
		{name: "escape parent", path: "../secret.txt", wantFail: true},
		{name: "escape deep", path: "sub/../../secret.txt", wantFail: true},
		{name: "absolute outside", path: absoluteOutside, wantFail: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			abs, rel, err := resolveWorkDirPath(workDir, tc.path)
			if tc.wantFail {
				if err == nil {
					t.Fatalf("expected rejection for %q, got abs=%q rel=%q", tc.path, abs, rel)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.path, err)
			}
			if rel != tc.wantRel {
				t.Fatalf("expected rel %q, got %q", tc.wantRel, rel)
			}
		})
	}
}

func TestResolveWorkDirPathRejectsSymlinkEscape(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workDir, "link")); err != nil {
		t.Fatalf("create dir symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(workDir, "file-link.txt")); err != nil {
		t.Fatalf("create file symlink: %v", err)
	}

	for _, path := range []string{"link/secret.txt", "file-link.txt", "link/new.txt"} {
		if _, _, err := resolveWorkDirPath(workDir, path); err == nil {
			t.Fatalf("expected rejection for symlinked escape %q", path)
		}
	}

	// 指向工作目录内部的符号链接仍可用，且返回解析后的真实路径。
	if err := os.WriteFile(filepath.Join(workDir, "inside.txt"), []byte("ok"), 0644); err != nil {
		t.Fatalf("seed inside file: %v", err)
	}
	if err := os.Symlink(filepath.Join(workDir, "inside.txt"), filepath.Join(workDir, "inside-link.txt")); err != nil {
		t.Fatalf("create inside symlink: %v", err)
	}
	target, rel, err := resolveWorkDirPath(workDir, "inside-link.txt")
	if err != nil {
		t.Fatalf("unexpected rejection for inside symlink: %v", err)
	}
	if rel != "inside.txt" {
		t.Fatalf("expected rel %q, got %q", "inside.txt", rel)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "ok" {
		t.Fatalf("read resolved target %q: content=%q err=%v", target, content, err)
	}
}
