package agy

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"mcode/internal/procattr"
	"mcode/internal/providers/loginenv"
	"mcode/internal/providers/modelcache"
)

type Model struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Efforts []string `json:"efforts,omitempty"`
	Default bool     `json:"default,omitempty"`
}

// ParseModels 按首次出现顺序合并 agy models 的模型与强度档位。
func ParseModels(out string) []Model {
	var models []Model
	positions := make(map[string]int)
	for _, line := range strings.Split(out, "\n") {
		slug, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || slug == "" {
			continue
		}
		id, effort := parseModelRow(slug)
		name = strings.TrimSpace(name)
		if suffix := "(" + effort + ")"; effort != "" && strings.HasSuffix(strings.ToLower(name), suffix) {
			name = strings.TrimSpace(name[:len(name)-len(suffix)])
		}
		pos, exists := positions[id]
		if !exists {
			pos = len(models)
			positions[id] = pos
			models = append(models, Model{ID: id, Name: name})
		}
		if effort != "" && !slices.Contains(models[pos].Efforts, effort) {
			models[pos].Efforts = append(models[pos].Efforts, effort)
		}
	}
	return models
}

var (
	modelCatalogMu sync.Mutex
	modelHarness   *Harness
	modelCache     = &modelcache.Cache[Model]{File: modelcache.DefaultFile("agy")}
)

func catalogCache() *modelcache.Cache[Model] {
	modelCatalogMu.Lock()
	defer modelCatalogMu.Unlock()
	return modelCache
}

// CachedModels 返回已缓存的账号模型目录，首次读取时在后台查询。
func CachedModels() []Model {
	return catalogCache().Get(fetchCachedModels)
}

func fetchCachedModels(ctx context.Context) ([]Model, error) {
	modelCatalogMu.Lock()
	h := modelHarness
	modelCatalogMu.Unlock()
	if h == nil {
		return nil, errors.New("agy: 模型目录尚未注册 harness")
	}
	return h.fetchModels(ctx)
}

// RefreshModels 强制重新读取账号可用的模型目录，失败时保留原目录。
func (h *Harness) RefreshModels(ctx context.Context) error {
	return catalogCache().Refresh(ctx, h.fetchModels)
}

func (h *Harness) fetchModels(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if h.opts.ModelsEnv == nil {
		return nil, errors.New("agy: 未配置模型目录账号环境")
	}
	env, args, err := h.opts.ModelsEnv()
	if err != nil {
		return nil, err
	}
	// --gemini_dir 是全局参数，必须放在 models 子命令之前。
	args = append(append([]string(nil), args...), "models")
	cmd := procattr.HideWindow(exec.CommandContext(ctx, resolveBinary(h.opts.Binary), args...))
	cmd.Env = append(loginenv.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return ParseModels(string(out)), nil
}

// SetCachedModelsForTest 隔离测试目录并返回恢复函数，不读取或写入磁盘缓存。
func SetCachedModelsForTest(models []Model) func() {
	cache := &modelcache.Cache[Model]{}
	_ = cache.Refresh(context.Background(), func(context.Context) ([]Model, error) { return models, nil })
	modelCatalogMu.Lock()
	previous := modelCache
	modelCache = cache
	modelCatalogMu.Unlock()
	return func() {
		modelCatalogMu.Lock()
		modelCache = previous
		modelCatalogMu.Unlock()
	}
}
