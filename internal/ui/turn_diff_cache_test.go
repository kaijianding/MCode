package mcodeui

import (
	"context"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"mcode/internal/git"
)

// newToolDiffCacheTestState 在真实渲染服务下取出 chatState，并种好一轮已就绪的改动缓存。
func newToolDiffCacheTestState(t *testing.T) (*chatState, Thread) {
	t.Helper()
	fake := NewFakeClient()
	var state *chatState
	tt := ui.NewTester(Root(fake, func(s *chatState) { state = s }), 1000, 700)
	waitFor(t, tt, func() bool { return state != nil })
	t.Cleanup(state.close)

	thread := Thread{ID: "t1", ProjectID: "p1", Title: "会话"}
	state.mu.Lock()
	state.selectedThreadID = "t1"
	state.threads = []Thread{thread}
	state.threadsByProj["p1"] = []Thread{thread}
	if state.toolDiff.entries == nil {
		state.toolDiff.entries = make(map[string]*toolDiffEntry)
	}
	if state.toolDiff.turnKey == nil {
		state.toolDiff.turnKey = make(map[string]string)
	}
	state.toolDiff.entries["t1|turn-a"] = &toolDiffEntry{
		files: []git.FileDiff{{NewPath: "a.go", Status: "modified"}},
	}
	state.toolDiff.turnKey["t1|#1"] = "t1|turn-a"
	state.mu.Unlock()

	return state, thread
}

// TestOnWatchUpdateScopesToolDiffInvalidation 验证改动缓存只对本帧涉及的轮次失效：
// 用量增量不该清空缓存，别的轮次里的工具调用也不该让一张已就绪的卡片丢掉状态。
func TestOnWatchUpdateScopesToolDiffInvalidation(t *testing.T) {
	state, thread := newToolDiffCacheTestState(t)

	readyEntry := func() (*toolDiffEntry, bool) {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.toolDiff.entries["t1|turn-a"], state.toolDiff.entries["t1|turn-a"] != nil
	}

	// 用量增量：帧里不带消息。缓存原样保留，不标待重取。
	state.onWatchUpdate(ThreadUpdate{ThreadID: "t1", Thread: thread})
	entry, ok := readyEntry()
	if !ok || entry.stale || len(entry.files) != 1 {
		t.Fatalf("用量增量后改动缓存被动了: ok=%v entry=%+v", ok, entry)
	}

	// 新轮次里的工具调用：只影响新轮次，第一轮仍然可用。
	state.onWatchUpdate(ThreadUpdate{
		ThreadID: "t1",
		Thread:   thread,
		Messages: []Message{{ID: "m-t2", Role: "tool", TurnIndex: 2, TurnID: "turn-b"}},
	})
	entry, ok = readyEntry()
	if !ok || entry.stale || len(entry.files) != 1 {
		t.Fatalf("别的轮次有工具调用时第一轮缓存不应失效: ok=%v entry=%+v", ok, entry)
	}

	// 第一轮自己新增工具调用：标成待重取，但缓存还在，界面仍能画出状态。
	state.onWatchUpdate(ThreadUpdate{
		ThreadID: "t1",
		Thread:   thread,
		Messages: []Message{{ID: "m-t1", Role: "tool", TurnIndex: 1, TurnID: "turn-a"}},
	})
	entry, ok = readyEntry()
	if !ok || !entry.stale || len(entry.files) != 1 {
		t.Fatalf("本轮工具调用应把缓存标为待重取且保留内容: ok=%v entry=%+v", ok, entry)
	}
}

// TestToolDiffEntryServesStaleCache 验证待重取的缓存仍能立即给出上一次的结果，
// 卡片因此不会在重取期间闪一下没有状态。
func TestToolDiffEntryServesStaleCache(t *testing.T) {
	state, _ := newToolDiffCacheTestState(t)

	state.mu.Lock()
	state.toolDiff.entries["t1|turn-a"].stale = true
	state.mu.Unlock()

	entry := state.toolDiffEntryFor("t1", Message{TurnIndex: 1, TurnID: "turn-a"})
	if entry.loading || len(entry.files) != 1 {
		t.Fatalf("待重取的缓存应继续返回上一次的结果: %+v", entry)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if e := state.toolDiff.entries["t1|turn-a"]; e != nil && e.stale {
		t.Fatal("取出后应清掉待重取标记，避免每帧重复发起请求")
	}
}

type blockingToolDiffClient struct {
	*FakeClient
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingToolDiffClient) Diff(ctx context.Context, threadID, turnID string) (ThreadDiff, error) {
	c.once.Do(func() {
		if c.started != nil {
			close(c.started)
		}
	})
	if c.release != nil {
		<-c.release
	}
	return c.FakeClient.Diff(ctx, threadID, turnID)
}

// TestToolDiffKeepsStaleDuringInflightLoad 验证加载在途期间若轮次又被标成 stale，
// loadToolDiff 写回新条目时必须保留 stale=true，避免用旧 diff 覆盖待重取标记。
func TestToolDiffKeepsStaleDuringInflightLoad(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  Message
	}{
		{"by_turn_id", Message{ID: "m1", Role: "tool", TurnIndex: 1, TurnID: "turn-a"}},
		{"by_turn_index", Message{ID: "m1", Role: "tool", TurnIndex: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := NewFakeClient()
			patch := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"
			fake.SetThreadDiff("t1", "", ThreadDiff{
				Turns: []DiffTurn{{Index: 1, TurnID: "turn-a"}},
			})
			fake.SetThreadDiff("t1", "turn-a", ThreadDiff{
				Diff: patch,
			})
			blocking := &blockingToolDiffClient{
				FakeClient: fake,
				started:    make(chan struct{}),
				release:    make(chan struct{}),
			}
			var state *chatState
			tt := ui.NewTester(Root(blocking, func(s *chatState) { state = s }), 1000, 700)
			waitFor(t, tt, func() bool { return state != nil })
			t.Cleanup(state.close)

			entry := state.toolDiffEntryFor("t1", tc.msg)
			if !entry.loading {
				t.Fatalf("首次读取应处于加载中: %+v", entry)
			}
			<-blocking.started

			state.mu.Lock()
			state.invalidateToolDiffTurnsLocked("t1", []Message{tc.msg})
			state.mu.Unlock()

			close(blocking.release)
			waitFor(t, tt, func() bool {
				state.mu.Lock()
				defer state.mu.Unlock()
				e := state.toolDiff.entries["t1|turn-a"]
				return e != nil && !e.loading
			})

			state.mu.Lock()
			e := state.toolDiff.entries["t1|turn-a"]
			state.mu.Unlock()
			if e == nil || !e.stale {
				t.Fatalf("在途期间被标为 stale 后，写回条目应保留 stale=true，实际=%+v", e)
			}
		})
	}
}
