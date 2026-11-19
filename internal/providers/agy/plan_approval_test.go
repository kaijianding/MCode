package agy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mcode/internal/harness"
)

const fakeAgyPlanText = "Review the implementation plan."
const fakeAgyPlanContent = "# Implementation Plan\n\n1. Add tests\n"

type noPlanApprover struct{ test *testing.T }

func (a noPlanApprover) Approve(_ context.Context, req harness.ApprovalRequest) (harness.Decision, error) {
	a.test.Errorf("unexpected Approve: %+v", req)
	return harness.DecisionDeny, nil
}

func (a noPlanApprover) Interact(_ context.Context, req harness.ApprovalRequest) (harness.ApprovalResponse, error) {
	a.test.Errorf("unexpected Interact: %+v", req)
	return harness.ApprovalResponse{Decision: harness.DecisionDeny}, nil
}

func runFakeAgyPlanApproval(firstLine []byte, writeMsg func(any)) {
	recordFakeInvocation(os.Getenv("MCODE_TEST_AGY_RECORD_FILE"), os.Args, string(firstLine))
	planPath := os.Getenv("MCODE_TEST_AGY_PLAN_FILE")
	if err := os.WriteFile(planPath, []byte(fakeAgyPlanContent), 0o600); err != nil {
		os.Exit(11)
	}
	const sessionID = "plan-conv-001"
	writeMsg(map[string]any{"event": "init", "conversation_id": sessionID})
	writeMsg(map[string]any{"event": "step_update", "step_update": map[string]any{
		"step_index": 0, "step_type": "tool", "tool_name": "write_to_file", "tool_id": "plan-write", "state": "DONE",
		"tool_info": map[string]any{"name": "write_to_file", "id": "plan-write", "parameters": map[string]any{"TargetFile": planPath}, "output": "ok"},
	}})
	writeMsg(map[string]any{"event": "step_update", "step_update": map[string]any{
		"step_index": 1, "step_type": "agent_response", "text_delta": fakeAgyPlanText,
	}})
	writeMsg(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS", "conversation_id": sessionID}})
	os.Exit(0)
}

func handleFakeAgyACPPlanPrompt(id any, currentMode string, params map[string]any) {
	planPath := os.Getenv("MCODE_TEST_AGY_PLAN_FILE")
	if err := os.WriteFile(planPath, []byte(fakeAgyPlanContent), 0o600); err != nil {
		os.Exit(21)
	}
	items := params["prompt"].([]any)
	promptText, _ := items[0].(map[string]any)["text"].(string)
	recordFakeInvocation(os.Getenv("MCODE_TEST_AGY_RECORD_FILE"), []string{currentMode}, promptText)
	for _, update := range []map[string]any{
		{"sessionUpdate": "tool_call", "toolCallId": "plan-write", "name": "write_to_file", "rawInput": map[string]any{"TargetFile": planPath}},
		{"sessionUpdate": "tool_call_update", "toolCallId": "plan-write", "status": "completed", "output": "ok"},
		{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": fakeAgyPlanText}},
	} {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": update},
		})
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"},
	})
}

func TestAgyRunNoSynthesizedPlanApproval(t *testing.T) {
	for _, tc := range []struct{ channel, fakeMode string }{
		{ChannelPrintMode, "plan_approval"},
		{ChannelACP, "acp_transport"},
	} {
		t.Run(tc.channel, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			planPath := filepath.Join(dir, "implementation_plan.md")
			recordPath := filepath.Join(dir, "records.json")
			h := New(Options{Binary: exe, Channel: func() string { return tc.channel }})
			h.versions = map[string]string{exe: MinACPVersion}
			t.Cleanup(func() { _ = h.Close() })
			turn := harness.Turn{
				ThreadID:        "agy-plan",
				WorkDir:         dir,
				Prompt:          "Create a plan",
				Mode:            harness.ModeReadOnly,
				InteractionMode: "plan",
				Approver:        noPlanApprover{test: t},
				Env: []string{
					fakeAgyEnv + "=" + tc.fakeMode,
					"MCODE_TEST_AGY_PLAN_FILE=" + planPath,
					"MCODE_TEST_ACP_PLAN_FILE=" + planPath,
					"MCODE_TEST_AGY_RECORD_FILE=" + recordPath,
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var sawText atomic.Bool
			if err := h.Run(ctx, turn, func(ev harness.Event) {
				if ev.Type == harness.EventText && ev.Text == fakeAgyPlanText {
					sawText.Store(true)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(planPath); err != nil || string(data) != fakeAgyPlanContent {
				t.Fatalf("plan file = %q, err = %v", data, err)
			}
			if !sawText.Load() {
				t.Fatal("missing plan response")
			}
			data, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatal(err)
			}
			var records []fakeInvocationRecord
			if err := json.Unmarshal(data, &records); err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 {
				t.Fatalf("got %d prompts, want 1", len(records))
			}
			if tc.channel == ChannelPrintMode && !strings.Contains(records[0].Stdin, "You are in Plan Mode") {
				t.Fatal("missing print mode plan guard")
			}
			if tc.channel == ChannelACP && (len(records[0].Args) != 1 || records[0].Args[0] != "plan") {
				t.Fatalf("ACP mode = %v, want plan", records[0].Args)
			}
		})
	}
}
