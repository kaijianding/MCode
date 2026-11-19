package agy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"mcode/internal/harness"
)

type fakePlanApprover struct {
	mu       sync.Mutex
	requests []harness.ApprovalRequest
	acks     []error
	interact func(context.Context, harness.ApprovalRequest) (harness.ApprovalResponse, error)
	onAck    func(harness.ApprovalRequest, error)
}

func (f *fakePlanApprover) Approve(context.Context, harness.ApprovalRequest) (harness.Decision, error) {
	return harness.DecisionDeny, nil
}

func (f *fakePlanApprover) Interact(ctx context.Context, req harness.ApprovalRequest) (harness.ApprovalResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	fn := f.interact
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return harness.ApprovalResponse{Decision: harness.DecisionDeny}, nil
}

func (f *fakePlanApprover) AckDelivery(req harness.ApprovalRequest, err error) {
	f.mu.Lock()
	f.acks = append(f.acks, err)
	cb := f.onAck
	f.mu.Unlock()
	if cb != nil {
		cb(req, err)
	}
}

func (f *fakePlanApprover) snapshot() ([]harness.ApprovalRequest, []error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]harness.ApprovalRequest(nil), f.requests...), append([]error(nil), f.acks...)
}

type plainApprover struct {
	called int
}

func (p *plainApprover) Approve(context.Context, harness.ApprovalRequest) (harness.Decision, error) {
	p.called++
	return harness.DecisionAllow, nil
}

