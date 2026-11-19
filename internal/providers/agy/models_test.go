package agy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestParseModels(t *testing.T) {
	t.Parallel()
	out, err := os.ReadFile("testdata/models.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := []Model{
		{ID: "gemini-4-carbon", Name: "Gemini 4 Carbon", Efforts: []string{"low", "medium", "high"}},
		{ID: "gemini-4-argon", Name: "Gemini 4 Argon", Efforts: []string{"low", "medium", "high"}},
		{ID: "gemini-4-hydrogen", Name: "Gemini 4 Hydrogen", Efforts: []string{"low", "medium", "high", "xhigh"}},
		{ID: "gemini-pro-orchestrator", Name: "Orchestrator (Carbon)"},
		{ID: "gemini-pro-orchestrator-argon", Name: "Orchestrator (Argon)"},
		{ID: "gemini-3.8-flash", Name: "Gemini 3.8 Flash", Efforts: []string{"low", "medium", "high"}},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6 (Thinking)"},
		{ID: "claude-opus-4-6-thinking", Name: "Claude Opus 4.6 (Thinking)"},
		{ID: "gpt-oss-120b", Name: "GPT-OSS 120B", Efforts: []string{"medium"}},
	}
	if got := ParseModels(string(out)); !reflect.DeepEqual(got, want) {
		t.Fatalf("解析目录 = %+v，期望 %+v", got, want)
	}
	// 重复档位只保留一次；无 tab 的状态行忽略，显示名只去除同名档位括号。
	extra := "\nFetching available models...\n\n" + string(out) + "gemini-4-carbon-medium\tGemini 4 Carbon (MEDIUM)\n"
	if got := ParseModels(extra); !reflect.DeepEqual(got, want) {
		t.Fatalf("带状态行或重复档位的目录 = %+v", got)
	}
	if got := ParseModels("test-xhigh\tTest (Thinking) (XHIGH)\ntest-max\tTest (MAX)\n"); !reflect.DeepEqual(got, []Model{
		{ID: "test", Name: "Test (Thinking)", Efforts: []string{"xhigh", "max"}},
	}) {
		t.Fatalf("大小写档位括号解析 = %+v", got)
	}
}

func TestParseModelsOrdersEfforts(t *testing.T) {
	t.Parallel()
	out := "gemini-4-hydrogen-high\tGemini 4 Hydrogen (high)\n" +
		"gemini-4-hydrogen-low\tGemini 4 Hydrogen (low)\n" +
		"gemini-4-hydrogen-medium\tGemini 4 Hydrogen (medium)\n" +
		"gemini-4-hydrogen-xhigh\tGemini 4 Hydrogen (xhigh)\n"
	want := []Model{
		{ID: "gemini-4-hydrogen", Name: "Gemini 4 Hydrogen", Efforts: []string{"low", "medium", "high", "xhigh"}},
	}
	if got := ParseModels(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("解析目录 = %+v，期望 %+v", got, want)
	}
}

func TestNormalizeModelXHigh(t *testing.T) {
	t.Cleanup(SetCachedModelsForTest([]Model{{ID: "gemini-pro-orchestrator", Name: "Orchestrator (Carbon)"}}))
	for _, tc := range []struct{ model, effort, base, resolved string }{
		{"gemini-4-hydrogen-xhigh", "", "gemini-4-hydrogen", "xhigh"},
		{"gemini-4-hydrogen-max", "", "gemini-4-hydrogen", "max"},
		{"gemini-4-hydrogen-xhigh", "low", "gemini-4-hydrogen", "low"},
		{"gemini-3.1-pro", "medium", "gemini-3.1-pro", "high"},
		{"gpt-oss-120b-high", "max", "gpt-oss-120b", "medium"},
		{"claude-sonnet-4-6", "high", "claude-sonnet-4-6", ""},
		{"gemini-pro-orchestrator", "high", "gemini-pro-orchestrator", ""},
		{"gemini-pro-orchestrator", "", "gemini-pro-orchestrator", ""},
		{"gemini-3.8-flash", "", "gemini-3.8-flash", "high"},
		{"", "high", "", ""},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			if base, effort := normalizeModel(tc.model, tc.effort); base != tc.base || effort != tc.resolved {
				t.Fatalf("normalizeModel = (%q, %q)，期望 (%q, %q)", base, effort, tc.base, tc.resolved)
			}
		})
	}
	for _, effort := range []string{"xhigh", "max", "high", "medium", "low"} {
		if base, got := parseModelRow("model-" + effort); base != "model" || got != effort {
			t.Errorf("parseModelRow(%q) = (%q, %q)", effort, base, got)
		}
	}
	if args := buildArgs("", "", "gemini-pro-orchestrator", "high", "", nil, nil); slices.Contains(args, "--effort") {
		t.Fatalf("无档位模型不应传 --effort: %v", args)
	}
}

func TestRefreshModels(t *testing.T) {
	t.Cleanup(SetCachedModelsForTest([]Model{{ID: "seed"}}))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	modelsEnv := func() ([]string, []string, error) {
		return []string{fakeAgyEnv + "=models", "HOME=" + home, "AGY_MODELS_TEST_HOME=" + home},
			[]string{"--gemini_dir=" + filepath.Join(home, ".gemini")}, nil
	}
	h := New(Options{Binary: exe, ModelsEnv: modelsEnv})
	if err := h.RefreshModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := CachedModels(); len(got) != 9 || got[0].ID != "gemini-4-carbon" {
		t.Fatalf("刷新未写入动态目录: %+v", got)
	}
	t.Run("失败保留目录", func(t *testing.T) {
		wantErr := errors.New("账号不可用")
		h := New(Options{ModelsEnv: func() ([]string, []string, error) { return nil, nil, wantErr }})
		if err := h.RefreshModels(context.Background()); !errors.Is(err, wantErr) {
			t.Fatalf("刷新错误 = %v", err)
		}
		if got := CachedModels(); len(got) != 9 {
			t.Fatalf("失败丢失已有目录: %+v", got)
		}
	})
	t.Run("取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := h.fetchModels(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("取消错误 = %v", err)
		}
	})
	t.Run("缺少账号环境", func(t *testing.T) {
		if _, err := (&Harness{}).fetchModels(context.Background()); err == nil {
			t.Fatal("缺少账号环境应报错")
		}
	})
}

func TestModelCatalogConcurrentRegistration(t *testing.T) {
	t.Cleanup(SetCachedModelsForTest([]Model{{ID: "seed"}}))
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 10 {
				New(Options{})
				if got := CachedModels(); len(got) != 1 || got[0].ID != "seed" {
					t.Errorf("并发注册改变目录: %+v", got)
				}
			}
		})
	}
	wg.Wait()
}
