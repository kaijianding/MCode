package mcodeui

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
)

type uiReview18UnreadClient struct {
	*FakeClient
}

func (*uiReview18UnreadClient) AutomationRunsState(context.Context, string, int, bool) (AutomationRunsState, error) {
	return AutomationRunsState{}, nil
}

func (*uiReview18UnreadClient) MarkAutomationRunRead(context.Context, string) error {
	return nil
}

func (*uiReview18UnreadClient) MarkAllAutomationRunsRead(context.Context, string) (int, error) {
	return 0, nil
}

func (*uiReview18UnreadClient) SetAutomationRunArchived(context.Context, string, bool) error {
	return nil
}

func (*uiReview18UnreadClient) AutomationUnreadCounts(context.Context, string) (map[string]int, error) {
	return map[string]int{}, nil
}

func TestUIReview18AutomationScopeSwitchRace(t *testing.T) {
	fake := NewFakeClient()
	fake.SetAutomations([]Automation{
		testAutomation("auto-1", "p1"),
		testAutomation("auto-2", "p2"),
	})
	client := &uiReview18UnreadClient{FakeClient: fake}
	s := &chatState{
		client:   client,
		projects: []Project{{ID: "p1", Name: "P1"}, {ID: "p2", Name: "P2"}},
	}
	as := getAutomationState(s)
	defer stopAutomationWatch(s)

	scope := "p1"
	tt := ui.NewTester(func(c *ui.Context) {
		s.applyWorkspaceUpdates()
		renderAutomationScopedBody(c, s, automationWorkspaceProps{ScopeID: scope})
	}, 800, 600)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			automationMu.Lock()
			if as.runningIDs == nil {
				as.runningIDs = make(map[string]bool)
			}
			as.runningIDs["auto-1"] = i%2 == 0
			_ = as.projectID
			_ = as.initialized
			automationMu.Unlock()
			if i%16 == 0 {
				fake.TriggerAutomationChange()
				runtime.Gosched()
			}
		}
	}()

	for i := 0; i < 60; i++ {
		if i%2 == 0 {
			scope = "p1"
		} else {
			scope = "p2"
		}
		tt.Frame()
	}

	close(done)
	wg.Wait()

	waitFor(t, tt, func() bool {
		automationMu.Lock()
		defer automationMu.Unlock()
		return !as.loading && as.unreadKnown && as.projectID == scope && as.initialized
	})
}