func runFakeAgyPlanApproval(firstLine []byte, writeMsg func(any)) {
	callIndex := recordFakeInvocation(os.Getenv("MCODE_TEST_AGY_RECORD_FILE"), os.Args, string(firstLine))
	scenario := os.Getenv("MCODE_TEST_AGY_PLAN_SCENARIO")
	planPath := os.Getenv("MCODE_TEST_AGY_PLAN_FILE")
	planContent := os.Getenv("MCODE_TEST_AGY_PLAN_CONTENT")
	ackMarker := os.Getenv("MCODE_TEST_AGY_ACK_MARKER")

	emitSuccess := func(convID, text string) {
		writeMsg(map[string]any{
			"event":           "init",
			"conversation_id": convID,
		})
		if text != "" {
			writeMsg(map[string]any{
				"event": "step_update",
				"step_update": map[string]any{
					"step_index": 0,
					"step_type":  "agent_response",
					"text_delta": text,
				},
			})
		}
		writeMsg(map[string]any{
			"event": "result",
			"result": map[string]any{
				"status":          "SUCCESS",
				"conversation_id": convID,
			},
		})
		os.Exit(0)
	}

	switch scenario {
	case "plan_file":
		if callIndex == 1 {
			if err := os.WriteFile(planPath, []byte(planContent), 0o600); err != nil {
				os.Exit(11)
			}
			writeMsg(map[string]any{
				"event":           "init",
				"conversation_id": "plan-conv-001",
			})
			writeMsg(map[string]any{
				"event": "step_update",
				"step_update": map[string]any{
					"step_index": 0,
					"step_type":  "tool",
					"tool_name":  "write_to_file",
					"tool_id":    "tool-plan-1",
					"state":      "DONE",
					"tool_info": map[string]any{
						"name":       "write_to_file",
						"id":         "tool-plan-1",
						"parameters": map[string]any{"TargetFile": planPath},
						"output":     "ok",
					},
				},
			})
			writeMsg(map[string]any{
				"event": "step_update",
				"step_update": map[string]any{
					"step_index": 1,
					"step_type":  "agent_response",
					"text_delta": "请查看计划文档，确认后我将开始修改。",
				},
			})
			writeMsg(map[string]any{
				"event": "result",
				"result": map[string]any{
					"status":          "SUCCESS",
					"conversation_id": "plan-conv-001",
				},
			})
			os.Exit(0)
		}
		if ackMarker != "" {
			deadline := time.Now().Add(2 * time.Second)
			for {
				if _, err := os.Stat(ackMarker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					os.Exit(12)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		emitSuccess("plan-conv-001", "Implemented.")

	case "text_fallback":
		if callIndex == 1 {
			emitSuccess("plan-conv-text", "# Fallback Plan\n1. Step A")
		}
		emitSuccess("plan-conv-text", "Executed.")

	case "feedback_then_approve":
		switch callIndex {
		case 1:
			emitSuccess("plan-conv-fb", "Draft plan v1")
		case 2:
			if err := os.WriteFile(planPath, []byte("# Plan v2\n1. Added tests"), 0o600); err != nil {
				os.Exit(13)
			}
			writeMsg(map[string]any{
				"event":           "init",
				"conversation_id": "plan-conv-fb",
			})
			writeMsg(map[string]any{
				"event": "step_update",
				"step_update": map[string]any{
					"step_index": 0,
					"step_type":  "tool",
					"tool_name":  "replace_file_content",
					"tool_id":    "tool-plan-2",
					"state":      "DONE",
					"tool_info": map[string]any{
						"name":       "replace_file_content",
						"id":         "tool-plan-2",
						"parameters": map[string]any{"file_path": planPath},
						"output":     "ok",
					},
				},
			})
			writeMsg(map[string]any{
				"event": "step_update",
				"step_update": map[string]any{
					"step_index": 1,
					"step_type":  "agent_response",
					"text_delta": "Updated plan v2",
				},
			})
			writeMsg(map[string]any{
				"event": "result",
				"result": map[string]any{
					"status":          "SUCCESS",
					"conversation_id": "plan-conv-fb",
				},
			})
			os.Exit(0)
		default:
			emitSuccess("plan-conv-fb", "All implemented.")
		}

	case "empty_plan":
		emitSuccess("plan-conv-empty", "")

	case "turn_error":
		writeMsg(map[string]any{
			"event":           "init",
			"conversation_id": "plan-conv-err",
		})
		writeMsg(map[string]any{
			"event": "result",
			"result": map[string]any{
				"status":          "ERROR",
				"conversation_id": "plan-conv-err",
				"error":           "boom",
			},
		})
		os.Exit(1)

	default:
		os.Exit(10)
	}
}

func handleFakeAgyACPPlanPrompt(id any, promptCount int, currentMode string, params map[string]any) {
	planPath := os.Getenv("MCODE_TEST_AGY_PLAN_FILE")
	planContent := os.Getenv("MCODE_TEST_AGY_PLAN_CONTENT")
	recordFile := os.Getenv("MCODE_TEST_AGY_RECORD_FILE")

	var promptText string
	if items, ok := params["prompt"].([]any); ok && len(items) > 0 {
		if first, ok := items[0].(map[string]any); ok {
			promptText, _ = first["text"].(string)
		}
	}
	_ = recordFakeInvocation(recordFile, []string{"mode=" + currentMode}, promptText)

	if promptCount == 1 {
		if err := os.WriteFile(planPath, []byte(planContent), 0o600); err != nil {
			os.Exit(21)
		}
		for _, update := range []map[string]any{
			{
				"sessionUpdate": "tool_call",
				"toolCallId":    "acp-plan-tool",
				"name":          "write_to_file",
				"rawInput":      map[string]any{"TargetFile": planPath},
			},
			{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    "acp-plan-tool",
				"status":        "completed",
				"output":        "ok",
			},
			{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"text": "请查看计划文档"},
			},
		} {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params":  map[string]any{"update": update},
			})
		}
	} else {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"jsonrpc": "2.0",
			"method":  "session/update",
			"params":  map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": "Implemented via ACP."}}},
		})
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  map[string]any{"stopReason": "end_turn"},
	})
}

func readFakeInvocations(t *testing.T, recordFile string) []fakeInvocationRecord {
	t.Helper()
	data, err := os.ReadFile(recordFile)
	if err != nil {
		t.Fatalf("read record file: %v", err)
	}
	var records []fakeInvocationRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("unmarshal record file: %v", err)
	}
	return records
}

func argPairValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func newPrintModePlanHarness(t *testing.T) *Harness {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return New(Options{
		Binary:  exe,
		Channel: func() string { return ChannelPrintMode },
	})
}

