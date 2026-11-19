package agy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mcode/internal/harness"
)

const fakeAgyPlanText = "Review the implementation plan."
const fakeAgyPlanContent = "# Implementation Plan\n\n1. Add tests\n"
const fakeAgyPlanSession = "plan-conv-001"

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
	writeMsg(map[string]any{"event": "init", "conversation_id": fakeAgyPlanSession})
	for index, input := range fakeAgyPlanInputs() {
		toolID := "plan-write-" + strconv.Itoa(index)
		writeMsg(map[string]any{"event": "step_update", "step_update": map[string]any{
			"step_index": index, "step_type": "tool", "tool_name": "write_file", "tool_id": toolID, "state": "DONE",
			"tool_info": map[string]any{"name": "write_file", "id": toolID, "parameters": input, "output": "ok"},
		}})
	}
	writeMsg(map[string]any{"event": "step_update", "step_update": map[string]any{
		"step_index": 2, "step_type": "agent_response", "text_delta": fakeAgyPlanText,
	}})
	writeMsg(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS", "conversation_id": fakeAgyPlanSession}})
	os.Exit(0)
}

func handleFakeAgyACPPlanPrompt(id any, currentMode string, params map[string]any) {
	items := params["prompt"].([]any)
	promptText, _ := items[0].(map[string]any)["text"].(string)
	sessionID, _ := params["sessionId"].(string)
	recordFakeInvocation(os.Getenv("MCODE_TEST_AGY_RECORD_FILE"), []string{currentMode, sessionID, strconv.Itoa(len(items))}, promptText)
	writeUpdate := func(update map[string]any) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": update},
		})
	}
	for index, input := range fakeAgyPlanInputs() {
		toolID := "plan-write-" + strconv.Itoa(index)
		writeUpdate(map[string]any{"sessionUpdate": "tool_call", "toolCallId": toolID, "name": "write_file", "rawInput": input})
		writeUpdate(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": toolID, "status": "completed", "output": "ok"})
	}
	writeUpdate(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": fakeAgyPlanText}})
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"},
	})
}

func fakeAgyPlanInputs() []map[string]any {
	variant := os.Getenv("MCODE_TEST_AGY_PLAN_ARTIFACT")
	if variant == "" {
		return nil
	}
	var metadata any = map[string]any{"RequestFeedback": variant != "no-feedback", "UserFacing": true}
	if variant == "string" {
		metadata = `{"RequestFeedback":true,"UserFacing":true}`
	}
	path := os.Getenv("MCODE_TEST_AGY_PLAN_FILE")
	input := map[string]any{"Path": path, "Content": fakeAgyPlanContent, "ArtifactMetadata": metadata, "Overwrite": true}
	if variant == "draft" {
		return []map[string]any{{"Path": path, "Content": "# Draft", "ArtifactMetadata": metadata}, input}
	}
	return []map[string]any{input}
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
			if tc.channel == ChannelACP && (len(records[0].Args) == 0 || records[0].Args[0] != "plan") {
				t.Fatalf("ACP mode = %v, want plan", records[0].Args)
			}
		})
	}
}

type planArtifactApprover struct {
	noPlanApprover
	responses []harness.ApprovalResponse
	requests  []harness.ApprovalRequest
	acks      []error
	err       error
	cancel    context.CancelFunc
}

func (a *planArtifactApprover) Interact(_ context.Context, req harness.ApprovalRequest) (harness.ApprovalResponse, error) {
	index := len(a.requests)
	a.requests = append(a.requests, req)
	if index >= len(a.responses) {
		a.test.Errorf("unexpected Interact: %+v", req)
		return harness.ApprovalResponse{Decision: harness.DecisionDeny}, nil
	}
	if a.cancel != nil {
		a.cancel()
	}
	return a.responses[index], a.err
}

func (a *planArtifactApprover) AckDelivery(_ harness.ApprovalRequest, err error) {
	a.acks = append(a.acks, err)
}

type planArtifactSandbox struct {
	modes []harness.PermissionMode
}

func (s *planArtifactSandbox) Command(ctx context.Context, _ string, mode harness.PermissionMode, name string, args ...string) (*exec.Cmd, func(), error) {
	s.modes = append(s.modes, mode)
	return exec.CommandContext(ctx, name, args...), nil, nil
}

