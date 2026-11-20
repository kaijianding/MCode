package mcodeui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type timeoutSettingsClient struct {
	*FakeClient
	mu                sync.Mutex
	customCtxs        []context.Context
	piMagpieCtxs      []context.Context
	refreshMagpieCtxs []context.Context
	blockUntilDone    bool
}

func (c *timeoutSettingsClient) CustomEndpoints(ctx context.Context) ([]CustomEndpoint, error) {
	c.mu.Lock()
	c.customCtxs = append(c.customCtxs, ctx)
	block := c.blockUntilDone
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.FakeClient.CustomEndpoints(ctx)
}

func (c *timeoutSettingsClient) PiMagpieEndpoints(ctx context.Context) ([]PiMagpieEndpoint, error) {
	c.mu.Lock()
	c.piMagpieCtxs = append(c.piMagpieCtxs, ctx)
	block := c.blockUntilDone
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.FakeClient.PiMagpieEndpoints(ctx)
}

func (c *timeoutSettingsClient) RefreshPiMagpieEndpoints(ctx context.Context) ([]PiMagpieEndpoint, error) {
	c.mu.Lock()
	c.refreshMagpieCtxs = append(c.refreshMagpieCtxs, ctx)
	block := c.blockUntilDone
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.FakeClient.RefreshPiMagpieEndpoints(ctx)
}

func waitUntilSettings(t *testing.T, msg string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestUIReview22_SettingsRPCContextsHaveDeadline 验证 CustomEndpoints、PiMagpieEndpoints 与
// RefreshPiMagpieEndpoints 调用均携带带 Deadline 的超时上下文。
func TestUIReview22_SettingsRPCContextsHaveDeadline(t *testing.T) {
	client := &timeoutSettingsClient{FakeClient: NewFakeClient()}
	st := NewSettingsState(settingsTestDir(t), nil, newChatState(client))

	loadCustomEndpoints(st)
	waitUntilSettings(t, "loadCustomEndpoints 超时未完成", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.endpoints.loading && st.endpoints.loaded
	})

	st.mu.Lock()
	openCustomEndpointForm(st, nil, endpointFormScopeCustom)
	st.endpoints.editName = "TestEP"
	st.endpoints.editBaseURL = "https://api.example.com/v1"
	st.mu.Unlock()
	saveCustomEndpoint(st)
	waitUntilSettings(t, "saveCustomEndpoint 超时未完成", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.endpoints.saving && len(st.endpoints.endpoints) == 1
	})

	st.mu.Lock()
	ep := st.endpoints.endpoints[0]
	st.mu.Unlock()
	toggleCustomEndpointEnabled(st, ep, !ep.Enabled)
	waitUntilSettings(t, "toggleCustomEndpointEnabled 超时未完成", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.endpoints.toggling
	})

	deleteCustomEndpoint(st, ep.ID)
	waitUntilSettings(t, "deleteCustomEndpoint 超时未完成", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.endpoints.deletingID == "" && len(st.endpoints.endpoints) == 0
	})

	st.loadPiMagpieEndpoints()
	waitUntilSettings(t, "loadPiMagpieEndpoints 超时未完成", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.piMagpie.loading && st.piMagpie.loaded
	})

	st.refreshPiMagpieEndpoints()
	waitUntilSettings(t, "refreshPiMagpieEndpoints 超时未完成", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.piMagpie.loading
	})

	client.mu.Lock()
	customCtxs := append([]context.Context(nil), client.customCtxs...)
	piCtxs := append([]context.Context(nil), client.piMagpieCtxs...)
	refreshCtxs := append([]context.Context(nil), client.refreshMagpieCtxs...)
	client.mu.Unlock()

	if len(customCtxs) != 4 {
		t.Fatalf("CustomEndpoints 调用次数 = %d，期望 4", len(customCtxs))
	}
	for i, ctx := range customCtxs {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatalf("CustomEndpoints 第 %d 次调用 ctx 缺少 Deadline", i+1)
		}
	}

	if len(piCtxs) != 1 {
		t.Fatalf("PiMagpieEndpoints 调用次数 = %d，期望 1", len(piCtxs))
	}
	if _, ok := piCtxs[0].Deadline(); !ok {
		t.Fatal("PiMagpieEndpoints ctx 缺少 Deadline")
	}

	if len(refreshCtxs) != 1 {
		t.Fatalf("RefreshPiMagpieEndpoints 调用次数 = %d，期望 1", len(refreshCtxs))
	}
	if deadline, ok := refreshCtxs[0].Deadline(); !ok {
		t.Fatal("RefreshPiMagpieEndpoints ctx 缺少 Deadline")
	} else if time.Until(deadline) <= settingsRPCTimeout {
		t.Fatalf("RefreshPiMagpieEndpoints 超时剩余时间过短: %v，期望大于 %v", time.Until(deadline), settingsRPCTimeout)
	}
}