func TestAgyPlanApproval_ApproveFromPlanFile(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "implementation_plan.md")
	recordFile := filepath.Join(dir, "records.json")
	ackMarker := filepath.Join(dir, "acked")
	wantPlan := "# Implementation Plan\n\n1. Update handler\n2. Add tests\n"

	var doneMu sync.Mutex
	doneCount := 0

	approver := &fakePlanApprover{
		interact: func(_ context.Context, req harness.ApprovalRequest) (harness.ApprovalResponse, error) {
			doneMu.Lock()
			seenDone := doneCount
			doneMu.Unlock()
			if seenDone != 1 {
				t.Errorf("Interact must run after round 1 EventDone, got doneCount=%d", seenDone)
			}
			return harness.ApprovalResponse{Decision: harness.DecisionAllow}, nil
		},
		onAck: func(_ harness.ApprovalRequest, err error) {
			if err == nil {
				_ = os.WriteFile(ackMarker, []byte("ok"), 0o600)
			}
		},
	}

	h := newPrintModePlanHarness(t)
	turn := harness.Turn{
		WorkDir:         dir,
		Prompt:          "Create a plan",
		Mode:            harness.ModeReadOnly,
		InteractionMode: "plan",
		PlanExitMode:    harness.ModeAuto,
		Approver:        approver,
		Env: []string{
			fakeAgyEnv + "=plan_approval",
			"MCODE_TEST_AGY_PLAN_SCENARIO=plan_file",
			"MCODE_TEST_AGY_PLAN_FILE=" + planPath,
			"MCODE_TEST_AGY_PLAN_CONTENT=" + wantPlan,
			"MCODE_TEST_AGY_RECORD_FILE=" + recordFile,
			"MCODE_TEST_AGY_ACK_MARKER=" + ackMarker,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.Run(ctx, turn, func(ev harness.Event) {
		if ev.Type == harness.EventDone {
			doneMu.Lock()
			doneCount++
			doneMu.Unlock()
		}
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reqs, acks := approver.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 Interact call, got %d", len(reqs))
	}
	if reqs[0].Kind != harness.RequestKindPlan || reqs[0].ToolName != "exit_plan_mode" || reqs[0].ToolID != "exit_plan_mode" {
		t.Fatalf("unexpected request metadata: %+v", reqs[0])
	}
	if reqs[0].Plan != wantPlan {
		t.Fatalf("plan = %q, want %q", reqs[0].Plan, wantPlan)
	}
	if len(acks) != 1 || acks[0] != nil {
		t.Fatalf("expected 1 nil AckDelivery, got %v", acks)
	}

	doneMu.Lock()
	finalDone := doneCount
	doneMu.Unlock()
	if finalDone != 2 {
		t.Fatalf("expected 2 EventDone events, got %d", finalDone)
	}

	records := readFakeInvocations(t, recordFile)
	if len(records) != 2 {
		t.Fatalf("expected 2 invocations, got %d", len(records))
	}
	if got := argPairValue(records[0].Args, "--mode"); got != "plan" {
		t.Fatalf("round 1 --mode = %q, want plan", got)
	}
	if got := argPairValue(records[1].Args, "--mode"); got != "accept-edits" {
		t.Fatalf("round 2 --mode = %q, want accept-edits (PlanExitMode=%s)", got, harness.ModeAuto)
	}
	if got := argPairValue(records[1].Args, "--conversation"); got != "plan-conv-001" {
		t.Fatalf("round 2 --conversation = %q, want plan-conv-001", got)
	}
	if !strings.Contains(records[1].Stdin, "Implement the plan.") {
		t.Fatalf("round 2 stdin = %q, want Implement the plan.", records[1].Stdin)
	}
	if strings.Contains(records[1].Stdin, "You are in Plan Mode") {
		t.Fatalf("round 2 must not contain planModePromptGuard: %q", records[1].Stdin)
	}
}

func TestAgyPlanApproval_DenyWithFeedbackThenApprove(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "implementation_plan.md")
	recordFile := filepath.Join(dir, "records.json")

	approver := &fakePlanApprover{}
	approver.interact = func(_ context.Context, _ harness.ApprovalRequest) (harness.ApprovalResponse, error) {
		reqs, _ := approver.snapshot()
		if len(reqs) == 1 {
			return harness.ApprovalResponse{Decision: harness.DecisionDeny, Feedback: "Revise with test cases"}, nil
		}
		return harness.ApprovalResponse{Decision: harness.DecisionAllow}, nil
	}

	h := newPrintModePlanHarness(t)
	turn := harness.Turn{
		WorkDir:         dir,
		Prompt:          "Plan feature",
		Mode:            harness.ModeReadOnly,
		InteractionMode: "plan",
		PlanExitMode:    harness.ModeSupervised,
		Approver:        approver,
		Env: []string{
			fakeAgyEnv + "=plan_approval",
			"MCODE_TEST_AGY_PLAN_SCENARIO=feedback_then_approve",
			"MCODE_TEST_AGY_PLAN_FILE=" + planPath,
			"MCODE_TEST_AGY_RECORD_FILE=" + recordFile,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.Run(ctx, turn, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reqs, acks := approver.snapshot()
	if len(reqs) != 2 || len(acks) != 2 || acks[0] != nil || acks[1] != nil {
		t.Fatalf("unexpected requests/acks: reqs=%d acks=%v", len(reqs), acks)
	}
	if reqs[0].Plan != "Draft plan v1" {
		t.Fatalf("round 1 fallback plan = %q, want Draft plan v1", reqs[0].Plan)
	}
	if reqs[1].Plan != "# Plan v2\n1. Added tests" {
		t.Fatalf("round 2 file plan = %q", reqs[1].Plan)
	}

	records := readFakeInvocations(t, recordFile)
	if len(records) != 3 {
		t.Fatalf("expected 3 invocations, got %d", len(records))
	}
	if argPairValue(records[1].Args, "--mode") != "plan" || argPairValue(records[1].Args, "--conversation") != "plan-conv-fb" {
		t.Fatalf("round 2 args unexpected: %v", records[1].Args)
	}
	if !strings.Contains(records[1].Stdin, "Revise with test cases") {
		t.Fatalf("round 2 stdin missing feedback: %q", records[1].Stdin)
	}
	if slices.Contains(records[2].Args, "--mode") {
		t.Fatalf("round 3 should run in ModeSupervised (no --mode flag), got %v", records[2].Args)
	}
	if argPairValue(records[2].Args, "--conversation") != "plan-conv-fb" || !strings.Contains(records[2].Stdin, "Implement the plan.") {
		t.Fatalf("round 3 unexpected: args=%v stdin=%q", records[2].Args, records[2].Stdin)
	}
}

func TestAgyPlanApproval_DenyWithoutFeedback(t *testing.T) {
	dir := t.TempDir()
	recordFile := filepath.Join(dir, "records.json")

	approver := &fakePlanApprover{
		interact: func(context.Context, harness.ApprovalRequest) (harness.ApprovalResponse, error) {
			return harness.ApprovalResponse{Decision: harness.DecisionDeny}, nil
		},
	}

	h := newPrintModePlanHarness(t)
	turn := harness.Turn{
		WorkDir:         dir,
		Prompt:          "Plan only",
		Mode:            harness.ModeReadOnly,
		InteractionMode: "plan",
		PlanExitMode:    harness.ModeAuto,
		Approver:        approver,
		Env: []string{
			fakeAgyEnv + "=plan_approval",
			"MCODE_TEST_AGY_PLAN_SCENARIO=text_fallback",
			"MCODE_TEST_AGY_RECORD_FILE=" + recordFile,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.Run(ctx, turn, nil); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reqs, acks := approver.snapshot()
	if len(reqs) != 1 || reqs[0].Kind != harness.RequestKindPlan {
		t.Fatalf("expected 1 plan request, got %+v", reqs)
	}
	if len(acks) != 1 || acks[0] != nil {
		t.Fatalf("expected 1 nil AckDelivery, got %v", acks)
	}
	if records := readFakeInvocations(t, recordFile); len(records) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(records))
	}
}

func TestAgyPlanApproval_InteractError(t *testing.T) {
	dir := t.TempDir()
	recordFile := filepath.Join(dir, "records.json")
	wantErr := errors.New("approval channel closed")

	approver := &fakePlanApprover{
		interact: func(context.Context, harness.ApprovalRequest) (harness.ApprovalResponse, error) {
			return harness.ApprovalResponse{}, wantErr
		},
	}

	h := newPrintModePlanHarness(t)
	turn := harness.Turn{
		WorkDir:         dir,
		Prompt:          "Plan only",
		Mode:            harness.ModeReadOnly,
		InteractionMode: "plan",
		PlanExitMode:    harness.ModeAuto,
		Approver:        approver,
		Env: []string{
			fakeAgyEnv + "=plan_approval",
			"MCODE_TEST_AGY_PLAN_SCENARIO=text_fallback",
			"MCODE_TEST_AGY_RECORD_FILE=" + recordFile,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.Run(ctx, turn, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	_, acks := approver.snapshot()
	if len(acks) != 1 || !errors.Is(acks[0], wantErr) {
		t.Fatalf("expected AckDelivery with %v, got %v", wantErr, acks)
	}
	if records := readFakeInvocations(t, recordFile); len(records) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(records))
	}
}

func TestAgyPlanApproval_SkipsWhenNotEligible(t *testing.T) {
	for _, tc := range []struct {
		name            string
		interactionMode string
		scenario        string
		plainOnly       bool
		wantErr         bool
	}{
		{name: "non-plan", interactionMode: "build", scenario: "text_fallback"},
		{name: "empty-interaction-mode", interactionMode: "", scenario: "text_fallback"},
		{name: "non-interactor", interactionMode: "plan", scenario: "text_fallback", plainOnly: true},
		{name: "empty-plan", interactionMode: "plan", scenario: "empty_plan"},
		{name: "round-error", interactionMode: "plan", scenario: "turn_error", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			recordFile := filepath.Join(dir, "records.json")

			approver := &fakePlanApprover{
				interact: func(context.Context, harness.ApprovalRequest) (harness.ApprovalResponse, error) {
					t.Fatal("Interact must not be called")
					return harness.ApprovalResponse{}, nil
				},
			}

			var usedApprover harness.Approver = approver
			if tc.plainOnly {
				usedApprover = &plainApprover{}
			}

			h := newPrintModePlanHarness(t)
			turn := harness.Turn{
				WorkDir:         dir,
				Prompt:          "Run",
				Mode:            harness.ModeAuto,
				InteractionMode: tc.interactionMode,
				PlanExitMode:    harness.ModeAuto,
				Approver:        usedApprover,
				Env: []string{
					fakeAgyEnv + "=plan_approval",
					"MCODE_TEST_AGY_PLAN_SCENARIO=" + tc.scenario,
					"MCODE_TEST_AGY_RECORD_FILE=" + recordFile,
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := h.Run(ctx, turn, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}

			reqs, acks := approver.snapshot()
			if len(reqs) != 0 || len(acks) != 0 {
				t.Fatalf("unexpected Interact/AckDelivery: reqs=%d acks=%d", len(reqs), len(acks))
			}
			if records := readFakeInvocations(t, recordFile); len(records) != 1 {
				t.Fatalf("expected 1 invocation, got %d", len(records))
			}
		})
	}
}

func TestAgyPlanApproval_ACP(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	planPath := filepath.Join(dir, "implementation_plan.md")
	recordFile := filepath.Join(dir, "acp-records.json")
	wantPlan := "# ACP Plan\n1. Do ACP work\n"

	h := New(Options{
		Binary:  exe,
		Channel: func() string { return ChannelACP },
	})
	h.versions = map[string]string{exe: MinACPVersion}
	t.Cleanup(func() { _ = h.Close() })

	approver := &fakePlanApprover{
		interact: func(_ context.Context, _ harness.ApprovalRequest) (harness.ApprovalResponse, error) {
			return harness.ApprovalResponse{Decision: harness.DecisionAllow}, nil
		},
	}

	turn := harness.Turn{
		ThreadID:        "acp-plan-thread",
		WorkDir:         dir,
		Prompt:          "Plan via ACP",
		Mode:            harness.ModeReadOnly,
		InteractionMode: "plan",
		PlanExitMode:    harness.ModeAuto,
		Approver:        approver,
		Env: []string{
			fakeAgyEnv + "=acp_transport",
			"MCODE_TEST_ACP_PLAN_FILE=" + planPath,
			"MCODE_TEST_AGY_PLAN_FILE=" + planPath,
			"MCODE_TEST_AGY_PLAN_CONTENT=" + wantPlan,
			"MCODE_TEST_AGY_RECORD_FILE=" + recordFile,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var doneCount int
	if err := h.Run(ctx, turn, func(ev harness.Event) {
		if ev.Type == harness.EventDone {
			doneCount++
		}
	}); err != nil {
		t.Fatalf("Run ACP failed: %v", err)
	}

	reqs, acks := approver.snapshot()
	if len(reqs) != 1 || reqs[0].Kind != harness.RequestKindPlan || reqs[0].Plan != wantPlan {
		t.Fatalf("unexpected ACP plan request: %+v", reqs)
	}
	if len(acks) != 1 || acks[0] != nil {
		t.Fatalf("unexpected ACP acks: %v", acks)
	}
	if doneCount != 2 {
		t.Fatalf("expected 2 EventDone in ACP plan approval, got %d", doneCount)
	}
	records := readFakeInvocations(t, recordFile)
	if len(records) != 2 {
		t.Fatalf("expected 2 ACP prompt invocations, got %d", len(records))
	}
	if records[0].Args[0] != "mode=plan" || records[1].Args[0] != "mode=auto" {
		t.Fatalf("unexpected ACP modes across rounds: round1=%v round2=%v", records[0].Args, records[1].Args)
	}
	if records[1].Stdin != "Implement the plan." {
		t.Fatalf("round 2 prompt = %q, want Implement the plan.", records[1].Stdin)
	}
}
