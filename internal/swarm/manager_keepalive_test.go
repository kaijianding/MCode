package swarm

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"mcode/internal/git"
	"mcode/internal/kanban"
)

func TestCacheKeepalive(t *testing.T) {
	t.Parallel()
	newManager := func(t *testing.T) (*Manager, *testClock, *fakeDriver, *[]string) {
		t.Helper()
		clock := newTestClock()
		driver := newFakeDriver()
		var messages []string
		mgr := NewManager(Options{
			Git:                git.NewRunner("", 1),
			Driver:             driver,
			Now:                clock.Now,
			NoEditStallTimeout: 2 * time.Hour,
			StallTimeout:       2 * time.Hour,
			ThinkingAfter:      2 * time.Hour,
		})
		t.Cleanup(mgr.Stop)
		mgr.leads["lead"] = &leadState{
			leadThreadID:     "lead",
			requestStartedAt: clock.Now(),
			delegationRounds: 3,
			bees: []Bee{{
				ThreadID:  "bee",
				Status:    StatusRunning,
				StartedAt: clock.Now(),
			}},
		}
		driver.setSessionState("lead", kanban.SessionState{})
		driver.setSessionState("bee", kanban.SessionState{Running: true})
		mgr.NoteLeadTurn("lead")
		mgr.opts.OnReport = func(threadID, message string) error {
			if threadID != "lead" {
				t.Errorf("保活发给了 %q", threadID)
			}
			mgr.mu.Lock()
			reservedAt := mgr.leads[threadID].lastLeadTurnAt
			mgr.mu.Unlock()
			if !reservedAt.Equal(clock.Now()) {
				t.Error("投递前必须更新时间以防重复保活")
			}
			// 与真实开轮一样回调 Manager，验证投递不持 m.mu。
			mgr.NoteLeadTurn(threadID)
			messages = append(messages, message)
			return nil
		}
		return mgr, clock, driver, &messages
	}

	t.Run("50分钟边界与并发Tick去重", func(t *testing.T) {
		t.Parallel()
		mgr, clock, _, messages := newManager(t)
		startedAt := clock.Now()
		before := mgr.RequestLimits("lead", 5)
		clock.Advance(49 * time.Minute)
		mgr.Tick(context.Background())
		if len(*messages) != 0 {
			t.Fatalf("49 分钟不应保活: %v", *messages)
		}
		clock.Advance(time.Minute)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				mgr.Tick(context.Background())
			}()
		}
		wg.Wait()
		mgr.Tick(context.Background())
		if len(*messages) != 1 || !strings.HasPrefix((*messages)[0], NoticePrefix) || !strings.Contains((*messages)[0], "缓存保活") {
			t.Fatalf("50 分钟应恰好保活一次: %v", *messages)
		}
		if IsReport((*messages)[0]) || !IsNotice((*messages)[0]) {
			t.Fatal("保活必须是通知，不能被当成批量汇报")
		}
		after := mgr.RequestLimits("lead", 5)
		if after.RoundsUsed != before.RoundsUsed || after.BeesStarted != before.BeesStarted || after.Elapsed != 50*time.Minute || !mgr.leads["lead"].requestStartedAt.Equal(startedAt) {
			t.Fatalf("保活改变了请求预算: before=%+v after=%+v", before, after)
		}
		if !mgr.leads["lead"].lastLeadTurnAt.Equal(clock.Now()) {
			t.Fatal("投递后未更新时间")
		}
		clock.Advance(50 * time.Minute)
		mgr.Tick(context.Background())
		if len(*messages) != 2 {
			t.Fatalf("仍有 bee 运行时下一次 50 分钟应继续保活: %v", *messages)
		}
	})

	t.Run("回合结束重新计时", func(t *testing.T) {
		t.Parallel()
		mgr, clock, _, messages := newManager(t)
		clock.Advance(49 * time.Minute)
		mgr.NoteLeadTurn("lead")
		clock.Advance(time.Minute)
		mgr.Tick(context.Background())
		if len(*messages) != 0 {
			t.Fatal("应从最近一次回合结束计时")
		}
		clock.Advance(49 * time.Minute)
		mgr.Tick(context.Background())
		if len(*messages) != 1 {
			t.Fatalf("最近一次回合结束 50 分钟后应保活: %v", *messages)
		}
	})

	for _, tc := range []struct {
		name  string
		setup func(*leadState, *fakeDriver)
	}{
		{"没有bee", func(lead *leadState, _ *fakeDriver) { lead.bees = nil }},
		{"没有运行中的bee", func(lead *leadState, _ *fakeDriver) {
			lead.bees[0].Status = StatusCompleted
			lead.bees[0].Reported = true
		}},
		{"正在派发", func(lead *leadState, _ *fakeDriver) { lead.dispatching = true }},
		{"没有回合记录", func(lead *leadState, _ *fakeDriver) { lead.lastLeadTurnAt = time.Time{} }},
		{"用户已停止", func(lead *leadState, _ *fakeDriver) { lead.stoppedByUser = true }},
		{"主会话运行中", func(_ *leadState, driver *fakeDriver) {
			driver.setSessionState("lead", kanban.SessionState{Running: true})
		}},
		{"主会话已中断", func(_ *leadState, driver *fakeDriver) {
			driver.setSessionState("lead", kanban.SessionState{Interrupted: true})
		}},
		{"主会话等待审批", func(_ *leadState, driver *fakeDriver) {
			driver.setSessionState("lead", kanban.SessionState{AwaitingApproval: true})
		}},
		{"主会话等待输入", func(_ *leadState, driver *fakeDriver) {
			driver.setSessionState("lead", kanban.SessionState{AwaitingInput: true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr, clock, driver, messages := newManager(t)
			tc.setup(mgr.leads["lead"], driver)
			clock.Advance(time.Hour)
			mgr.Tick(context.Background())
			if len(*messages) != 0 {
				t.Fatalf("不应保活: %v", *messages)
			}
		})
	}
}
