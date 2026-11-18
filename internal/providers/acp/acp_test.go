package acp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/hex"
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
	"unicode"

	"mcode/internal/harness"
	"mcode/internal/providers/loginenv"
	_ "modernc.org/sqlite"
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
	if strings.HasPrefix(mode, "feature_") {
		runFakeACPFeatures(mode)
		return
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
			writeMsg(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"sessionId": "fake-session-001",
				},
			})

		case "session/load":
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

func runFakeACPFeatures(mode string) {
	reader := bufio.NewReader(os.Stdin)
	trace, _ := os.OpenFile(os.Getenv("MCODE_TEST_ACP_TRACE"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	defer trace.Close()
	read := func() map[string]any {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil
		}
		_, _ = trace.Write(line)
		var msg map[string]any
		_ = json.Unmarshal(line, &msg)
		return msg
	}
	write := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	_ = json.NewEncoder(trace).Encode(map[string]any{"args": os.Args[1:]})
	options := []map[string]any{
		{"id": "model", "category": "model", "currentValue": "old"},
		{"id": "thought_level", "category": "model_config", "currentValue": "low", "options": []any{map[string]any{"value": "low"}, map[string]any{"value": "high"}}},
	}
	for {
		msg := read()
		if msg == nil {
			return
		}
		params, _ := msg["params"].(map[string]any)
		result := map[string]any{}
		switch msg["method"] {
		case "initialize":
			caps := map[string]any{"loadSession": mode == "feature_load"}
			sessionCaps := map[string]any{"close": map[string]any{}}
			if mode != "feature_load" {
				sessionCaps["resume"] = map[string]any{}
			}
			caps["sessionCapabilities"] = sessionCaps
			result["agentCapabilities"] = caps
		case "session/new", "session/load", "session/resume":
			result["sessionId"] = "feature-session"
			result["configOptions"] = options
			current := "default"
			if mode == "feature_agy_current_auto" {
				current = "auto"
			}
			available := []any{map[string]any{"id": "default"}, map[string]any{"id": "plan"}, map[string]any{"id": "accept-edits"}, map[string]any{"id": "auto"}}
			if mode == "feature_agy_unadvertised" {
				available = nil
			}
			result["modes"] = map[string]any{"currentModeId": current, "availableModes": available}
		case "session/set_config_option":
			for _, option := range options {
				if option["id"] == params["configId"] && !(mode == "feature_agy_model_mismatch" && option["id"] == "model") && !(mode == "feature_agy_effort_mismatch" && option["id"] == "thought_level") {
					option["currentValue"] = params["value"]
				}
			}
			result["configOptions"] = options
		case "session/prompt":
			if strings.HasPrefix(mode, "feature_agy_outcome_") {
				dbPath := agySessionDB(agyGeminiDir(os.Args[1:], os.Environ()), "feature-session")
				if mode == "feature_agy_outcome_skip" {
					_ = os.Remove(dbPath)
				}
				db, err := sql.Open("sqlite", dbPath)
				if err != nil {
					panic(err)
				}
				if _, err := db.Exec("CREATE TABLE IF NOT EXISTS steps (idx INTEGER, step_type INTEGER, step_payload BLOB)"); err != nil {
					panic(err)
				}
				if mode != "feature_agy_outcome_old" {
					kind := 1
					if mode == "feature_agy_outcome_error" || mode == "feature_agy_outcome_skip" {
						kind = 17
					}
					if _, err := db.Exec("INSERT INTO steps VALUES (2, ?, ?)", kind, []byte("Error 429, Message: quota, Status: RESOURCE_EXHAUSTED quotaResetTimeStamp:2026-11-17T00:00:00Z\nstack")); err != nil {
						panic(err)
					}
				}
				if err := db.Close(); err != nil {
					panic(err)
				}
			}
			if mode == "feature_agy_interaction" {
				write(map[string]any{"jsonrpc": "2.0", "id": 9100, "method": "session/request_permission", "params": map[string]any{
					"toolCall": map[string]any{"toolCallId": "interaction_test", "title": "Choose"},
					"options":  []any{map[string]any{"optionId": "one", "name": "One"}, map[string]any{"optionId": "two", "name": "Two"}},
				}})
				read()
			}
			if strings.HasPrefix(mode, "feature_agy_client_read") {
				workDir, _ := os.Getwd()
				path := filepath.Join(workDir, "agy-read.txt")
				_ = os.WriteFile(path, []byte("read"), 0600)
				notify := func(update map[string]any) {
					write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": update}})
				}
				if mode == "feature_agy_client_read_early" {
					write(map[string]any{"jsonrpc": "2.0", "id": 9102, "method": "fs/read_text_file", "params": map[string]any{"path": path}})
					read()
				}
				call := map[string]any{"sessionUpdate": "tool_call", "toolCallId": "client-read", "title": "Running client_view_file", "locations": []any{map[string]any{"path": path}}}
				if mode == "feature_agy_client_read_early" {
					call["title"] = "Run client_view_file?"
					delete(call, "locations")
					call["rawInput"] = map[string]any{"absolute_path": path}
				}
				notify(call)
				if mode == "feature_agy_client_read_multiple" {
					call["toolCallId"] = "client-read-second"
					notify(call)
				}
				if mode != "feature_agy_client_read_early" {
					if mode == "feature_agy_client_read_missing" {
						_ = os.Remove(path)
					}
					write(map[string]any{"jsonrpc": "2.0", "id": 9102, "method": "fs/read_text_file", "params": map[string]any{"path": path}})
					read()
				}
				notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "client-read", "status": "failed"})
				if mode == "feature_agy_client_read_multiple" {
					notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "client-read-second", "status": "failed"})
				}
			}
			if mode == "feature_agy_command" {
				write(map[string]any{"jsonrpc": "2.0", "id": 9101, "method": "session/request_permission", "params": map[string]any{
					"toolCall": map[string]any{"toolCallId": "command_test", "name": "run", "rawInput": map[string]any{"CommandLine": "cat README.md"}},
					"options":  []any{map[string]any{"optionId": "allow", "kind": "allow_once"}, map[string]any{"optionId": "deny", "kind": "reject_once"}},
				}})
				read()
			}
			if mode == "feature_command_kind" {
				calls := []map[string]any{
					{"toolCallId": "kind-execute", "name": "execute", "kind": "execute", "rawInput": map[string]any{"command": "ls"}},
					{"toolCallId": "kind-agy-command", "name": "run_command", "rawInput": map[string]any{"CommandLine": "git log -n 10 --oneline"}},
					{"toolCallId": "kind-edit", "name": "edit_file", "kind": "edit", "rawInput": map[string]any{"path": "a.txt"}},
				}
				for i, call := range calls {
					write(map[string]any{"jsonrpc": "2.0", "id": 9110 + i, "method": "session/request_permission", "params": map[string]any{
						"toolCall": call,
						"options":  []any{map[string]any{"optionId": "allow", "kind": "allow_once"}, map[string]any{"optionId": "deny", "kind": "reject_once"}},
					}})
					if read() == nil {
						return
					}
				}
			}
			if strings.HasPrefix(mode, "feature_tool_") {
				notify := func(update map[string]any) {
					write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": update}})
				}
				for _, id := range []string{"z", "a"} {
					notify(map[string]any{"sessionUpdate": "tool_call", "toolCallId": id, "name": "test"})
				}
				if mode == "feature_tool_array" {
					notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "a", "status": "pending"})
					notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "a", "status": "in_progress"})
					notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "a", "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "第一段"}}, map[string]any{"type": "text", "text": "第二段"}}, "rawOutput": map[string]any{"output": "备用"}})
					notify(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "z", "content": "无法解析", "rawOutput": map[string]any{"output": "备用"}})
				}
				if mode == "feature_tool_error" {
					write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "error": map[string]any{"code": -32603, "message": "失败"}})
					continue
				}
			}
			if mode == "feature_cancel" {
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": "cancel"}}}})
				for read() != nil {
				}
				return
			}
			if mode == "feature_elicitation" {
				write(map[string]any{"jsonrpc": "2.0", "id": 9001, "method": "elicitation/create", "params": map[string]any{
					"mode": "form", "message": "Choose", "requestedSchema": map[string]any{"properties": map[string]any{
						"single": map[string]any{"type": "string", "title": "Single", "description": "Select one", "enum": []string{"a", "b"}},
						"multi":  map[string]any{"type": "array", "title": "Multi", "items": map[string]any{"enum": []string{"x", "y"}}},
						"text":   map[string]any{"type": "string"},
						"bool":   map[string]any{"type": "boolean"},
						"oneof":  map[string]any{"type": "string", "oneOf": []any{map[string]any{"const": "v", "title": "Visible"}}},
						"anyof":  map[string]any{"type": "array", "items": map[string]any{"anyOf": []any{map[string]any{"const": "z", "title": "Zed"}}}},
					}},
				}})
				if read() == nil {
					return
				}
			}
			if strings.HasPrefix(mode, "feature_permission") {
				opts := []any{map[string]any{"optionId": "deny", "kind": "reject_once"}, map[string]any{"optionId": "session", "kind": "allow_session"}}
				if mode == "feature_permission_once" {
					opts = append(opts, map[string]any{"optionId": "once", "kind": "allow_once"})
				}
				write(map[string]any{"jsonrpc": "2.0", "id": 9010, "method": "session/request_permission", "params": map[string]any{"toolCall": map[string]any{"toolCallId": "permission-test", "name": "read_file"}, "options": opts}})
				if read() == nil {
					return
				}
			}
			if mode == "feature_usage" {
				for _, used := range []int{100, 200} {
					write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": map[string]any{"sessionUpdate": "usage_update", "used": used, "size": 2000}}})
				}
				result["usage"] = map[string]any{"inputTokens": 500, "outputTokens": 20, "thoughtTokens": 5, "cachedReadTokens": 100}
			}
			result["stopReason"] = "end_turn"
			if mode == "feature_agy_outcome_refusal" {
				result["stopReason"] = "refusal"
			}
		case "session/close":
			write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
			return
		}
		write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
	}
}

