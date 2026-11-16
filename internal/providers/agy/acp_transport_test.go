package agy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"mcode/internal/harness"
	"mcode/internal/providers/acp"
)

func TestAgyChannelSelect(t *testing.T) {
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
	for _, mode := range []harness.PermissionMode{harness.ModeFullAccess, harness.ModeAuto, harness.ModeAutoAcceptEdits, harness.ModeSupervised, harness.ModeReadOnly} {
		want := []string{"acp"}
		if mode == harness.ModeFullAccess {
			want = append(want, "--dangerously-skip-permissions")
		}
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

func runFakeAgyACP() {
	reader := bufio.NewReader(os.Stdin)
	var setup map[string]any
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
			setup = params
			result["sessionId"] = "new-session"
		case "session/prompt":
			payload, _ := json.Marshal(map[string]any{"args": os.Args[1:], "prompt": params["prompt"], "setup": setup, "env": os.Getenv("MCODE_TEST_ROUTED_ACCOUNT")})
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"text": string(payload)}}}})
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
}

func (r *acpTestRouter) Route(session, _ string, _ []string) (string, []string, []string, error) {
	return session, []string{fakeAgyEnv + "=acp_transport", "MCODE_TEST_ROUTED_ACCOUNT=" + session}, []string{"--gemini_dir", session}, nil
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
				if !reflect.DeepEqual(got.Args, []string{"acp", "--gemini_dir", account}) || got.Env != account || len(got.Prompt) != 1 || got.Prompt[0].Text != "raw prompt" {
					t.Errorf("wrong ACP route: %+v", got)
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
