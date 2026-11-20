package magpie

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
)

func TestModelProvider(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("请求路径 = %q", r.URL.Path)
		}
		io.WriteString(w, `{"data":[
			{"id":"auto/model","owned_by":" DeepSeek "},
			{"id":"unknown","owned_by":"  "}]}`)
	}))
	defer srv.Close()
	for _, tc := range []struct {
		model string
		want  string
	}{
		{"auto/model", "deepseek"},
		{"model", ""},
		{"AUTO/model", ""},
		{"unknown", ""},
		{"missing", ""},
	} {
		provider, ok := ModelProvider(context.Background(), srv.URL+"/v1", "key", tc.model)
		if provider != tc.want || ok != (tc.want != "") {
			t.Errorf("ModelProvider(%q) = %q, %v", tc.model, provider, ok)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("模型目录未复用缓存，请求次数 = %d", hits.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if provider, ok := ModelProvider(ctx, srv.URL+"/uncached", "key", "auto/model"); provider != "" || ok {
		t.Errorf("目录读取失败应返回未知: %q, %v", provider, ok)
	}
}

func TestModelProviders(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("请求路径 = %q", r.URL.Path)
		}
		io.WriteString(w, `{"data":[
			{"id":"m-codex","owned_by":" CODEX "},
			{"id":"m-claude","owned_by":" Claude "},
			{"id":"m-codex-2","owned_by":"codex"},
			{"id":"m-unowned","owned_by":"  "}]}`)
	}))
	defer srv.Close()

	if got := ModelProviders(context.Background(), srv.URL+"/v1", "key", nil); got != nil {
		t.Errorf("空切片应返回 nil，实际 %#v", got)
	}
	if got := ModelProviders(context.Background(), srv.URL+"/v1", "key", []string{"", "  \t "}); got != nil {
		t.Errorf("仅空白项应返回 nil，实际 %#v", got)
	}
	if hits.Load() != 0 {
		t.Errorf("空模型列表不应发 HTTP 请求，请求次数 = %d", hits.Load())
	}

	got := ModelProviders(context.Background(), srv.URL+"/v1", "key", []string{
		" m-claude ",
		"m-unowned",
		"m-missing",
		"m-codex",
		"m-codex-2",
		"m-claude",
	})
	want := []string{"claude", "codex"}
	if !slices.Equal(got, want) {
		t.Errorf("ModelProviders = %#v，想要 %#v", got, want)
	}

	got2 := ModelProviders(context.Background(), srv.URL+"/v1", "key", []string{"m-codex", "m-claude"})
	if !slices.Equal(got2, []string{"codex", "claude"}) {
		t.Errorf("复用缓存时顺序应保持: %#v", got2)
	}
	if hits.Load() != 1 {
		t.Errorf("模型目录未复用缓存，请求次数 = %d", hits.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := ModelProviders(ctx, srv.URL+"/uncached", "key", []string{"m-codex"}); got != nil {
		t.Errorf("目录读取失败应返回 nil，实际 %#v", got)
	}
}