func runACPFeature(t *testing.T, mode string, agent Agent, turn harness.Turn, configure ...func(*Options)) ([]map[string]any, []harness.Event, error) {
	t.Helper()
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	h := New(Options{Agent: agent, Binary: os.Args[0], ExtraArgs: []string{"--extra"}, Env: []string{fakeACPEnv + "=" + mode, "MCODE_TEST_ACP_TRACE=" + trace}, TurnArgs: func(harness.Turn) []string { return []string{"--gemini_dir", "routed-account"} }})
	for _, apply := range configure {
		apply(&h.opts)
	}
	if mode == "feature_mcp_http" {
		h.opts.BuiltinMCP = harness.BuiltinMCP{URL: "http://127.0.0.1:9000/mcp"}
	}
	if turn.WorkDir == "" {
		turn.WorkDir = t.TempDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var events []harness.Event
	var mu sync.Mutex
	err := h.Run(ctx, turn, func(e harness.Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) })
	data, readErr := os.ReadFile(trace)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var messages []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, msg)
	}
	if roots := h.ProcessRootPIDs(turn.ThreadID); len(roots) != 0 {
		t.Fatalf("leaked process roots: %v", roots)
	}
	mu.Lock()
	defer mu.Unlock()
	return messages, append([]harness.Event(nil), events...), err
}