func TestAgyPlanArtifact(t *testing.T) {
	const feedback = "Add rollback steps."
	allow := harness.ApprovalResponse{Decision: harness.DecisionAllow}
	deny := harness.ApprovalResponse{Decision: harness.DecisionDeny}
	interactionErr := errors.New("interaction failed")
	for _, tc := range []struct {
		name, variant, wantPrompt, wantACPMode string
		printMode, build, cancel               bool
		exitMode, wantMode                     harness.PermissionMode
		responses                              []harness.ApprovalResponse
		wantErr                                error
	}{
		{name: "approve last artifact", variant: "draft", responses: []harness.ApprovalResponse{allow},
			exitMode: harness.ModeAuto, wantMode: harness.ModeAuto, wantPrompt: "Implement the plan.", wantACPMode: "default"},
		{name: "string metadata", variant: "string", printMode: true, responses: []harness.ApprovalResponse{allow},
			exitMode: harness.ModeAuto, wantMode: harness.ModeAuto, wantPrompt: "Implement the plan."},
		{name: "empty exit mode", variant: "object", responses: []harness.ApprovalResponse{allow},
			wantMode: harness.ModeReadOnly, wantPrompt: "Implement the plan.", wantACPMode: "default"},
		{name: "revise then reject", variant: "object", responses: []harness.ApprovalResponse{{Decision: harness.DecisionDeny, Feedback: feedback}, deny},
			wantMode: harness.ModeReadOnly, wantPrompt: feedback, wantACPMode: "plan"},
		{name: "reject without feedback", variant: "object", responses: []harness.ApprovalResponse{deny}},
		{name: "build ignores artifact", variant: "object", build: true},
		{name: "feedback disabled", variant: "no-feedback"},
		{name: "different basename", variant: "object", build: false},
		{name: "interaction error", variant: "object", responses: []harness.ApprovalResponse{allow}, wantErr: interactionErr},
		{name: "cancel during approval", variant: "object", responses: []harness.ApprovalResponse{allow}, cancel: true, wantErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			planPath := filepath.Join(dir, "implementation_plan.md")
			if tc.name == "different basename" {
				planPath = filepath.Join(dir, "notes.md")
			}
			recordPath := filepath.Join(dir, "records.json")
			imagePath := filepath.Join(dir, "attachment.png")
			if err := os.WriteFile(imagePath, []byte("image fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			channel, fakeMode := ChannelACP, "acp_transport"
			if tc.printMode {
				channel, fakeMode = ChannelPrintMode, "plan_approval"
			}
			h := New(Options{Binary: exe, Channel: func() string { return channel }})
			h.versions = map[string]string{exe: MinACPVersion}
			t.Cleanup(func() { _ = h.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			approver := &planArtifactApprover{noPlanApprover: noPlanApprover{test: t}, responses: tc.responses}
			if tc.cancel {
				approver.cancel = cancel
			} else {
				approver.err = tc.wantErr
			}
			sandbox := &planArtifactSandbox{}
			turn := harness.Turn{
				ThreadID: "agy-plan", WorkDir: dir, Prompt: "Create a plan", Images: []string{imagePath},
				Mode: harness.ModeReadOnly, InteractionMode: "plan", PlanExitMode: tc.exitMode,
				Approver: approver, Sandbox: sandbox,
				Env: []string{
					fakeAgyEnv + "=" + fakeMode,
					"MCODE_TEST_AGY_PLAN_FILE=" + planPath,
					"MCODE_TEST_ACP_PLAN_FILE=" + planPath,
					"MCODE_TEST_AGY_RECORD_FILE=" + recordPath,
					"MCODE_TEST_AGY_PLAN_ARTIFACT=" + tc.variant,
				},
			}
			if tc.build {
				turn.InteractionMode = ""
				turn.Mode = harness.ModeAuto
			}
			var starts atomic.Int32
			err = h.Run(ctx, turn, func(event harness.Event) {
				if event.Type == harness.EventToolStart && event.ToolName == "write_file" {
					starts.Add(1)
				}
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Run error = %v, want %v", err, tc.wantErr)
			}
			if len(approver.requests) != len(tc.responses) || len(approver.acks) != len(tc.responses) {
				t.Fatalf("requests = %v, acks = %v, want %d of each", approver.requests, approver.acks, len(tc.responses))
			}
			for index, req := range approver.requests {
				if req.ToolID != "exit_plan_mode" || req.ToolName != "exit_plan_mode" || req.Kind != harness.RequestKindPlan || req.Summary != "Implement this plan" || req.Plan != fakeAgyPlanContent {
					t.Errorf("approval request = %+v", req)
				}
				if !errors.Is(approver.acks[index], tc.wantErr) {
					t.Errorf("delivery error = %v, want %v", approver.acks[index], tc.wantErr)
				}
			}
			data, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatal(err)
			}
			var records []fakeInvocationRecord
			if err := json.Unmarshal(data, &records); err != nil {
				t.Fatal(err)
			}
			wantModes := []harness.PermissionMode{turn.Mode}
			if tc.wantPrompt != "" {
				wantModes = append(wantModes, tc.wantMode)
			}
			if len(records) != len(wantModes) || !slices.Equal(sandbox.modes, wantModes) {
				t.Fatalf("prompts = %d, modes = %v, want modes %v", len(records), sandbox.modes, wantModes)
			}
			wantStarts := int32(len(records))
			if tc.variant == "draft" {
				wantStarts *= 2
			}
			if starts.Load() != wantStarts {
				t.Fatalf("forwarded tool starts = %d, want %d", starts.Load(), wantStarts)
			}
			if tc.wantPrompt == "" {
				return
			}
			if tc.printMode {
				var input userMsgPayload
				if err := json.Unmarshal([]byte(records[1].Stdin), &input); err != nil {
					t.Fatal(err)
				}
				prompt := strings.TrimPrefix(input.Message.Content, visibleOutputPromptGuard)
				if prompt := strings.TrimPrefix(prompt, questionBridgePromptGuard); prompt != tc.wantPrompt {
					t.Errorf("second prompt = %q, want %q", prompt, tc.wantPrompt)
				}
				index := slices.Index(records[1].Args, "--conversation")
				if index < 0 || index+1 >= len(records[1].Args) || records[1].Args[index+1] != fakeAgyPlanSession {
					t.Errorf("continuation did not resume the session: %v", records[1].Args)
				}
				return
			}
			if want := []string{"plan", "new-session", "2"}; !slices.Equal(records[0].Args, want) {
				t.Errorf("initial ACP mode/session/prompt item count = %v, want %v", records[0].Args, want)
			}
			if want := []string{tc.wantACPMode, "new-session", "1"}; !slices.Equal(records[1].Args, want) || records[1].Stdin != tc.wantPrompt {
				t.Errorf("second ACP prompt = %q, mode/session/prompt item count = %v, want %q and %v", records[1].Stdin, records[1].Args, tc.wantPrompt, want)
			}
		})
	}
}