// TestUIReview22_SettingsRPCTimeoutEndsLoadingAndRecordsError 验证后端挂起时超时会结束加载状态并记录错误。
func TestUIReview22_SettingsRPCTimeoutEndsLoadingAndRecordsError(t *testing.T) {
	origRPC := settingsRPCTimeout
	origRefresh := piMagpieRefreshRPCTimeout
	settingsRPCTimeout = 20 * time.Millisecond
	piMagpieRefreshRPCTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		settingsRPCTimeout = origRPC
		piMagpieRefreshRPCTimeout = origRefresh
	})

	client := &timeoutSettingsClient{
		FakeClient:     NewFakeClient(),
		blockUntilDone: true,
	}
	st := NewSettingsState(settingsTestDir(t), nil, newChatState(client))

	loadCustomEndpoints(st)
	waitUntilSettings(t, "CustomEndpoints 超时后加载未结束", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.endpoints.loading && st.endpoints.loaded
	})
	st.mu.Lock()
	epErr := st.endpoints.err
	st.mu.Unlock()
	if !strings.Contains(epErr, context.DeadlineExceeded.Error()) {
		t.Fatalf("CustomEndpoints 超时错误信息 = %q，期望包含 %q", epErr, context.DeadlineExceeded.Error())
	}

	st.loadPiMagpieEndpoints()
	waitUntilSettings(t, "PiMagpieEndpoints 超时后加载未结束", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.piMagpie.loading && st.piMagpie.err != ""
	})
	st.mu.Lock()
	piErr := st.piMagpie.err
	st.mu.Unlock()
	if !strings.Contains(piErr, context.DeadlineExceeded.Error()) {
		t.Fatalf("PiMagpieEndpoints 超时错误信息 = %q，期望包含 %q", piErr, context.DeadlineExceeded.Error())
	}

	st.mu.Lock()
	st.piMagpie.err = ""
	st.mu.Unlock()
	st.refreshPiMagpieEndpoints()
	waitUntilSettings(t, "RefreshPiMagpieEndpoints 超时后加载未结束", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return !st.piMagpie.loading && st.piMagpie.err != ""
	})
	st.mu.Lock()
	refreshErr := st.piMagpie.err
	st.mu.Unlock()
	if !strings.Contains(refreshErr, context.DeadlineExceeded.Error()) {
		t.Fatalf("RefreshPiMagpieEndpoints 超时错误信息 = %q，期望包含 %q", refreshErr, context.DeadlineExceeded.Error())
	}

	client.mu.Lock()
	customCtxs := append([]context.Context(nil), client.customCtxs...)
	piCtxs := append([]context.Context(nil), client.piMagpieCtxs...)
	refreshCtxs := append([]context.Context(nil), client.refreshMagpieCtxs...)
	client.mu.Unlock()
	if len(customCtxs) != 1 || len(piCtxs) != 1 || len(refreshCtxs) != 1 {
		t.Fatalf("意外的调用次数 custom=%d pi=%d refresh=%d", len(customCtxs), len(piCtxs), len(refreshCtxs))
	}
	if _, ok := customCtxs[0].Deadline(); !ok {
		t.Fatal("超时分支 CustomEndpoints ctx 缺少 Deadline")
	}
	if _, ok := piCtxs[0].Deadline(); !ok {
		t.Fatal("超时分支 PiMagpieEndpoints ctx 缺少 Deadline")
	}
	if _, ok := refreshCtxs[0].Deadline(); !ok {
		t.Fatal("超时分支 RefreshPiMagpieEndpoints ctx 缺少 Deadline")
	}
}
