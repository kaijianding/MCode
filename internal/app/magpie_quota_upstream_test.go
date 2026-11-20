package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"mcode/internal/config"
	"mcode/internal/providers/claude"
	"mcode/internal/providers/codex"
	"mcode/internal/providers/magpie"
	"mcode/internal/remote"
	"mcode/internal/remote/wire"
	mcodeui "mcode/internal/ui"
)

const upstreamQuotaJSON = `{"data":[
	{"provider":"claude","user":"a","windows":[{"name":"5 hours","used":10}]},
	{"provider":"codex","user":"b","windows":[{"name":"5 hours","used":20}]},
	{"provider":"codex","user":"c","windows":[{"name":"5 hours","used":30}]}]}`

func upstreamQuotaGateway(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var modelsHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			io.WriteString(w, `{"name":"magpie"}`)
		case "/v1/magpie/quotas", "/v1/magpie/quotas/cards":
			io.WriteString(w, upstreamQuotaJSON)
		case "/v1/models":
			modelsHits.Add(1)
			io.WriteString(w, `{"data":[{"id":"auto/model","owned_by":" CODEX "},{"id":"unowned"},{"id":"auto/claude-model","owned_by":"claude"},{"id":"auto/balance-model","owned_by":"sub2api"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &modelsHits
}

func TestReadMagpieQuotaWindowsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, route, model string
		want               int
		modelsHits         int32
	}{
		{"路由优先于目录", "claude", "auto/model", 1, 0},
		{"无路由按目录", "", "auto/model", 2, 1},
		{"路由缺供应商按目录", " ", "auto/model", 2, 1},
		{"路由无匹配返回空", "missing", "auto/model", 0, 0},
		{"模型上游无匹配返回空", "", "auto/balance-model", 0, 1},
		{"模型无供应商保留全部", "", "unowned", 3, 1},
		{"模型不在目录保留全部", "", "missing", 3, 1},
		{"都未知保留全部", "", "", 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, hits := upstreamQuotaGateway(t)
			client := NewClient(nil)
			tracker := newTestSessionAccountTracker(t)
			useQuotaTracker(client, tracker)
			if tc.route != "" {
				tracker.ingest(magpie.TraceSnapshot{Routes: []magpie.TraceRoute{
					sessionTraceRoute(1, "session", tc.route, "a", "account"),
				}})
			}
			windows, err := client.readMagpieQuotaWindows(context.Background(), srv.URL+"/v1", "key", "", "session", tc.model)
			if err != nil || len(windows) != tc.want {
				t.Fatalf("窗口 = %+v，err=%v，想要 %d 个", windows, err, tc.want)
			}
			if hits.Load() != tc.modelsHits {
				t.Errorf("目录请求数 = %d，想要 %d", hits.Load(), tc.modelsHits)
			}
			for _, w := range windows {
				if tc.want == 2 && (w.Provider != "codex" || w.Active) {
					t.Errorf("目录推断不应标 Active: %+v", w)
				}
				if tc.route == "claude" && !w.Active {
					t.Errorf("真实路由应保留 Active: %+v", w)
				}
			}
		})
	}
}

func TestProviderQuotaMagpieModelFallback(t *testing.T) {
	for _, tc := range []struct {
		name, agent, provider, model, defaultEndpoint string
		want                                          int
		explicitModel                                 string
		withoutThread                                 bool
	}{
		{"pi 当前模型", "pi", "pi:magpie-jp", "magpie-jp/auto/model", "", 2, "", false},
		{"引擎当前模型", "mcode", "mcode:magpie-jp", "magpie-jp/auto/model", "", 2, "", false},
		{"端点当前模型", "mcode", "custom-jp", "auto/model", "", 2, "", false},
		{"codex 注入模型", "codex", "codex", "magpie-jp/auto/model", "", 2, "", false},
		{"pi 默认模型", "pi", "pi:magpie-jp", "", "jp", 2, "", false},
		{"引擎默认模型", "mcode", "mcode:magpie-jp", "", "jp", 2, "", false},
		{"codex 默认模型", "codex", "codex", "", "jp", 2, "", false},
		{"pi 其他端点默认模型", "pi", "pi:magpie-jp", "", "other", 3, "", false},
		{"引擎其他端点默认模型", "mcode", "mcode:magpie-jp", "", "other", 3, "", false},
		{"codex 其他端点默认模型", "codex", "codex", "", "other", 3, "", false},
		{"codex 仅显式模型", "codex", "codex", "auto/claude-model", "", 2, "auto/model", true},
		{"codex 显式模型优先", "codex", "codex", "auto/claude-model", "", 2, "auto/model", false},
		{"codex 显式模型去端点前缀", "codex", "codex", "auto/claude-model", "", 2, "magpie-jp/auto/model", false},
		{"pi 显式模型优先", "pi", "pi:magpie-jp", "magpie-jp/auto/claude-model", "", 2, "magpie-jp/auto/model", false},
		{"引擎仅显式模型", "mcode", "mcode:magpie-jp", "magpie-jp/auto/claude-model", "", 2, "magpie-jp/auto/model", true},
		{"引擎显式模型优先", "mcode", "mcode:magpie-jp", "magpie-jp/auto/claude-model", "", 2, "magpie-jp/auto/model", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := upstreamQuotaGateway(t)
			backend := remote.NewMemoryBackend(nil)
			defer backend.Close()
			threadProvider := tc.agent
			if tc.agent == "codex" && tc.model == "auto/claude-model" {
				threadProvider = "claude"
			}
			backend.AddThread(wire.ThreadSummary{ID: "thread", Provider: threadProvider, Model: &tc.model}, nil)
			client := NewClient(backend)
			client.SetAgySessionLookup(func(context.Context, string) string {
				if tc.withoutThread {
					t.Error("仅显式模型的请求不应解析会话路由")
				}
				return ""
			})
			dataDir := t.TempDir()
			cfg := magpieTestConfig(config.CustomEndpointConfig{ID: "jp", Name: "Magpie JP", BaseURL: srv.URL + "/v1", Magpie: true, Enabled: true})
			cfg.Providers.MagpieAgents = map[string]config.MagpieAgentConfig{
				tc.agent: {DefaultEndpointID: tc.defaultEndpoint, DefaultModel: "auto/model"},
			}
			if err := config.Save(dataDir, cfg); err != nil {
				t.Fatal(err)
			}
			client.SetProviderStore(dataDir, magpieTestSecrets(t, map[string]string{"jp": "key"}))
			useQuotaTracker(client, newTestSessionAccountTracker(t))
			if tc.agent == "codex" {
				previousRoutes, previousReader := codexMagpieRoutesForQuota, readCodexQuotaLimits
				codexMagpieRoutesForQuota = func() []codex.MagpieRoute {
					return []codex.MagpieRoute{{ProviderID: "magpie-jp", BaseURL: srv.URL + "/v1", APIKey: "key"}}
				}
				readCodexQuotaLimits = func(*Client, context.Context, string) (codex.RateLimits, error) {
					return codex.RateLimits{Source: codex.QuotaSourceMagpie, GatewayBaseURL: srv.URL + "/v1", GatewayToken: "key"}, nil
				}
				t.Cleanup(func() { codexMagpieRoutesForQuota, readCodexQuotaLimits = previousRoutes, previousReader })
			}
			ctx := mcodeui.WithQuotaModel(context.Background(), tc.explicitModel)
			if !tc.withoutThread {
				ctx = mcodeui.WithQuotaThread(ctx, "thread")
			}
			windows, err := client.ProviderQuota(ctx, tc.provider)
			if err != nil || len(windows) != tc.want {
				t.Fatalf("窗口 = %+v，err=%v，想要 %d 个", windows, err, tc.want)
			}
			for _, w := range windows {
				if tc.want == 2 && (w.Provider != "codex" || w.Active) {
					t.Errorf("模型筛选应仅返回 codex 且不标在用: %+v", w)
				}
			}
		})
	}
}

func TestProviderQuotaClaudeOtherUpstream(t *testing.T) {
	claude.ResetPlanLimitsCache()
	t.Cleanup(claude.ResetPlanLimitsCache)
	srv, _ := upstreamQuotaGateway(t)
	setClaudeGatewayEnv(t, srv.URL, "key")
	client := NewClient(nil)
	client.SetAgySessionLookup(func(context.Context, string) string { return "session" })
	tracker := newTestSessionAccountTracker(t)
	useQuotaTracker(client, tracker)
	tracker.ingest(magpie.TraceSnapshot{Routes: []magpie.TraceRoute{
		sessionTraceRoute(1, "session", "codex", "b", "account"),
	}})
	windows, err := client.ProviderQuota(mcodeui.WithQuotaThread(context.Background(), "thread"), "claude")
	if err != nil || len(windows) != 2 {
		t.Fatalf("窗口 = %+v，err=%v", windows, err)
	}
	for _, w := range windows {
		if w.Provider != "codex" || w.Active != (w.User == "b") {
			t.Errorf("Claude 应按实际路由筛选并标 Active: %+v", w)
		}
	}
}

func TestProviderQuotaCodexMagpieAssignedModels(t *testing.T) {
	multiQuotaJSON := `{"data":[
		{"provider":"claude","user":"claude-a","windows":[{"name":"5 hours","used":10}]},
		{"provider":"codex","user":"codex-a","windows":[{"name":"5 hours","used":20}]},
		{"provider":"codex","user":"codex-b","windows":[{"name":"5 hours","used":30}]},
		{"provider":"workbuddy","user":"wb-a","windows":[{"name":"5 hours","used":40}]},
		{"provider":"commandcode-plan","user":"cc-a","windows":[{"name":"5 hours","used":50}]}]}`

	newGateway := func(t *testing.T) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/magpie/quotas":
				io.WriteString(w, multiQuotaJSON)
			case "/v1/models":
				io.WriteString(w, `{"data":[
					{"id":"auto/codex","owned_by":" CODEX "},
					{"id":"auto/claude","owned_by":"Claude"},
					{"id":"auto/unowned","owned_by":"  "},
					{"id":"auto/sub2api","owned_by":"sub2api"}]}`)
			default:
				t.Errorf("意外请求路径: %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	assertProviders := func(t *testing.T, windows []mcodeui.ProviderQuotaWindow, wantCounts map[string]int) {
		t.Helper()
		gotCounts := map[string]int{}
		for _, w := range windows {
			gotCounts[w.Provider]++
		}
		totalWant := 0
		for p, n := range wantCounts {
			totalWant += n
			if gotCounts[p] != n {
				t.Errorf("供应商 %q 卡片数 = %d，想要 %d（全部=%+v）", p, gotCounts[p], n, windows)
			}
		}
		if len(windows) != totalWant {
			t.Errorf("总卡片数 = %d，想要 %d（实际=%+v）", len(windows), totalWant, gotCounts)
		}
	}

	type endpointSpec struct {
		id     string
		token  string
		models []string
	}

	for _, tc := range []struct {
		name         string
		endpoints    []endpointSpec
		gatewayToken string
		defaultEp    string
		defaultModel string
		threadID     string
		wantCounts   map[string]int
	}{
		{
			name:         "分属codex与claude的多个模型",
			endpoints:    []endpointSpec{{id: "jp", token: "key", models: []string{"auto/codex", "auto/claude"}}},
			gatewayToken: "key",
			wantCounts:   map[string]int{"codex": 2, "claude": 1},
		},
		{
			name:         "仅分配claude模型",
			endpoints:    []endpointSpec{{id: "jp", token: "key", models: []string{"auto/claude"}}},
			gatewayToken: "key",
			wantCounts:   map[string]int{"claude": 1},
		},
		{
			name:         "codex模型加无owned_by模型",
			endpoints:    []endpointSpec{{id: "jp", token: "key", models: []string{"auto/codex", "auto/unowned"}}},
			gatewayToken: "key",
			wantCounts:   map[string]int{"codex": 2},
		},
		{
			name:         "仅无owned_by或不存在模型回退codex",
			endpoints:    []endpointSpec{{id: "jp", token: "key", models: []string{"auto/unowned", "auto/missing"}}},
			gatewayToken: "key",
			wantCounts:   map[string]int{"codex": 2},
		},
		{
			name:         "未分配任何模型回退codex",
			endpoints:    []endpointSpec{{id: "jp", token: "key", models: nil}},
			gatewayToken: "key",
			wantCounts:   map[string]int{"codex": 2},
		},
		{
			name:         "已知上游但无对应额度卡返回空",
			endpoints:    []endpointSpec{{id: "jp", token: "key", models: []string{"auto/sub2api"}}},
			gatewayToken: "key",
			wantCounts:   map[string]int{},
		},
		{
			name: "同源多端点按token唯一匹配",
			endpoints: []endpointSpec{
				{id: "jp-1", token: "key-1", models: []string{"auto/codex"}},
				{id: "jp-2", token: "key-2", models: []string{"auto/claude"}},
			},
			gatewayToken: "key-2",
			wantCounts:   map[string]int{"claude": 1},
		},
		{
			name:         "带threadID且GatewayBaseURL无v1时DefaultModel兜底",
			endpoints:    []endpointSpec{{id: "jp", token: "key", models: []string{"auto/claude"}}},
			gatewayToken: "key",
			defaultEp:    "jp",
			defaultModel: "auto/claude",
			threadID:     "thread-1",
			wantCounts:   map[string]int{"claude": 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newGateway(t)
			backend := remote.NewMemoryBackend(nil)
			defer backend.Close()
			if tc.threadID != "" {
				backend.AddThread(wire.ThreadSummary{ID: tc.threadID, Provider: config.ProviderCodex}, nil)
			}
			client := NewClient(backend)
			client.SetAgySessionLookup(func(context.Context, string) string { return "" })

			customEndpoints := make([]config.CustomEndpointConfig, 0, len(tc.endpoints))
			selections := make([]config.MagpieSelection, 0, len(tc.endpoints))
			keys := make(map[string]string, len(tc.endpoints))
			for _, ep := range tc.endpoints {
				customEndpoints = append(customEndpoints, config.CustomEndpointConfig{
					ID:      ep.id,
					Name:    "Magpie " + ep.id,
					BaseURL: srv.URL + "/v1",
					Magpie:  true,
					Enabled: true,
				})
				keys[ep.id] = ep.token
				if len(ep.models) > 0 {
					selections = append(selections, config.MagpieSelection{EndpointID: ep.id, Models: ep.models})
				}
			}

			dataDir := t.TempDir()
			cfg := magpieTestConfig(customEndpoints...)
			cfg.Providers.MagpieAgents = map[string]config.MagpieAgentConfig{
				config.ProviderCodex: {
					Mode:              config.MagpieModeGlobal,
					Selections:        selections,
					DefaultEndpointID: tc.defaultEp,
					DefaultModel:      tc.defaultModel,
				},
			}
			if err := config.Save(dataDir, cfg); err != nil {
				t.Fatal(err)
			}
			client.SetProviderStore(dataDir, magpieTestSecrets(t, keys))
			useQuotaTracker(client, newTestSessionAccountTracker(t))

			previousRoutes, previousReader := codexMagpieRoutesForQuota, readCodexQuotaLimits
			codexMagpieRoutesForQuota = func() []codex.MagpieRoute { return nil }
			readCodexQuotaLimits = func(*Client, context.Context, string) (codex.RateLimits, error) {
				return codex.RateLimits{
					Source:         codex.QuotaSourceMagpie,
					GatewayBaseURL: srv.URL,
					GatewayToken:   tc.gatewayToken,
				}, nil
			}
			t.Cleanup(func() { codexMagpieRoutesForQuota, readCodexQuotaLimits = previousRoutes, previousReader })

			ctx := context.Background()
			if tc.threadID != "" {
				ctx = mcodeui.WithQuotaThread(ctx, tc.threadID)
			}
			windows, err := client.ProviderQuota(ctx, "codex")
			if err != nil {
				t.Fatalf("ProviderQuota 失败: %v", err)
			}
			assertProviders(t, windows, tc.wantCounts)
		})
	}
}
