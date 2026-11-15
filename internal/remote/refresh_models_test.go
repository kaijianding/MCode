package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"mcode/internal/harness"
	"mcode/internal/providers/acp"
	"mcode/internal/providers/omp"
	"mcode/internal/remote/wire"
)

type refreshTestHarness struct {
	customMetaHarness
	available bool
	err       error
	calls     atomic.Int32
}

type panicRefreshHarness struct {
	customMetaHarness
	calls atomic.Int32
}

func (h *panicRefreshHarness) RefreshModels(context.Context) error {
	if h.calls.Add(1) == 1 {
		panic("refresh")
	}
	return nil
}

func TestRefreshProviderModelsRecoversPanic(t *testing.T) {
	hub := NewStreamHub(StreamHubOptions{})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	h := &panicRefreshHarness{customMetaHarness: customMetaHarness{kind: "custom-panic"}}
	backend.RegisterHarness(h)
	if err := backend.RefreshProviderModels(context.Background()); err == nil {
		t.Fatal("应报告刷新失败")
	}
	if err := backend.RefreshProviderModels(context.Background()); err != nil || h.calls.Load() != 2 {
		t.Fatalf("再次刷新: %v", err)
	}
}

func TestRefreshAPICatalogAndMcodeAlias(t *testing.T) {
	const kind = "custom-magpie-refresh"
	h := &refreshTestHarness{customMetaHarness: customMetaHarness{kind: kind}, available: true}
	alias := &refreshTestHarness{customMetaHarness: customMetaHarness{kind: "mcode"}, available: true}
	SetAPICatalog(kind, func() []wire.ProviderModel {
		var rows []wire.ProviderModel
		for _, id := range h.modelIDs {
			rows = append(rows, wire.ProviderModel{ID: id, Name: id})
		}
		return rows
	})
	t.Cleanup(func() { SetAPICatalog(kind, nil) })
	hub := NewStreamHub(StreamHubOptions{})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	backend.RegisterHarness(h)
	backend.RegisterHarness(alias)
	if len(ProviderModels(kind)) != 0 {
		t.Fatal("初始目录应为空")
	}
	if err := backend.RefreshProviderModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.calls.Load() != 1 || alias.calls.Load() != 0 {
		t.Fatal("刷新次数错误")
	}
	if rows := ProviderModels(kind); len(rows) != 1 || rows[0].ID != "new-model" {
		t.Fatalf("刷新后目录错误: %+v", rows)
	}
}

func TestRefreshUsesConfiguredCLIBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("测试用 CLI 为 shell 脚本")
	}
	binary := filepath.Join(t.TempDir(), "custom-omp")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' '{\"models\":[{\"id\":\"new\",\"provider\":\"test\"}]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := omp.New(omp.Options{Binary: binary})
	models, err := fetchOmpModelsWithBinary(context.Background(), h.CatalogBinary())
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if model.ID == "test/new" {
			return
		}
	}
	t.Fatalf("未从配置的 CLI 读取目录: %+v", models)
}

func (h *refreshTestHarness) Available() (bool, string) { return h.available, "unavailable" }
func (h *refreshTestHarness) RefreshModels(context.Context) error {
	h.calls.Add(1)
	if h.err == nil {
		h.modelIDs = []string{"new-model"}
	}
	return h.err
}

func TestRefreshProviderModelsPartialFailure(t *testing.T) {
	hub := NewStreamHub(StreamHubOptions{})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	good := &refreshTestHarness{customMetaHarness: customMetaHarness{kind: "custom-good"}, available: true}
	bad := &refreshTestHarness{customMetaHarness: customMetaHarness{kind: "custom-bad", modelIDs: []string{"old-model"}}, available: true, err: errors.New("offline")}
	unavailable := &refreshTestHarness{customMetaHarness: customMetaHarness{kind: "custom-unavailable"}}
	backend.RegisterHarness(good)
	backend.RegisterHarness(bad)
	backend.RegisterHarness(unavailable)
	err := backend.RefreshProviderModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "custom-bad") {
		t.Fatalf("应提示失败的供应商: %v", err)
	}
	if good.calls.Load() != 1 || bad.calls.Load() != 1 || unavailable.calls.Load() != 0 {
		t.Fatalf("可用供应商应各刷新一次: good=%d bad=%d unavailable=%d", good.calls.Load(), bad.calls.Load(), unavailable.calls.Load())
	}
	result, err := backend.ListProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range result.Providers {
		switch p.ID {
		case "custom-good":
			if len(p.Models) != 1 || p.Models[0].ID != "new-model" {
				t.Fatalf("成功的供应商未更新: %+v", p.Models)
			}
		case "custom-bad":
			if len(p.Models) != 1 || p.Models[0].ID != "old-model" {
				t.Fatalf("失败的供应商应保留旧目录: %+v", p.Models)
			}
		}
	}
}

