package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// makiConfigOptions 复刻 maki 0.7.1 的 session/new 应答形状：
// 不返回 models，而是用 category 为 model 的 select 配置项广告 30 个模型。
func makiConfigOptions(current string) []map[string]any {
	entries := make([]any, 0, 30)
	for i := 0; i < 30; i++ {
		value := fmt.Sprintf("vendor/model-%02d", i)
		switch i {
		case 0:
			value = "google/gemini-2.5-pro"
		case 1:
			value = "google/gemini-2.5-flash"
		case 2:
			value = "dewuopenai/deepseek-v4.1-flash"
		}
		entries = append(entries, map[string]any{"value": value, "name": value})
	}
	return []map[string]any{{
		"id":           "model",
		"name":         "Model",
		"category":     "model",
		"type":         "select",
		"currentValue": current,
		"options":      entries,
	}}
}

// rpiModelOption 复刻 pi-rust 的模型项：一层分组，组对象没有 value，
// 真正的条目在组的 options 里。
func rpiModelOption(current string) map[string]any {
	return map[string]any{
		"id":           "model",
		"name":         "Model",
		"category":     "model",
		"type":         "select",
		"currentValue": current,
		"options": []any{
			map[string]any{
				"group": "dewuopenai",
				"name":  "dewuopenai",
				"options": []any{
					map[string]any{"value": "dewuopenai/deepseek-v4-flash", "name": "deepseek-v4-flash", "description": "1000000 tokens"},
					map[string]any{"value": "dewuopenai/kimi-k2", "name": "kimi-k2", "description": "256000 tokens"},
				},
			},
			map[string]any{
				"group": "dewuai",
				"name":  "dewuai",
				"options": []any{
					map[string]any{"value": "dewuai/glm-5", "name": "glm-5"},
				},
			},
		},
	}
}

// rpiThinkingOption 复刻 pi-rust 的推理强度项（category 为 thought_level）。
func rpiThinkingOption(current string) map[string]any {
	return map[string]any{
		"id":           "thinking",
		"name":         "Thinking",
		"category":     "thought_level",
		"type":         "select",
		"currentValue": current,
		"options": []any{
			map[string]any{"value": "off", "name": "Off", "description": "No reasoning"},
			map[string]any{"value": "minimal", "name": "Minimal"},
			map[string]any{"value": "low", "name": "Low"},
			map[string]any{"value": "medium", "name": "Medium"},
			map[string]any{"value": "high", "name": "High"},
		},
	}
}

