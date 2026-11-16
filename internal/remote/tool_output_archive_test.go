package remote

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"mcode/internal/harness"
	"mcode/internal/remote/wire"
	"mcode/internal/store"
)

func TestToolOutputArchive(t *testing.T) {
	dir := t.TempDir()
	threadID, messageID := "00000000-0000-0000-0000-000000000101", "00000000-0000-0000-0000-000000000102"
	short := strings.Repeat("a", 2000)
	if size, err := archiveToolOutput(dir, threadID, messageID, short); err != nil || size != 0 {
		t.Fatalf("short archive size=%d err=%v", size, err)
	}
	if _, err := os.Stat(toolOutputPath(dir, threadID, messageID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("short output created an archive: %v", err)
	}
	long := strings.Repeat("界", 2001)
	size, err := archiveToolOutput(dir, threadID, messageID, long)
	if err != nil || size != int64(len(long)) {
		t.Fatalf("archive size=%d err=%v", size, err)
	}
	data, err := os.ReadFile(toolOutputPath(dir, threadID, messageID))
	if err != nil || string(data) != long {
		t.Fatalf("archived text len=%d err=%v", len(data), err)
	}
	m := messageToStore(threadID, wire.ThreadMessage{OutputBytes: &size})
	if m.OutputBytes != size || messageFromStore(m).OutputBytes == nil {
		t.Fatalf("outputBytes not retained: %#v", m)
	}
	t.Run("size limit", func(t *testing.T) {
		large := strings.Repeat("界", toolOutputArchiveMaxBytes/3+1)
		size, err := archiveToolOutput(dir, threadID, messageID, large)
		if err != nil || size > toolOutputArchiveMaxBytes || size != int64(toolOutputArchiveMaxBytes/3*3) {
			t.Fatalf("bounded archive size=%d err=%v", size, err)
		}
		data, err := os.ReadFile(toolOutputPath(dir, threadID, messageID))
		if err != nil || !utf8.Valid(data) {
			t.Fatalf("invalid archive UTF-8: %v", err)
		}
	})
	t.Run("tool end and persisted history", func(t *testing.T) {
		ctx := context.Background()
		for _, tc := range []struct {
			name, output string
			failWrite    bool
			toolError    bool
		}{
			{"long", long, false, false}, {"long ascii", short + "a", false, false}, {"long failed", short + "a", false, true}, {"short", short, false, false}, {"write failure", long, true, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				s, err := store.Open(filepath.Join(dir, "mcode.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				hub := NewStreamHub(StreamHubOptions{})
				defer hub.Close()
				b := NewMemoryBackend(hub)
				defer b.Close()
				b.SetDataDir(dir)
				if tc.failWrite {
					if err := os.WriteFile(filepath.Join(dir, "tool-outputs"), []byte("blocked"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := b.AttachStore(ctx, s); err != nil {
					t.Fatal(err)
				}
				b.SetHarness(&mockHarness{runFunc: func(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
					emit(harness.Event{Type: harness.EventToolStart, ToolID: "call", ToolName: "Bash"})
					emit(harness.Event{Type: harness.EventToolEnd, ToolID: "call", Output: tc.output, IsError: tc.toolError})
					emit(harness.Event{Type: harness.EventDone})
					return nil
				}}, dir, harness.ModeAgent)
				b.AddProject(wire.ProjectSummary{ID: "project", Name: "project", Path: dir})
				b.AddThread(wire.ThreadSummary{ID: threadID, ProjectID: "project", Title: "Tool output"}, nil)
				if _, err := b.SendMessage(ctx, wire.SendMessageParams{ThreadID: threadID, Text: "run", ActionID: messageID}); err != nil {
					t.Fatal(err)
				}
				waitThreadIdle(t, b, ctx, threadID)
				msg := waitToolMessage(t, b, ctx, threadID, "Bash")
				wantBytes := int64(0)
				if strings.HasPrefix(tc.name, "long") {
					wantBytes = int64(len(tc.output))
				}
				if valueOrZero(msg.OutputBytes) != wantBytes {
					t.Fatalf("tool outputBytes=%v want=%d", msg.OutputBytes, wantBytes)
				}
				wantStatus := "completed"
				if tc.toolError {
					wantStatus = "failed"
				}
				if msg.ToolStatus == nil || *msg.ToolStatus != wantStatus {
					t.Fatalf("tool status=%v", msg.ToolStatus)
				}
				if wantBytes > 0 {
					if wantBytes <= int64(len(msg.Text)) {
						t.Fatalf("archive must exceed preview: bytes=%d preview=%d", wantBytes, len(msg.Text))
					}
					data, err := os.ReadFile(toolOutputPath(dir, threadID, msg.ID))
					if err != nil || string(data) != tc.output {
						t.Fatalf("tool end archive mismatch: %v", err)
					}
				}
				b.Close()
				reloadedHub := NewStreamHub(StreamHubOptions{})
				defer reloadedHub.Close()
				reloaded := NewMemoryBackend(reloadedHub)
				defer reloaded.Close()
				if err := reloaded.AttachStore(ctx, s); err != nil {
					t.Fatal(err)
				}
				d := NewDispatcher(&fakeAuth{}, reloaded)
				req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "threads.history", "params": map[string]any{"threadID": threadID}})
				response := d.Handle(ctx, &fakeConn{info: ClientInfo{Authenticated: true}}, req)
				var decoded struct {
					Result wire.HistoryPage `json:"result"`
				}
				if err := json.Unmarshal(response, &decoded); err != nil {
					t.Fatal(err)
				}
				for _, got := range decoded.Result.Messages {
					if got.ID == msg.ID {
						if valueOrZero(got.OutputBytes) != wantBytes {
							t.Fatalf("persisted outputBytes=%v want=%d", got.OutputBytes, wantBytes)
						}
						return
					}
				}
				t.Fatalf("tool missing from history: %s", response)
			})
		}
	})
}

func TestThreadsToolOutputRPC(t *testing.T) {
	ctx := context.Background()
	threadID, messageID := "00000000-0000-0000-0000-000000000201", "00000000-0000-0000-0000-000000000202"
	backend := NewMemoryBackend(NewStreamHub(StreamHubOptions{}))
	defer backend.Close()
	dir := t.TempDir()
	backend.SetDataDir(dir)
	backend.AddThread(wire.ThreadSummary{ID: threadID}, nil)
	full := strings.Repeat("界a", 1100)
	if _, err := archiveToolOutput(dir, threadID, messageID, full); err != nil {
		t.Fatal(err)
	}
	part, err := backend.ThreadToolOutput(ctx, threadID, messageID, 1, 8)
	if err != nil || part.Offset != 3 || part.Text != "a界a界" || part.EOF {
		t.Fatalf("first chunk=%+v err=%v", part, err)
	}
	end, err := backend.ThreadToolOutput(ctx, threadID, messageID, int64(len(full)-3), 10)
	if err != nil || !end.EOF {
		t.Fatalf("final chunk=%+v err=%v", end, err)
	}
	var chunks strings.Builder
	for offset := int64(0); ; {
		part, err := backend.ThreadToolOutput(ctx, threadID, messageID, offset, 11)
		if err != nil || !utf8.ValidString(part.Text) {
			t.Fatalf("chunk=%+v err=%v", part, err)
		}
		chunks.WriteString(part.Text)
		if part.EOF {
			break
		}
		next := part.Offset + int64(len(part.Text))
		if next <= offset {
			t.Fatal("chunk made no progress")
		}
		offset = next
	}
	if chunks.String() != full {
		t.Fatal("chunked read differs from archive")
	}
	if _, err := backend.ThreadToolOutput(ctx, threadID, "../../escape", 0, 8); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unsafe ID error=%v", err)
	}

	d := NewDispatcher(&fakeAuth{}, backend)
	validRequest, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "threads.toolOutput", "params": map[string]any{"threadID": threadID, "messageID": messageID, "offset": 1, "length": 8}})
	validResponse := d.Handle(ctx, &fakeConn{info: ClientInfo{Authenticated: true}}, validRequest)
	var decoded struct {
		Result wire.ThreadsToolOutputResult `json:"result"`
	}
	if err := json.Unmarshal(validResponse, &decoded); err != nil || decoded.Result.Text != "a界a界" || decoded.Result.Offset != 3 {
		t.Fatalf("RPC response=%s err=%v", validResponse, err)
	}
	badRequest, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "threads.toolOutput", "params": map[string]any{"threadID": threadID, "messageID": "../../escape"}})
	response := string(d.Handle(ctx, &fakeConn{info: ClientInfo{Authenticated: true}}, badRequest))
	if !strings.Contains(response, "Invalid tool output params") {
		t.Fatalf("invalid messageID response=%s", response)
	}
	for _, params := range []map[string]any{
		{"threadID": threadID, "messageID": "invalid"},
		{"threadID": "../escape", "messageID": messageID},
		{"threadID": threadID, "messageID": messageID, "offset": -1},
		{"threadID": threadID, "messageID": messageID, "length": 0},
	} {
		req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "threads.toolOutput", "params": params})
		var res struct {
			Error *wire.RPCError `json:"error"`
		}
		if err := json.Unmarshal(d.Handle(ctx, &fakeConn{info: ClientInfo{Authenticated: true}}, req), &res); err != nil || res.Error == nil || res.Error.Code != wire.InvalidParams {
			t.Fatalf("invalid params=%v response=%+v err=%v", params, res, err)
		}
	}
	missingRequest, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "threads.toolOutput", "params": map[string]any{"threadID": threadID, "messageID": "00000000-0000-0000-0000-000000000203"}})
	var missing struct {
		Error *wire.RPCError `json:"error"`
	}
	if err := json.Unmarshal(d.Handle(ctx, &fakeConn{info: ClientInfo{Authenticated: true}}, missingRequest), &missing); err != nil || missing.Error == nil || missing.Error.Code != wire.ErrCodeResourceNotFound {
		t.Fatalf("missing response=%+v err=%v", missing, err)
	}
	large := strings.Repeat("a", toolOutputReadMaxBytes+10)
	if _, err := archiveToolOutput(dir, threadID, messageID, large); err != nil {
		t.Fatal(err)
	}
	for _, params := range []map[string]any{
		{"threadID": threadID, "messageID": messageID},
		{"threadID": threadID, "messageID": messageID, "length": toolOutputReadMaxBytes + 100},
	} {
		req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "threads.toolOutput", "params": params})
		if err := json.Unmarshal(d.Handle(ctx, &fakeConn{info: ClientInfo{Authenticated: true}}, req), &decoded); err != nil || len(decoded.Result.Text) != toolOutputReadMaxBytes || decoded.Result.EOF {
			t.Fatalf("read limit bytes=%d eof=%v err=%v", len(decoded.Result.Text), decoded.Result.EOF, err)
		}
	}

	if _, err := archiveToolOutput(dir, threadID, messageID, full); err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			_, err := backend.ThreadToolOutput(ctx, threadID, messageID, 0, 8)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				t.Errorf("read during deletion: %v", err)
			}
		}()
	}
	close(start)
	if err := backend.DeleteThread(ctx, threadID); err != nil {
		t.Fatal(err)
	}
	readers.Wait()
	if _, err := os.Stat(filepath.Join(dir, "tool-outputs", threadID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive directory remains: %v", err)
	}
}
