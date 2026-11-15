package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mcode/internal/harness"
	"mcode/internal/procattr"
	"mcode/internal/procutil"
	"mcode/internal/providers/loginenv"
)

// ProbedModel 是 Agent 在 session/new 应答里广告的一个模型（ACP models.availableModels）。
type ProbedModel struct {
	ID          string
	Name        string
	Description string
	Current     bool // 会话开局时 Agent 自己选中的模型
	// Efforts lists the reasoning levels this model accepts, in the order the
	// Agent advertised them. Nil means the Agent did not advertise any.
	Efforts []string
	// DefaultEffort is the level the session currently uses; empty means unknown.
	DefaultEffort string
	// ContextTokens is the model's context window in tokens; 0 means the Agent
	// did not advertise it.
	ContextTokens int
}

// ProbeModels 使用当前供应商配置的可执行文件读取目录。
// 探测必须用与真实会话相同的启动参数与环境：自定义 Agent 的 ACP 入口可能不是默认的
// "acp" 子命令（例如 rpi 用 --acp 标志），也可能需要额外环境变量。
func (h *Harness) ProbeModels(ctx context.Context) ([]ProbedModel, error) {
	bin, err := loginenv.LookPath(h.opts.Binary)
	if err != nil {
		return nil, err
	}
	env := h.opts.Env
	if extra := ExtraEnv(h.opts.Agent); len(extra) > 0 {
		env = append(append([]string{}, extra...), env...)
	}
	return probeModels(ctx, h.opts.Agent, bin, agentLaunchArgs(h.opts.Agent, h.opts, harness.ModeSupervised), env)
}

// ProbeModels 启动一次 ACP 会话只为读取模型目录：
// initialize、在临时目录 session/new，读出应答里的 models 后结束进程。
// 后台读取目录不触发登录（不调用 authenticate）。
func ProbeModels(ctx context.Context, agent Agent, binary string) ([]ProbedModel, error) {
	return probeModels(ctx, agent, binary, nil, nil)
}

// probeModels 用给定的启动参数与额外环境变量探测目录，未给定则回退到内置默认值。
func probeModels(ctx context.Context, agent Agent, binary string, args, extraEnv []string) ([]ProbedModel, error) {
	if binary == "" {
		binary = DefaultBinary(agent)
	}
	if len(args) == 0 {
		args = LaunchArgs(agent, harness.ModeSupervised)
	}
	if len(extraEnv) == 0 {
		extraEnv = ExtraEnv(agent)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	dir := filepath.Join(os.TempDir(), "mcode-acp-catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	cmd := procattr.HideWindow(exec.Command(binary, args...))
	setProcessGroup(cmd)
	cmd.Dir = dir
	cmd.Env = buildEnv(loginenv.Environ(), dir, extraEnv, nil)
	stderrBuf := procutil.NewTailBuffer(4096)
	cmd.Stderr = stderrBuf
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := waitLaunchSlot(ctx); err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	conn := NewConn(stdout, stdin)
	defer func() {
		_ = conn.Close()
		_ = killProcessGroup(cmd, syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = killProcessGroup(cmd, syscall.SIGKILL)
		}
	}()
	// 探测期间 Agent 不该发请求；万一发了一律拒绝，避免它卡在等待应答上。
	conn.SetRequestHandler(func(method string, _ json.RawMessage) (any, error) {
		return nil, fmt.Errorf("acp probe: %s not supported", method)
	})

	initParams := map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]any{"name": "mcode", "title": "mcode", "version": "1.0.0"},
	}
	if err := conn.Call(ctx, "initialize", initParams, nil); err != nil {
		return nil, probeError("initialize", err, stderrBuf)
	}

	var res struct {
		Models        *acpSessionModels `json:"models"`
		ConfigOptions []map[string]any  `json:"configOptions"`
	}
	if err := conn.Call(ctx, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}}, &res); err != nil {
		return nil, probeError("session/new", err, stderrBuf)
	}
	return modelCatalogFromSession(res.Models, res.ConfigOptions), nil
}

// acpSessionModels 是 session/new 应答里的 models 字段（ACP 标准模型目录）。
type acpSessionModels struct {
	CurrentModelID  string            `json:"currentModelId"`
	AvailableModels []acpSessionModel `json:"availableModels"`
}

