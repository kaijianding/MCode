package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcode/internal/harness"
)

type planApprover struct {
	interact func(context.Context, harness.ApprovalRequest) (harness.ApprovalResponse, error)
	requests []harness.ApprovalRequest
	acks     []error
}

func (a *planApprover) Approve(context.Context, harness.ApprovalRequest) (harness.Decision, error) {
	return harness.DecisionDeny, nil
}

func (a *planApprover) Interact(ctx context.Context, req harness.ApprovalRequest) (harness.ApprovalResponse, error) {
	a.requests = append(a.requests, req)
	return a.interact(ctx, req)
}

func (a *planApprover) AckDelivery(_ harness.ApprovalRequest, err error) {
	a.acks = append(a.acks, err)
}

func planTestTurn(t *testing.T, mode string) (*Harness, harness.Turn, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "requests.jsonl")
	h := New(Options{})
	t.Cleanup(func() { _ = h.Close() })
	return h, harness.Turn{
		ID: "plan_test", WorkDir: t.TempDir(), Mode: harness.ModeReadOnly,
		Prompt: "给出实施计划", InteractionMode: "plan", Model: "gpt-5-codex", Effort: "high",
		PlanExitMode: harness.ModeFullAccess,
		Sandbox: &mockSandbox{cmdFunc: func(ctx context.Context) *exec.Cmd {
			return codexAppServerCommand(ctx, mode, logPath)
		}},
	}, logPath
}

func planTurnRequests(t *testing.T, path string) []turnStartLogRequest {
	t.Helper()
	var turns []turnStartLogRequest
	for _, line := range readLines(t, path) {
		var req turnStartLogRequest
		if err := json.Unmarshal(line, &req); err != nil {
			t.Fatal(err)
		}
		if req.Method == "turn/start" {
			turns = append(turns, req)
		}
	}
	return turns
}