func TestListProvidersOpenAIIncludesEndpointModels(t *testing.T) {
	hub := NewStreamHub(StreamHubOptions{})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	backend.RegisterHarness(&customMetaHarness{kind: "openai", modelIDs: []string{"configured", "server-model"}})
	result, err := backend.ListProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range result.Providers {
		if p.ID == "openai" {
			for _, m := range p.Models {
				if m.ID == "server-model" {
					return
				}
			}
		}
	}
	t.Fatal("OpenAI 端点返回的模型没有出现在目录里")
}

type refreshACPTestHarness struct{ customMetaHarness }

func (h *refreshACPTestHarness) ProbeModels(context.Context) ([]acp.ProbedModel, error) {
	return []acp.ProbedModel{{ID: "agent-model", Name: "Agent Model", Current: true}}, nil
}

func TestRefreshProviderModelsProbesACPCatalog(t *testing.T) {
	catalog := acpCatalog("cursor")
	catalog.mu.Lock()
	previousModels, previousFetched, previousFailed, previousGen, previousFetchGen, previousFetching := catalog.models, catalog.fetchedAt, catalog.failedAt, catalog.gen, catalog.fetchGen, catalog.fetching
	catalog.mu.Unlock()
	t.Cleanup(func() {
		catalog.mu.Lock()
		catalog.models, catalog.fetchedAt, catalog.failedAt, catalog.gen, catalog.fetchGen, catalog.fetching = previousModels, previousFetched, previousFailed, previousGen, previousFetchGen, previousFetching
		catalog.mu.Unlock()
	})
	hub := NewStreamHub(StreamHubOptions{})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	backend.RegisterHarness(&refreshACPTestHarness{customMetaHarness: customMetaHarness{kind: "cursor"}})
	if err := backend.RefreshProviderModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := backend.ListProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range result.Providers {
		if p.ID == "cursor" && len(p.Models) == 1 && p.Models[0].ID == "agent-model" && p.Models[0].IsDefault {
			return
		}
	}
	t.Fatal("ACP 探测到的模型没有出现在供应商目录里")
}

// customACPTestHarness 是同时带 ACP 探测与可选 CLI 路径的 fake harness：
// 用来验证任意 id 的 ACP provider 都能拿到自己的目录，且专门目录的 provider 不被抢走。
type customACPTestHarness struct {
	customMetaHarness
	models []acp.ProbedModel
	binary string
	calls  atomic.Int32
}

func (h *customACPTestHarness) ProbeModels(context.Context) ([]acp.ProbedModel, error) {
	h.calls.Add(1)
	return h.models, nil
}

func (h *customACPTestHarness) CatalogBinary() string { return h.binary }