func featureParams(messages []map[string]any, method string) []map[string]any {
	var params []map[string]any
	for _, msg := range messages {
		if msg["method"] == method {
			p, _ := msg["params"].(map[string]any)
			params = append(params, p)
		}
	}
	return params
}

type elicitationApprover struct {
	mu       sync.Mutex
	req      harness.ApprovalRequest
	response harness.ApprovalResponse
	err      error
	acked    bool
	ackErr   error
}

func (a *elicitationApprover) Approve(context.Context, harness.ApprovalRequest) (harness.Decision, error) {
	return harness.DecisionDeny, nil
}
func (a *elicitationApprover) Interact(_ context.Context, req harness.ApprovalRequest) (harness.ApprovalResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.req = req
	return a.response, a.err
}
func (a *elicitationApprover) AckDelivery(_ harness.ApprovalRequest, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.acked = true
	a.ackErr = err
}

func TestACPElicitationForm(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		response     harness.ApprovalResponse
		noInteractor bool
		err          error
	}{
		{name: "answers", action: "accept", response: harness.ApprovalResponse{Answers: map[string][]string{"single": {"b"}, "multi": {"x", "y"}, "text": {"hello"}, "bool": {"true"}, "oneof": {"Visible"}, "anyof": {"Zed"}}}},
		{name: "skipped", action: "decline", response: harness.ApprovalResponse{Skipped: true}},
		{name: "no interactor", action: "cancel", noInteractor: true},
		{name: "error", action: "cancel", err: errors.New("cancelled")},
		{name: "no answers", action: "cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &elicitationApprover{response: tc.response, err: tc.err}
			var approver harness.Approver = a
			if tc.noInteractor {
				approver = &mockApprover{}
			}
			messages, _, err := runACPFeature(t, "feature_elicitation", AgentAgy, harness.Turn{Approver: approver})
			if err != nil {
				t.Fatal(err)
			}
			var reply map[string]any
			for _, msg := range messages {
				if msg["id"] == float64(9001) {
					reply, _ = msg["result"].(map[string]any)
				}
			}
			if reply["action"] != tc.action {
				t.Fatalf("reply = %v", reply)
			}
			if tc.noInteractor {
				return
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if !a.acked || (tc.err == nil && a.ackErr != nil) {
				t.Fatalf("delivery = %v, %v", a.acked, a.ackErr)
			}
			if a.req.Kind != harness.RequestKindQuestion || a.req.ToolName != "ask_question" || !strings.HasPrefix(a.req.ToolID, "acp-q-") {
				t.Fatalf("request = %+v", a.req)
			}
			if tc.action != "accept" {
				return
			}
			questions := map[string]harness.Question{}
			for _, q := range a.req.Questions {
				questions[q.ID] = q
			}
			if len(questions) != 6 {
				t.Fatalf("questions = %+v", questions)
			}
			if q := questions["single"]; q.Header != "Single" || q.Prompt != "Select one" || !reflect.DeepEqual(q.Choices, []harness.QuestionChoice{{Label: "a"}, {Label: "b"}}) {
				t.Fatalf("single = %+v", q)
			}
			if q := questions["multi"]; !q.AllowsMultiple || q.Prompt != "Multi" || len(q.Choices) != 2 {
				t.Fatalf("multi = %+v", q)
			}
			if q := questions["text"]; !q.AllowsOther || len(q.Choices) != 0 || q.Prompt != "Choose" {
				t.Fatalf("text = %+v", q)
			}
			if !reflect.DeepEqual(questions["bool"].Choices, []harness.QuestionChoice{{Label: "true"}, {Label: "false"}}) {
				t.Fatal(questions["bool"])
			}
			want := map[string]any{"single": "b", "multi": []any{"x", "y"}, "text": "hello", "bool": true, "oneof": "v", "anyof": []any{"z"}}
			if !reflect.DeepEqual(reply["content"], want) {
				t.Fatalf("content = %#v, want %#v", reply["content"], want)
			}
			init := featureParams(messages, "initialize")[0]
			if got := messages[0]["args"]; !reflect.DeepEqual(got, []any{"acp", "--extra", "--gemini_dir", "routed-account"}) {
				t.Fatalf("args order = %v", got)
			}
			caps := init["clientCapabilities"].(map[string]any)
			if _, ok := caps["elicitation"].(map[string]any)["form"]; !ok {
				t.Fatal("form capability missing")
			}
		})
	}
	t.Run("unsupported mode", func(t *testing.T) {
		if _, err := handleElicitation(context.Background(), nil, json.RawMessage(`{"mode":"url"}`)); err == nil {
			t.Fatal("expected error")
		}
	})
}