type acpSessionModel struct {
	ModelID     string         `json:"modelId"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Meta        map[string]any `json:"_meta"`
}

// modelCatalogFromSession 从 session/new 应答里读出模型目录。
// models.availableModels 优先；有些 Agent（如 maki）不广告 models，
// 而是用 configOptions 里 category/id 为 model 的 select 项列出模型。
// 两条路径都没有时返回 nil。
func modelCatalogFromSession(models *acpSessionModels, configOptions []map[string]any) []ProbedModel {
	efforts, defaultEffort := sessionEfforts(configOptions)
	if models != nil && len(models.AvailableModels) > 0 {
		out := make([]ProbedModel, 0, len(models.AvailableModels))
		for _, m := range models.AvailableModels {
			if strings.TrimSpace(m.ModelID) == "" {
				continue
			}
			out = append(out, ProbedModel{
				ID:            m.ModelID,
				Name:          m.Name,
				Description:   m.Description,
				Current:       m.ModelID == models.CurrentModelID,
				Efforts:       efforts,
				DefaultEffort: defaultEffort,
				ContextTokens: modelContextTokens(m.Description, m.Meta),
			})
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}

	option := findACPModelOption(configOptions)
	if option == nil {
		return nil
	}
	current, _ := option["currentValue"].(string)
	entries, _ := option["options"].([]any)
	leaves := flattenSelectEntries(entries)
	out := make([]ProbedModel, 0, len(leaves))
	for _, leaf := range leaves {
		value, _ := leaf["value"].(string)
		name, _ := leaf["name"].(string)
		description, _ := leaf["description"].(string)
		meta, _ := leaf["_meta"].(map[string]any)
		out = append(out, ProbedModel{
			ID:            value,
			Name:          name,
			Description:   description,
			Current:       value == current,
			Efforts:       efforts,
			DefaultEffort: defaultEffort,
			ContextTokens: modelContextTokens(description, meta),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// flattenSelectEntries flattens select options into their leaf entries.
// An entry with a non-empty string value is a leaf; an entry without a value
// but with nested options is a group and is descended into (one or more levels).
func flattenSelectEntries(entries []any) []map[string]any {
	var out []map[string]any
	for _, entry := range entries {
		valueMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if value, _ := valueMap["value"].(string); strings.TrimSpace(value) != "" {
			out = append(out, valueMap)
			continue
		}
		if nested, ok := valueMap["options"].([]any); ok {
			out = append(out, flattenSelectEntries(nested)...)
		}
	}
	return out
}

// sessionEfforts reads the reasoning levels advertised by configOptions:
// the values of the effort select option in advertised order, plus its
// current value. Both are zero when no option advertises levels.
func sessionEfforts(configOptions []map[string]any) (efforts []string, current string) {
	effortConfigID, currentEffort, _, _ := selectACPConfigOptions(configOptions)
	if effortConfigID == "" {
		return nil, ""
	}
	for _, option := range configOptions {
		if id, _ := option["id"].(string); id != effortConfigID {
			continue
		}
		entries, _ := option["options"].([]any)
		for _, leaf := range flattenSelectEntries(entries) {
			if value, _ := leaf["value"].(string); strings.TrimSpace(value) != "" {
				efforts = append(efforts, value)
			}
		}
		break
	}
	return efforts, currentEffort
}

// contextTokensInText matches sizes such as "1000000 tokens", "1M context" or
// "256k tokens" in a model description.
var contextTokensInText = regexp.MustCompile(`(?i)([0-9][0-9,_.]*)\s*([km])?\s*(?:tokens?|context)`)

// metaContextKeys are the _meta keys an Agent may use to advertise a window.
var metaContextKeys = []string{
	"maxContextTokens",
	"contextWindow",
	"contextTokens",
	"contextLimit",
	"maxContextWindow",
}

// modelContextTokens reads the context window out of a model entry: the _meta
// numbers win, then the description text ("1000000 tokens") as a fallback.
// Values below 1000 are treated as noise and reported as unknown.
func modelContextTokens(description string, meta map[string]any) int {
	for _, key := range metaContextKeys {
		if tokens := sanitizeContextTokens(contextTokensValue(meta[key])); tokens > 0 {
			return tokens
		}
	}
	match := contextTokensInText.FindStringSubmatch(description)
	if match == nil {
		return 0
	}
	return contextTokensValue(match[1] + match[2])
}

// contextTokensValue converts a _meta value (number or "1M"/"128000" string)
// into a context-window token count, dropping values too small to be a window.
func contextTokensValue(value any) int {
	return sanitizeContextTokens(tokenCountValue(value))
}

// tokenCountValue converts a _meta value (number or "1M"/"128000" string) into
// a token count without the context-window noise floor.
func tokenCountValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return int(n)
		}
	case string:
		text := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(typed, ",", "")))
		text = strings.TrimSuffix(text, "tokens")
		text = strings.TrimSpace(text)
		multiplier := 1.0
		if strings.HasSuffix(text, "k") {
			multiplier, text = 1e3, strings.TrimSuffix(text, "k")
		} else if strings.HasSuffix(text, "m") {
			multiplier, text = 1e6, strings.TrimSuffix(text, "m")
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0
		}
		return int(number * multiplier)
	}
	return 0
}

func sanitizeContextTokens(tokens int) int {
	if tokens < 1000 {
		return 0
	}
	return tokens
}

func probeError(step string, err error, stderr *procutil.TailBuffer) error {
	if tail := strings.TrimSpace(stderr.String()); tail != "" {
		return fmt.Errorf("acp probe %s: %w (stderr: %s)", step, err, tail)
	}
	return fmt.Errorf("acp probe %s: %w", step, err)
}