func TestModelCatalogFromSession(t *testing.T) {
	standard := []ProbedModel{
		{ID: "claude-sonnet", Name: "Claude Sonnet", Description: "default choice", Current: false},
		{ID: "claude-opus", Name: "Claude Opus", Current: true},
	}

	tests := []struct {
		name string
		in   func() (*acpSessionModels, []map[string]any)
		want func(t *testing.T, got []ProbedModel)
	}{
		{
			name: "availableModels only",
			in: func() (*acpSessionModels, []map[string]any) {
				return &acpSessionModels{
					CurrentModelID: "claude-opus",
					AvailableModels: []acpSessionModel{
						{ModelID: "claude-sonnet", Name: "Claude Sonnet", Description: "default choice"},
						{ModelID: "claude-opus", Name: "Claude Opus"},
						{ModelID: "  "},
					},
				}, nil
			},
			want: func(t *testing.T, got []ProbedModel) {
				if len(got) != 2 {
					t.Fatalf("got %d models, want 2", len(got))
				}
				if !reflect.DeepEqual(got, standard) {
					t.Fatalf("got %+v, want %+v", got, standard)
				}
			},
		},
		{
			name: "configOptions only (maki shape)",
			in: func() (*acpSessionModels, []map[string]any) {
				return nil, makiConfigOptions("dewuopenai/deepseek-v4.1-flash")
			},
			want: func(t *testing.T, got []ProbedModel) {
				if len(got) != 30 {
					t.Fatalf("got %d models, want 30", len(got))
				}
				if got[0].ID != "google/gemini-2.5-pro" || got[0].Name != "google/gemini-2.5-pro" {
					t.Fatalf("first model = %+v", got[0])
				}
				if got[1].ID != "google/gemini-2.5-flash" {
					t.Fatalf("second model = %+v", got[1])
				}
				if got[2].ID != "dewuopenai/deepseek-v4.1-flash" || !got[2].Current {
					t.Fatalf("current model = %+v", got[2])
				}
				current := 0
				for _, m := range got {
					if m.Current {
						current++
						if m.ID != "dewuopenai/deepseek-v4.1-flash" {
							t.Fatalf("unexpected current model %q", m.ID)
						}
					}
				}
				if current != 1 {
					t.Fatalf("got %d current models, want 1", current)
				}
			},
		},
		{
			name: "availableModels wins over configOptions",
			in: func() (*acpSessionModels, []map[string]any) {
				return &acpSessionModels{
					CurrentModelID:  "claude-opus",
					AvailableModels: []acpSessionModel{{ModelID: "claude-sonnet", Name: "Claude Sonnet", Description: "default choice"}, {ModelID: "claude-opus", Name: "Claude Opus"}},
				}, makiConfigOptions("dewuopenai/deepseek-v4.1-flash")
			},
			want: func(t *testing.T, got []ProbedModel) {
				if len(got) != 2 || !reflect.DeepEqual(got, standard) {
					t.Fatalf("got %+v, want %+v", got, standard)
				}
			},
		},
		{
			name: "neither source",
			in: func() (*acpSessionModels, []map[string]any) {
				return &acpSessionModels{}, nil
			},
			want: func(t *testing.T, got []ProbedModel) {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
			},
		},
		{
			name: "configOptions without a model option",
			in: func() (*acpSessionModels, []map[string]any) {
				return nil, []map[string]any{{
					"id":       "thought_level",
					"category": "thought_level",
					"type":     "select",
					"options":  []any{map[string]any{"value": "low"}, map[string]any{"value": "high"}},
				}}
			},
			want: func(t *testing.T, got []ProbedModel) {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
			},
		},
		{
			name: "grouped model options (rpi shape)",
			in: func() (*acpSessionModels, []map[string]any) {
				return nil, []map[string]any{rpiModelOption("dewuopenai/deepseek-v4-flash")}
			},
			want: func(t *testing.T, got []ProbedModel) {
				want := []ProbedModel{
					{ID: "dewuopenai/deepseek-v4-flash", Name: "deepseek-v4-flash", Description: "1000000 tokens", Current: true, ContextTokens: 1000000},
					{ID: "dewuopenai/kimi-k2", Name: "kimi-k2", Description: "256000 tokens", ContextTokens: 256000},
					{ID: "dewuai/glm-5", Name: "glm-5"},
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %+v, want %+v", got, want)
				}
				for _, m := range got {
					if len(m.Efforts) != 0 || m.DefaultEffort != "" {
						t.Fatalf("%s 不应带档位: %+v", m.ID, m)
					}
				}
			},
		},
		{
			name: "grouped models carry advertised efforts",
			in: func() (*acpSessionModels, []map[string]any) {
				return nil, []map[string]any{rpiModelOption("dewuopenai/kimi-k2"), rpiThinkingOption("medium")}
			},
			want: func(t *testing.T, got []ProbedModel) {
				if len(got) != 3 || !got[1].Current {
					t.Fatalf("got %+v", got)
				}
				wantEfforts := []string{"off", "minimal", "low", "medium", "high"}
				for _, m := range got {
					if !reflect.DeepEqual(m.Efforts, wantEfforts) || m.DefaultEffort != "medium" {
						t.Fatalf("%s 档位 = %v/%q，想要 %v/medium", m.ID, m.Efforts, m.DefaultEffort, wantEfforts)
					}
				}
			},
		},
		{
			name: "availableModels carry advertised efforts",
			in: func() (*acpSessionModels, []map[string]any) {
				return &acpSessionModels{
					CurrentModelID:  "claude-opus",
					AvailableModels: []acpSessionModel{{ModelID: "claude-sonnet", Name: "Claude Sonnet", Description: "default choice"}, {ModelID: "claude-opus", Name: "Claude Opus"}},
				}, []map[string]any{rpiThinkingOption("high")}
			},
			want: func(t *testing.T, got []ProbedModel) {
				if len(got) != 2 || !got[1].Current {
					t.Fatalf("got %+v", got)
				}
				wantEfforts := []string{"off", "minimal", "low", "medium", "high"}
				for _, m := range got {
					if !reflect.DeepEqual(m.Efforts, wantEfforts) || m.DefaultEffort != "high" {
						t.Fatalf("%s 档位 = %v/%q，想要 %v/high", m.ID, m.Efforts, m.DefaultEffort, wantEfforts)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			models, options := tc.in()
			tc.want(t, modelCatalogFromSession(models, options))
		})
	}
}

func TestSelectACPModelOption(t *testing.T) {
	tests := []struct {
		name       string
		options    []map[string]any
		wantID     string
		wantLen    int
		wantValues []string // only the leading entries are pinned for the 30-model maki shape
	}{
		{
			name:       "maki shape",
			options:    makiConfigOptions("dewuopenai/deepseek-v4.1-flash"),
			wantID:     "model",
			wantLen:    30,
			wantValues: []string{"google/gemini-2.5-pro", "google/gemini-2.5-flash"},
		},
		{
			name: "effort options are not model options",
			options: []map[string]any{
				{
					"id":           "thought_level",
					"category":     "thought_level",
					"type":         "select",
					"currentValue": "medium",
					"options":      []any{map[string]any{"value": "low"}, map[string]any{"value": "high"}},
				},
				{
					"id":       "fast",
					"category": "model_config",
					"type":     "boolean",
				},
			},
			wantID:     "",
			wantLen:    0,
			wantValues: nil,
		},
		{
			name:       "empty input",
			options:    nil,
			wantID:     "",
			wantLen:    0,
			wantValues: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, values := selectACPModelOption(tc.options)
			if id != tc.wantID {
				t.Fatalf("configID = %q, want %q", id, tc.wantID)
			}
			if len(values) != tc.wantLen {
				t.Fatalf("got %d values (%v), want %d", len(values), values, tc.wantLen)
			}
			for i, want := range tc.wantValues {
				if values[i] != want {
					t.Fatalf("values[%d] = %q, want %q", i, values[i], want)
				}
			}
		})
	}
}

// TestProbeModelsUsesConfiguredLaunchAndEnv 保证探测用的启动参数与环境变量与真实会话一致：
// 自定义 Agent 的 ACP 入口往往不是默认的 "acp" 子命令（rpi 用 --acp），也可能需要额外环境变量。
func TestProbeModelsUsesConfiguredLaunchAndEnv(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "probe.log")
	h := New(Options{
		Agent:  Agent("custom-probe-agent"),
		Binary: os.Args[0],
		Args:   []string{"-test.run=^$", "acp-flag"},
		Env: []string{
			fakeACPEnv + "=catalog",
			"MCODE_TEST_FAKE_ACP_ARGS_LOG=" + logPath,
			"MCODE_TEST_FAKE_ACP_ENV_MARKER=env-marker",
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := h.ProbeModels(ctx)
	if err != nil {
		t.Fatalf("ProbeModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "vendor/probed-model" || !models[0].Current {
		t.Fatalf("unexpected catalog: %+v", models)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("读取探测子进程记录失败: %v", err)
	}
	record := string(data)
	if !strings.Contains(record, "acp-flag") {
		t.Fatalf("探测未使用配置的启动参数: %s", record)
	}
	if !strings.Contains(record, "env-marker") {
		t.Fatalf("探测未传递配置的环境变量: %s", record)
	}
}

func TestModelCatalogContextTokens(t *testing.T) {
	tests := []struct {
		name        string
		description string
		meta        map[string]any
		want        int
	}{
		{name: "meta maxContextTokens", meta: map[string]any{"maxContextTokens": float64(1000000)}, want: 1000000},
		{name: "meta contextWindow string", meta: map[string]any{"contextWindow": "128k"}, want: 128000},
		{name: "meta wins over description", description: "256000 tokens", meta: map[string]any{"contextLimit": 200000}, want: 200000},
		{name: "description tokens", description: "1000000 tokens", want: 1000000},
		{name: "description context", description: "1M context · tools", want: 1000000},
		{name: "description k tokens", description: "256k tokens", want: 256000},
		{name: "noise", description: "fast and cheap", want: 0},
		{name: "too small", meta: map[string]any{"maxContextTokens": float64(42)}, want: 0},
		{name: "nothing", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelContextTokens(tc.description, tc.meta); got != tc.want {
				t.Fatalf("modelContextTokens(%q, %v) = %d, want %d", tc.description, tc.meta, got, tc.want)
			}
		})
	}
}

func TestModelCatalogFromSessionContextTokens(t *testing.T) {
	catalog := modelCatalogFromSession(&acpSessionModels{
		CurrentModelID: "vendor/one",
		AvailableModels: []acpSessionModel{
			{ModelID: "vendor/one", Name: "One", Description: "1M context", Meta: map[string]any{"maxContextTokens": float64(1000000)}},
			{ModelID: "vendor/two", Name: "Two", Description: "256000 tokens"},
		},
	}, nil)
	if len(catalog) != 2 || catalog[0].ContextTokens != 1000000 || catalog[1].ContextTokens != 256000 {
		t.Fatalf("availableModels 上下文窗口错误: %+v", catalog)
	}
	catalog = modelCatalogFromSession(nil, []map[string]any{{
		"id":           "model",
		"category":     "model",
		"type":         "select",
		"currentValue": "vendor/one",
		"options": []any{
			map[string]any{"value": "vendor/one", "name": "One", "description": "128k tokens"},
			map[string]any{"value": "vendor/two", "name": "Two"},
		},
	}})
	if len(catalog) != 2 || catalog[0].ContextTokens != 128000 || catalog[1].ContextTokens != 0 {
		t.Fatalf("configOptions 上下文窗口错误: %+v", catalog)
	}
}