func fakeResponse(messages []map[string]any, id float64) map[string]any {
	for _, message := range messages {
		if message["id"] == id {
			return message
		}
	}
	return nil
}

func TestAgyInteractionQuestion(t *testing.T) {
	for _, tc := range []struct {
		name, option string
		response     harness.ApprovalResponse
		err          error
		noInteractor bool
	}{
		{name: "selected", option: "two", response: harness.ApprovalResponse{Answers: map[string][]string{"interaction_test": {"Two"}}}},
		{name: "option id", option: "two", response: harness.ApprovalResponse{Answers: map[string][]string{"interaction_test": {"two"}}}},
		{name: "skipped", response: harness.ApprovalResponse{Skipped: true}},
		{name: "no answer"},
		{name: "error", err: errors.New("cancelled")},
		{name: "no interactor", noInteractor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approver := &elicitationApprover{response: tc.response, err: tc.err}
			var turnApprover harness.Approver = approver
			if tc.noInteractor {
				turnApprover = &mockApprover{decision: harness.DecisionAllow}
			}
			messages, _, err := runACPFeature(t, "feature_agy_interaction", AgentAgy, harness.Turn{Mode: harness.ModeAuto, Approver: turnApprover})
			if err != nil {
				t.Fatal(err)
			}
			result, _ := fakeResponse(messages, 9100)["result"].(map[string]any)
			outcome, _ := result["outcome"].(map[string]any)
			if tc.option == "" {
				if outcome["outcome"] != "cancelled" {
					t.Fatalf("outcome = %v", outcome)
				}
			} else if outcome["outcome"] != "selected" || outcome["optionId"] != tc.option {
				t.Fatalf("outcome = %v", outcome)
			}
			if !tc.noInteractor {
				approver.mu.Lock()
				defer approver.mu.Unlock()
				req := approver.req
				if req.Kind != harness.RequestKindQuestion || req.ToolName != "ask_question" || req.ToolID != "interaction_test" || req.Summary != "Choose" || len(req.Questions) != 1 || req.Questions[0].AllowsOther || req.Questions[0].AllowsMultiple || !reflect.DeepEqual(req.Questions[0].Choices, []harness.QuestionChoice{{Label: "One"}, {Label: "Two"}}) {
					t.Fatalf("request = %+v", req)
				}
				if !approver.acked || !errors.Is(approver.ackErr, tc.err) {
					t.Fatalf("ack = %v, %v", approver.acked, approver.ackErr)
				}
			}
		})
	}
}