func TestPlanModeRequestsPlanApproval(t *testing.T) {
	h, turn, logPath := planTestTurn(t, appServerPlan)
	completed := 0
	a := &planApprover{interact: func(context.Context, harness.ApprovalRequest) (harness.ApprovalResponse, error) {
		if completed != 1 {
			t.Errorf("approval must follow turn/completed, got %d completed turns", completed)
		}
		return harness.ApprovalResponse{Decision: harness.DecisionAllow}, nil
	}}
	turn.Approver = a
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Run(ctx, turn, func(ev harness.Event) {
		if ev.Type == harness.EventDone {
			completed++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(a.requests) != 1 || a.requests[0].Kind != harness.RequestKindPlan || a.requests[0].Plan != "没有流式增量的计划全文" {
		t.Fatalf("unexpected plan approval: %+v", a.requests)
	}
	turns := planTurnRequests(t, logPath)
	if len(turns) != 2 || completed != 2 {
		t.Fatalf("expected planning and implementation turns: %+v, completed=%d", turns, completed)
	}
	if turns[1].Params.ThreadID != turns[0].Params.ThreadID || turns[1].Params.CollaborationMode == nil || turns[1].Params.CollaborationMode.Mode != "default" || len(turns[1].Params.Input) != 1 || !strings.Contains(turns[1].Params.Input[0].Text, "Implement the plan.") {
		t.Fatalf("unexpected implementation turn: %+v", turns[1])
	}
	if len(a.acks) != 1 || a.acks[0] != nil || h.busyProcCount() != 0 {
		t.Fatalf("approval delivery/busy state: acks=%v, busy=%d", a.acks, h.busyProcCount())
	}
}

func TestBuildTurnSendsDefaultCollaborationMode(t *testing.T) {
	for _, interaction := range []string{"build", ""} {
		for _, model := range []string{"gpt-5-codex", ""} {
			params := buildTurnStartParams(harness.Turn{InteractionMode: interaction, Model: model, Effort: "high"}, "th_1")
			mode, ok := params["collaborationMode"].(map[string]any)
			if model == "" {
				if _, present := params["collaborationMode"]; present {
					t.Fatalf("empty model: %+v", params)
				}
			} else if !ok || mode["mode"] != "default" {
				t.Fatalf("interaction=%q: %+v", interaction, params)
			}
		}
	}
}

func TestPlanApprovalFeedbackAndPermissions(t *testing.T) {
	for _, tc := range []struct {
		name, sandbox, policy string
		mode                  harness.PermissionMode
		hardLocked            bool
	}{
		{"full access", "dangerFullAccess", "never", harness.ModeFullAccess, false},
		{"hard locked", "workspaceWrite", "on-request", harness.ModeFullAccess, true},
		{"supervised", "readOnly", "untrusted", harness.ModeSupervised, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, turn, logPath := planTestTurn(t, appServerPlan)
			turn.PlanExitMode, turn.HardLocked = tc.mode, tc.hardLocked
			turn.AdditionalDirs = []string{t.TempDir()}
			a := &planApprover{}
			a.interact = func(context.Context, harness.ApprovalRequest) (harness.ApprovalResponse, error) {
				if len(a.requests) == 1 {
					return harness.ApprovalResponse{Decision: harness.DecisionDeny, Feedback: "/review revise the plan"}, nil
				}
				return harness.ApprovalResponse{Decision: harness.DecisionAllow}, nil
			}
			turn.Approver = a
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := h.Run(ctx, turn, nil); err != nil {
				t.Fatal(err)
			}
			turns := planTurnRequests(t, logPath)
			if len(turns) != 3 || len(a.requests) != 2 || len(a.acks) != 2 {
				t.Fatalf("expected revision then implementation: turns=%d, requests=%d, acks=%v", len(turns), len(a.requests), a.acks)
			}
			if turns[1].Params.CollaborationMode.Mode != "plan" || turns[1].Params.Input[0].Text != "/review revise the plan" || turns[2].Params.CollaborationMode.Mode != "default" {
				t.Fatalf("unexpected turns: %+v", turns)
			}
			var params map[string]json.RawMessage
			for _, line := range readLines(t, logPath) {
				var req struct {
					Method string                     `json:"method"`
					Params map[string]json.RawMessage `json:"params"`
				}
				if err := json.Unmarshal(line, &req); err != nil {
					t.Fatal(err)
				}
				if req.Method == "turn/start" {
					params = req.Params
				}
			}
			var policy struct {
				Type          string   `json:"type"`
				WritableRoots []string `json:"writableRoots"`
			}
			if err := json.Unmarshal(params["sandboxPolicy"], &policy); err != nil {
				t.Fatal(err)
			}
			if policy.Type != tc.sandbox || string(params["approvalPolicy"]) != `"`+tc.policy+`"` {
				t.Fatalf("unexpected permission: %s %s", params["sandboxPolicy"], params["approvalPolicy"])
			}
			if tc.sandbox == "workspaceWrite" && (len(policy.WritableRoots) != 1 || policy.WritableRoots[0] != turn.AdditionalDirs[0]) {
				t.Fatalf("lost writable roots: %+v", policy)
			}
			if h.busyProcCount() != 0 || h.poolProcs()[0].getActiveTurn() != nil {
				t.Fatal("process still occupied")
			}
		})
	}
}

func TestPlanApprovalStopsCleanly(t *testing.T) {
	approvalErr := errors.New("approval failed")
	for _, action := range []string{"deny", "error", "cancel", "exit", "no interactor", "no plan"} {
		t.Run(action, func(t *testing.T) {
			mode := appServerPlan
			if action == "no plan" {
				mode = appServerPong
			}
			h, turn, logPath := planTestTurn(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			a := &planApprover{interact: func(ctx context.Context, _ harness.ApprovalRequest) (harness.ApprovalResponse, error) {
				switch action {
				case "error":
					return harness.ApprovalResponse{}, approvalErr
				case "cancel":
					cancel()
					<-ctx.Done()
					return harness.ApprovalResponse{}, ctx.Err()
				case "exit":
					h.ForceStopTurn(turn.ID)
					<-ctx.Done()
					return harness.ApprovalResponse{}, ctx.Err()
				}
				return harness.ApprovalResponse{Decision: harness.DecisionDeny}, nil
			}}
			turn.Approver = a
			if action == "no interactor" {
				turn.Approver = &mockApprover{}
			}
			err := h.Run(ctx, turn, nil)
			wantErr := action == "error" || action == "cancel" || action == "exit"
			if (err != nil) != wantErr || (action == "error" && !errors.Is(err, approvalErr)) || (action == "cancel" && !errors.Is(err, context.Canceled)) {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(planTurnRequests(t, logPath)) != 1 || h.busyProcCount() != 0 {
				t.Fatal("unexpected continuation or occupied process")
			}
			if action != "no interactor" && action != "no plan" && (len(a.acks) != 1 || (a.acks[0] != nil) != wantErr) {
				t.Fatalf("unexpected delivery: %v", a.acks)
			}
			if (action == "no plan" || action == "no interactor") && len(a.requests) != 0 {
				t.Fatal("unexpected approval")
			}
		})
	}
}

func TestPlanApprovalContent(t *testing.T) {
	for _, tc := range []struct{ name, status, full, want string }{
		{"fallback", "completed", "", "说明\n\n- 新计划"},
		{"full plan", "completed", "# 计划\n1. 实施", "# 计划\n1. 实施"},
		{"failed", "failed", "计划", ""},
		{"interrupted", "interrupted", "计划", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewProtocolHandler(harness.Turn{InteractionMode: "plan"})
			h.HandleNotification(&JSONRPCMessage{Method: "turn/plan/updated", Params: json.RawMessage(`{"plan":[{"step":"旧计划","status":"pending"}]}`)}, nil)
			h.HandleNotification(&JSONRPCMessage{Method: "turn/plan/updated", Params: json.RawMessage(`{"explanation":"说明","plan":[{"step":"新计划","status":"pending"}]}`)}, nil)
			if tc.full != "" {
				params, _ := json.Marshal(map[string]any{"item": map[string]any{"type": "plan", "id": "plan_1", "text": tc.full}})
				h.HandleNotification(&JSONRPCMessage{Method: "item/completed", Params: params}, nil)
			}
			if h.planForApproval() != "" {
				t.Fatal("plan is not ready before turn completion")
			}
			params, _ := json.Marshal(map[string]any{"turn": map[string]any{"status": tc.status}})
			h.HandleNotification(&JSONRPCMessage{Method: "turn/completed", Params: params}, nil)
			if got := h.planForApproval(); got != tc.want {
				t.Fatalf("plan=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestAppClientCallAfterReaderExit(t *testing.T) {
	c := newAppClient(nopWriteCloser{&strings.Builder{}})
	close(c.readerDoneCh)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.call(ctx, "turn/start", nil); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected closed connection without waiting: %v", err)
	}
}

type rpcWriteFunc func([]byte) (int, error)

func (f rpcWriteFunc) Write(p []byte) (int, error) { return f(p) }

func TestAppClientCallKeepsResponseAtReaderExit(t *testing.T) {
	c := newAppClient(nil)
	c.stdin = nopWriteCloser{rpcWriteFunc(func(p []byte) (int, error) {
		c.mu.Lock()
		for _, ch := range c.pending {
			ch <- &JSONRPCMessage{Result: json.RawMessage(`{"turn":{"id":"turn_1"}}`)}
			close(ch)
		}
		c.mu.Unlock()
		close(c.readerDoneCh)
		return len(p), nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if res, err := c.call(ctx, "turn/start", nil); err != nil || string(res) != `{"turn":{"id":"turn_1"}}` {
		t.Fatalf("lost response before EOF: %s, %v", res, err)
	}
}