func TestRefreshProviderModelsProbesCustomACPCatalog(t *testing.T) {
	const kind = "maki"
	t.Cleanup(func() { acpCatalogs.Delete(kind) })
	h := &customACPTestHarness{
		customMetaHarness: customMetaHarness{kind: kind},
		models: []acp.ProbedModel{
			{ID: "maki-fast", Name: "Maki Fast"},
			{ID: "maki-pro", Name: "Maki Pro", Current: true, Efforts: []string{"low", "medium", "high"}, DefaultEffort: "high", ContextTokens: 1000000},
		},
	}
	hub := NewStreamHub(StreamHubOptions{})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	backend.RegisterHarness(h)
	if rows := ProviderModels(kind); len(rows) != 1 || rows[0].ID != placeholderModelID {
		t.Fatalf("探测前的目录应为占位行: %+v", rows)
	}
	if err := backend.RefreshProviderModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("探测次数 = %d，想要 1", h.calls.Load())
	}
	snapshot := acpCatalogSnapshot(kind)
	if len(snapshot) != 2 || snapshot[0].ID != "maki-fast" || snapshot[1].ID != "maki-pro" || !snapshot[1].IsDefault {
		t.Fatalf("自定义 ACP 目录错误: %+v", snapshot)
	}
	if len(snapshot[0].Efforts) != 0 || snapshot[0].DefaultEffort != nil {
		t.Fatalf("没有档位的模型不该带档位: %+v", snapshot[0])
	}
	if len(snapshot[1].Efforts) != 3 || snapshot[1].Efforts[2] != "high" || snapshot[1].DefaultEffort == nil || *snapshot[1].DefaultEffort != "high" {
		t.Fatalf("探测行档位丢失: %+v", snapshot[1])
	}
	if snapshot[0].ContextLimit != 0 || snapshot[1].ContextLimit != 1000000 {
		t.Fatalf("探测行上下文窗口丢失: %+v", snapshot)
	}
	rows := ProviderModels(kind)
	if len(rows) != 2 || rows[0].ID != "maki-fast" || rows[1].ID != "maki-pro" {
		t.Fatalf("ProviderModels 未读到自定义 ACP 目录: %+v", rows)
	}
	result, err := backend.ListProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range result.Providers {
		if p.ID != kind {
			continue
		}
		if len(p.Models) != 2 || p.Models[0].ID != "maki-fast" || p.Models[1].ID != "maki-pro" || !p.Models[1].IsDefault {
			t.Fatalf("供应商目录错误: %+v", p.Models)
		}
		return
	}
	t.Fatalf("供应商列表里没有 %s", kind)
}

func TestRefreshProviderModelsKeepsDedicatedCatalogs(t *testing.T) {
	// cline/droid 的目录各有来源，即使 harness 实现了 ProbeModels 也不能改走 ACP 目录。
	for _, kind := range []string{"cline", "droid"} {
		t.Cleanup(func() { acpCatalogs.Delete(kind) })
	}
	missing := filepath.Join(t.TempDir(), "missing-cli")
	hub := NewStreamHub(StreamHubOptions{})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	harnesses := map[string]*customACPTestHarness{}
	for _, kind := range []string{"cline", "droid"} {
		h := &customACPTestHarness{
			customMetaHarness: customMetaHarness{kind: harness.Kind(kind)},
			models:            []acp.ProbedModel{{ID: "agent-model", Name: "Agent Model", Current: true}},
			binary:            missing,
		}
		harnesses[kind] = h
		backend.RegisterHarness(h)
	}
	_ = backend.RefreshProviderModels(context.Background())
	for kind, h := range harnesses {
		if h.calls.Load() != 0 {
			t.Fatalf("%s 不应走 ACP 探测，探测次数 = %d", kind, h.calls.Load())
		}
		if rows := acpCatalogSnapshot(kind); rows != nil {
			t.Fatalf("%s 不应写入 ACP 目录: %+v", kind, rows)
		}
	}
	for _, m := range ProviderModels("cline") {
		if m.ID == "agent-model" {
			t.Fatal("cline 目录被 ACP 探测结果顶掉了")
		}
	}
}

func TestRefreshProviderModelsFallsBackToRefreshModels(t *testing.T) {
	const kind = "plain-no-probe"
	t.Cleanup(func() { acpCatalogs.Delete(kind) })
	h := &refreshTestHarness{customMetaHarness: customMetaHarness{kind: kind}, available: true}
	hub := NewStreamHub(StreamHubOptions{})
	defer hub.Close()
	backend := NewMemoryBackend(hub)
	defer backend.Close()
	backend.RegisterHarness(h)
	if err := backend.RefreshProviderModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("RefreshModels 调用次数 = %d，想要 1", h.calls.Load())
	}
	if rows := acpCatalogSnapshot(kind); rows != nil {
		t.Fatalf("无探测能力的 provider 不应创建 ACP 目录: %+v", rows)
	}
}