func TestAgyNativeCommandField(t *testing.T) {
	messages, _, err := runACPFeature(t, "feature_agy_command", AgentAgy, harness.Turn{Mode: harness.ModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := fakeResponse(messages, 9101)["result"].(map[string]any)
	outcome, _ := result["outcome"].(map[string]any)
	if outcome["optionId"] != "allow" {
		t.Fatalf("optionId = %v", outcome["optionId"])
	}
}

// recordingApprover 记录每次审批请求并按 Allow 答复。
type recordingApprover struct {
	mu   sync.Mutex
	reqs []harness.ApprovalRequest
}

func (a *recordingApprover) Approve(_ context.Context, req harness.ApprovalRequest) (harness.Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reqs = append(a.reqs, req)
	return harness.DecisionAllow, nil
}

// TestRequestPermissionMarksExecuteAsCommand 覆盖 session/request_permission 的请求种类：
// 执行类 kind 与 agy 的 run_command 标为命令类，编辑类不标。
func TestRequestPermissionMarksExecuteAsCommand(t *testing.T) {
	approver := &recordingApprover{}
	if _, _, err := runACPFeature(t, "feature_command_kind", AgentAgy, harness.Turn{Mode: harness.ModeSupervised, Approver: approver}); err != nil {
		t.Fatal(err)
	}

	approver.mu.Lock()
	defer approver.mu.Unlock()
	if len(approver.reqs) != 3 {
		t.Fatalf("got %d approval requests, want 3", len(approver.reqs))
	}
	if got := approver.reqs[0].Kind; got != harness.RequestKindCommand {
		t.Errorf("execute 调用应标为命令类，得到 %q", got)
	}
	if got := approver.reqs[1]; got.ToolName != "run_command" || got.Kind != harness.RequestKindCommand {
		t.Errorf("agy run_command 应标为命令类，得到 toolName=%q kind=%q", got.ToolName, got.Kind)
	}
	if got := approver.reqs[2].Kind; got == harness.RequestKindCommand {
		t.Errorf("编辑类调用不应标为命令类，得到 %q", got)
	}
}

func TestAgyClientViewFileRepair(t *testing.T) {
	for _, mode := range []string{"feature_agy_client_read", "feature_agy_client_read_early", "feature_agy_client_read_multiple", "feature_agy_client_read_missing"} {
		t.Run(mode, func(t *testing.T) {
			_, events, err := runACPFeature(t, mode, AgentAgy, harness.Turn{Mode: harness.ModeAuto})
			if err != nil {
				t.Fatal(err)
			}
			var ends []harness.Event
			for _, event := range events {
				if event.Type == harness.EventToolEnd && event.ToolID == "client-read" {
					ends = append(ends, event)
				}
			}
			if len(ends) != 1 || ends[0].IsError != (mode == "feature_agy_client_read_missing") || ends[0].Output != "" {
				t.Fatalf("%s tool ends = %+v", mode, ends)
			}
			if mode == "feature_agy_client_read_multiple" {
				var second []harness.Event
				for _, event := range events {
					if event.Type == harness.EventToolEnd && event.ToolID == "client-read-second" {
						second = append(second, event)
					}
				}
				if len(second) != 1 || !second[0].IsError {
					t.Fatalf("second tool ends = %+v", second)
				}
			}
		})
	}
}

func TestAgyTurnOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, wantError  string
		missing, corrupt bool
	}{
		{name: "error", wantError: "Error 429, Message: quota, Status: RESOURCE_EXHAUSTED quotaResetTimeStamp:2026-11-17T00:00:00Z"},
		{name: "normal"},
		{name: "old"},
		{name: "error", missing: true, wantError: "RESOURCE_EXHAUSTED"},
		{name: "skip", corrupt: true},
		{name: "refusal", wantError: "agy quota exhausted (stopReason refusal)"},
	} {
		t.Run(fmt.Sprintf("%s/missing=%v", tc.name, tc.missing), func(t *testing.T) {
			geminiDir := filepath.Join(t.TempDir(), "gemini # account")
			dbPath := agySessionDB(geminiDir, "feature-session")
			if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
				t.Fatal(err)
			}
			if tc.corrupt {
				if err := os.WriteFile(dbPath, []byte("invalid sqlite"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if !tc.missing {
				db, err := sql.Open("sqlite", dbPath)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.Exec("CREATE TABLE steps (idx INTEGER, step_type INTEGER, step_payload BLOB)"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("INSERT INTO steps VALUES (1, 17, ?)", []byte("Error 429, Message: old quota, Status: RESOURCE_EXHAUSTED")); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			_, events, err := runACPFeature(t, "feature_agy_outcome_"+tc.name, AgentAgy, harness.Turn{}, func(opts *Options) {
				opts.TurnArgs = func(harness.Turn) []string { return []string{"--gemini_dir=" + geminiDir} }
			})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			done := 0
			for _, event := range events {
				if event.Type == harness.EventDone {
					done++
				}
			}
			wantDone := 1
			if tc.wantError != "" {
				wantDone = 0
			}
			if done != wantDone {
				t.Fatalf("EventDone count = %d, want %d", done, wantDone)
			}
		})
	}
}

func TestAgyStepErrorText(t *testing.T) {
	const payloadHex = "081120032ACC010A0B088FC1DED6061090E5E53D1805420B088FC1DED6061090E5E53D622437613765636239362D633166632D343662382D383133622D343461636132396431626135A201500A2430396439646261372D663734362D343731612D386665362D39623332663532316561366510551829222432363537353235392D386563312D346232652D393133362D393836316165653936646130D201110A0F0803120B088FC1DED60610F8E6E73D9A02206464373033656438383265313431383438643339633436653163613266333937C201A5011AA2011272415049206572726F722028617474656D70742032293A20554E415641494C41424C452028636F646520353033293A204E6F20636170616369747920617661696C61626C6520666F72206D6F64656C2067656D696E692D332E382D666C6173682D68696768206F6E2074686520736572766572322C30396439646261372D663734362D343731612D386665362D3962333266353231656136652D38352D32303130"
	tests := []struct {
		name    string
		payload []byte
		want    []string
	}{
		{
			name:    "protobuf payload",
			payload: mustDecodeHex(t, payloadHex),
			want:    []string{"UNAVAILABLE (code 503): No capacity available for model gemini-3.8-flash-high"},
		},
		{
			name:    "quota payload",
			payload: append([]byte("\x08\x11\x20\x03\x2a\x0a"), []byte("Error 429, Message: quota exhausted, Status: RESOURCE_EXHAUSTED quotaResetTimeStamp:2026-11-16T10:00:00Z")...),
			want:    []string{"RESOURCE_EXHAUSTED", "2026-11-16T10:00:00Z"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "steps.db")
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE TABLE steps (idx INTEGER, step_type INTEGER, step_payload BLOB)"); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO steps VALUES (1, 17, ?)", tc.payload); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			text, ok := agyNewStepError(dbPath, -1)
			if !ok {
				t.Fatal("agyNewStepError returned ok=false")
			}
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("text = %q, want substring %q", text, want)
				}
			}
			for _, r := range text {
				if unicode.IsControl(r) {
					t.Fatalf("text contains control character %U: %q", r, text)
				}
			}
		})
	}
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestACPResumeAndClose(t *testing.T) {
	for _, tc := range []struct{ mode, session, method string }{{"feature_resume", "existing", "session/resume"}, {"feature_load", "existing", "session/load"}, {"feature_new", "", "session/new"}} {
		t.Run(tc.method, func(t *testing.T) {
			dir := t.TempDir()
			messages, _, err := runACPFeature(t, tc.mode, AgentAgy, harness.Turn{SessionID: tc.session, AdditionalDirs: []string{dir}})
			if err != nil {
				t.Fatal(err)
			}
			params := featureParams(messages, tc.method)
			if len(params) != 1 || !reflect.DeepEqual(params[0]["additionalDirectories"], []any{dir}) {
				t.Fatalf("setup = %v", params)
			}
			if tc.method == "session/resume" && len(featureParams(messages, "session/load")) != 0 {
				t.Fatal("used load instead of resume")
			}
			if close := featureParams(messages, "session/close"); len(close) != 1 {
				t.Fatal("missing close")
			} else if tc.session != "" && close[0]["sessionId"] != tc.session {
				t.Fatal(close)
			}
		})
	}
}

func TestACPPromptUsage(t *testing.T) {
	_, events, err := runACPFeature(t, "feature_usage", AgentAgy, harness.Turn{})
	if err != nil {
		t.Fatal(err)
	}
	var usages []harness.Usage
	for i, e := range events {
		if e.Type == harness.EventUsage {
			usages = append(usages, *e.Usage)
			if i+1 == len(events) {
				t.Fatal("usage after done")
			}
		}
	}
	if len(usages) != 3 {
		t.Fatalf("usages = %v", usages)
	}
	if want := (harness.Usage{InputTokens: 200, ContextTokens: 200, ContextWindow: 2000}); !reflect.DeepEqual(usages[1], want) {
		t.Fatalf("update = %+v", usages[1])
	}
	if want := (harness.Usage{InputTokens: 500, OutputTokens: 25, CachedTokens: 100, ContextTokens: 200, ContextWindow: 2000}); !reflect.DeepEqual(usages[2], want) {
		t.Fatalf("final = %+v", usages[2])
	}
	if events[len(events)-1].Type != harness.EventDone {
		t.Fatal("done must be last")
	}
}

func TestACPAgySkipsUnchangedModel(t *testing.T) {
	messages, _, err := runACPFeature(t, "feature_agy", AgentAgy, harness.Turn{Model: "old", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if config := featureParams(messages, "session/set_config_option"); len(config) != 0 {
		t.Fatal(config)
	}
}

func TestACPAgyModeAndModel(t *testing.T) {
	for _, tc := range []struct {
		name                string
		mode                harness.PermissionMode
		interaction, wanted string
	}{
		{"plan", harness.ModeSupervised, "plan", "plan"}, {"edits", harness.ModeAutoAcceptEdits, "", "accept-edits"}, {"auto", harness.ModeAuto, "", "auto"}, {"supervised", harness.ModeSupervised, "", ""}, {"readonly", harness.ModeReadOnly, "", ""}, {"full", harness.ModeFullAccess, "plan", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages, _, err := runACPFeature(t, "feature_agy", AgentAgy, harness.Turn{Mode: tc.mode, InteractionMode: tc.interaction, Model: "requested", Effort: "high"})
			if err != nil {
				t.Fatal(err)
			}
			modes := featureParams(messages, "session/set_mode")
			if tc.wanted == "" {
				if len(modes) != 0 {
					t.Fatal(modes)
				}
			} else if len(modes) != 1 || modes[0]["modeId"] != tc.wanted {
				t.Fatal(modes)
			}
			if len(featureParams(messages, "session/set_model")) != 0 {
				t.Fatal("agy used set_model")
			}
			if config := featureParams(messages, "session/set_config_option"); len(config) != 2 || config[0]["configId"] != "model" || config[1]["configId"] != "thought_level" {
				t.Fatal(config)
			}
		})
	}
	for _, mode := range []string{"feature_agy_current_auto", "feature_agy_unadvertised"} {
		t.Run(mode, func(t *testing.T) {
			messages, _, err := runACPFeature(t, mode, AgentAgy, harness.Turn{Mode: harness.ModeAuto})
			if err != nil {
				t.Fatal(err)
			}
			if len(featureParams(messages, "session/set_mode")) != 0 {
				t.Fatal("unnecessary set_mode")
			}
		})
	}
	for _, name := range []string{"model", "effort"} {
		t.Run(name+" mismatch", func(t *testing.T) {
			messages, _, err := runACPFeature(t, "feature_agy_"+name+"_mismatch", AgentAgy, harness.Turn{Model: "requested", Effort: "high"})
			if err == nil || !strings.Contains(err.Error(), "agy did not apply "+name) {
				t.Fatalf("err = %v", err)
			}
			if len(featureParams(messages, "session/prompt")) != 0 {
				t.Fatal("sent prompt after config failure")
			}
		})
	}
}

func TestACPCancelDoesNotCloseSession(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	h := New(Options{Agent: AgentAgy, Binary: os.Args[0], Env: []string{fakeACPEnv + "=feature_cancel", "MCODE_TEST_ACP_TRACE=" + trace}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := h.Run(ctx, harness.Turn{WorkDir: t.TempDir(), ThreadID: "cancel"}, func(event harness.Event) {
		if event.Type == harness.EventText {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session/close") {
		t.Fatal("closed cancelled session")
	}
	if roots := h.ProcessRootPIDs("cancel"); len(roots) != 0 {
		t.Fatalf("leaked roots %v", roots)
	}
}

func TestACPAgyPermissionOptions(t *testing.T) {
	for _, wanted := range []string{"session", "once"} {
		t.Run(wanted, func(t *testing.T) {
			messages, _, err := runACPFeature(t, "feature_permission_"+wanted, AgentAgy, harness.Turn{Approver: &mockApprover{decision: harness.DecisionAllow}})
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range messages {
				if msg["id"] == float64(9010) {
					result := msg["result"].(map[string]any)
					if result["outcome"].(map[string]any)["optionId"] != wanted {
						t.Fatal(result)
					}
					return
				}
			}
			t.Fatal("missing permission reply")
		})
	}
}

func TestACPToolCallUpdateContentArray(t *testing.T) {
	_, events, err := runACPFeature(t, "feature_tool_array", AgentAgy, harness.Turn{})
	if err != nil {
		t.Fatal(err)
	}
	ends := map[string][]harness.Event{}
	for _, event := range events {
		if event.Type == harness.EventToolEnd {
			ends[event.ToolID] = append(ends[event.ToolID], event)
		}
	}
	if len(ends["a"]) != 1 || ends["a"][0].Output != "第一段第二段" || len(ends["z"]) != 1 || ends["z"][0].Output != "备用" {
		t.Fatalf("工具结束事件 = %+v", ends)
	}
}

func TestACPFinishesOpenToolsAtTurnEnd(t *testing.T) {
	for _, mode := range []string{"feature_tool_open", "feature_tool_error"} {
		t.Run(mode, func(t *testing.T) {
			_, events, err := runACPFeature(t, mode, AgentAgy, harness.Turn{})
			failed := mode == "feature_tool_error"
			if (err != nil) != failed {
				t.Fatalf("错误 = %v", err)
			}
			var ends []harness.Event
			done := false
			for _, event := range events {
				if event.Type == harness.EventDone {
					done = true
				}
				if event.Type == harness.EventToolEnd {
					if done {
						t.Fatal("工具结束事件出现在回合结束后")
					}
					ends = append(ends, event)
				}
			}
			if done == failed || len(ends) != 2 || ends[0].ToolID != "a" || ends[1].ToolID != "z" {
				t.Fatalf("事件 = %+v", events)
			}
			for _, end := range ends {
				if end.IsError != failed || (failed && end.Output != "本轮中断，工具未完成") {
					t.Fatalf("工具结束事件 = %+v", end)
				}
			}
		})
	}
}

func TestACPAgyMCPUsesHTTPWithoutCapability(t *testing.T) {
	messages, _, err := runACPFeature(t, "feature_mcp_http", AgentAgy, harness.Turn{})
	if err != nil {
		t.Fatal(err)
	}
	params := featureParams(messages, "session/new")
	if len(params) != 1 {
		t.Fatalf("会话参数 = %v", params)
	}
	servers, ok := params[0]["mcpServers"].([]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("MCP 服务 = %v", params[0]["mcpServers"])
	}
	server, ok := servers[0].(map[string]any)
	if !ok || server["type"] != "http" || server["command"] != nil {
		t.Fatalf("MCP 服务 = %v", servers[0])
	}
}
