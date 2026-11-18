package agy

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"mcode/internal/harness"
	"mcode/internal/providers/agy/accounts"
	"mcode/internal/providers/cliupdate"
	"mcode/internal/providers/loginenv"
)

const (
	ChannelAuto      = ""
	ChannelACP       = "acp"
	ChannelPrintMode = "printmode"
	MinACPVersion    = "1.5.8"
)

func selectChannel(setting, installedVersion string) string {
	if setting == ChannelPrintMode {
		return ChannelPrintMode
	}
	if setting != ChannelAuto && setting != ChannelACP {
		return ChannelPrintMode
	}
	// CompareVersions 对无效版本返回 0；先确认版本可解析，避免未知版本误选 ACP。
	version := cliupdate.ParseVersion(installedVersion)
	if version != "" && cliupdate.CompareVersions(version, MinACPVersion) >= 0 {
		return ChannelACP
	}
	return ChannelPrintMode
}

func (h *Harness) channel(ctx context.Context) string {
	setting := ChannelAuto
	if h.opts.Channel != nil {
		setting = h.opts.Channel()
	}
	if setting == ChannelPrintMode {
		return ChannelPrintMode
	}
	bin := resolveBinary(h.opts.Binary)
	if path, err := loginenv.LookPath(bin); err == nil {
		bin = path
	}
	h.versionMu.Lock()
	defer h.versionMu.Unlock()
	if h.versions == nil {
		h.versions = make(map[string]string)
	}
	version, ok := h.versions[bin]
	if !ok {
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		info, err := cliupdate.Installed(probeCtx, "agy", bin)
		cancel()
		// 被取消的探针不写缓存，后续正常回合仍可重新检测。
		if ctx.Err() == nil {
			if err == nil {
				version = info.Installed
			}
			h.versions[bin] = version
		}
	}
	return selectChannel(setting, version)
}

// ResetVersionCache 在 CLI 更新后清除版本缓存。
func (h *Harness) ResetVersionCache() {
	h.versionMu.Lock()
	h.versions = nil
	h.versionMu.Unlock()
}

func threadAffinityKey(turn harness.Turn) string {
	if turn.ThreadID == "" {
		return ""
	}
	return "thread:" + turn.ThreadID
}

func (h *Harness) runACP(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	if turn.Attach && !h.acp.HasUnsolicitedOutput(turn.ThreadID) {
		return nil
	}
	reused, err := func() (bool, error) {
		if accountID, ok := h.acpAccounts.Load(turn.ThreadID); ok {
			if tracker, ok := h.opts.Router.(interface{ QuotaUse(string) func() }); ok {
				if end := tracker.QuotaUse(accountID.(string)); end != nil {
					defer end()
				}
			}
		}
		return h.acp.RunResident(ctx, turn, emit)
	}()
	if reused {
		if !h.acp.HasResidentProcess(turn.ThreadID) {
			h.acpAccounts.Delete(turn.ThreadID)
		}
		return err
	}
	h.acpAccounts.Delete(turn.ThreadID)
	threadKey := threadAffinityKey(turn)
	sessionID := turn.SessionID
	var exclude []string
	for retryCount := 0; ; retryCount++ {
		routeKey := sessionID
		if routeKey == "" {
			routeKey = threadKey
		}
		accountID := accounts.SystemAccountID
		var args, env []string
		if h.opts.Router != nil {
			var err error
			accountID, env, args, err = h.opts.Router.Route(routeKey, turn.Model, exclude)
			if err != nil {
				return err
			}
			if threadKey != "" {
				h.opts.Router.Bind(threadKey, accountID)
			}
		}
		attempt := turn
		attempt.SessionID = sessionID
		attempt.Prompt = prependIsolatedHomeHint(accountID, turn.Prompt)
		attempt.Env = append(append([]string{}, turn.Env...), env...)
		// 每次尝试单独分配参数键，空 ID、并发调用和重试都不会串账号参数。
		attempt.ID = uuid.NewString()
		var eventMu sync.Mutex
		var streamedText bool
		err := func() error {
			h.acpArgs.Store(attempt.ID, append([]string(nil), args...))
			defer h.acpArgs.Delete(attempt.ID)
			if tracker, ok := h.opts.Router.(interface{ QuotaUse(string) func() }); ok {
				if end := tracker.QuotaUse(accountID); end != nil {
					defer end()
				}
			}
			return h.acp.Run(ctx, attempt, func(event harness.Event) {
				eventMu.Lock()
				switch event.Type {
				case harness.EventSession:
					if strings.TrimSpace(event.SessionID) != "" {
						sessionID = event.SessionID
					}
				case harness.EventText, harness.EventToolStart, harness.EventToolOutput, harness.EventToolEnd, harness.EventFileChange:
					streamedText = true
				}
				eventMu.Unlock()
				if event.Type == harness.EventSession && h.opts.Router != nil && strings.TrimSpace(event.SessionID) != "" {
					h.opts.Router.Bind(event.SessionID, accountID)
				}
				_ = harness.Emit(emit, event)
			})
		}()
		if h.acp.HasResidentProcess(turn.ThreadID) {
			h.acpAccounts.Store(turn.ThreadID, accountID)
		}
		if err == nil || h.opts.Router == nil || !isRateLimited(err.Error()) {
			return err
		}
		eventMu.Lock()
		if sessionID != "" {
			routeKey = sessionID
		}
		streamed := streamedText
		eventMu.Unlock()
		resetAt := parseResetTime(err.Error())
		if !h.opts.Router.MarkLimited(routeKey, accountID, resetAt) {
			resetInfo := ""
			if !resetAt.IsZero() {
				resetInfo = "，重置时间：" + resetAt.Format(time.RFC3339)
			}
			return fmt.Errorf("agy: 当前会话绑定的账号已限流，严格绑定模式下不会换号%s：%w", resetInfo, err)
		}
		if streamed || retryCount >= maxRetries {
			return err
		}
		exclude = append(exclude, accountID)
	}
}

func (h *Harness) SetUnsolicitedTurnHandler(fn func(string)) {
	h.acp.SetUnsolicitedTurnHandler(fn)
}

func (h *Harness) SetProcessExitHandler(fn func(string, time.Time)) {
	h.acp.SetProcessExitHandler(func(threadID string, before time.Time) {
		if !h.acp.HasResidentProcess(threadID) {
			h.acpAccounts.Delete(threadID)
		}
		if fn != nil {
			fn(threadID, before)
		}
	})
}

func (h *Harness) HasUnsolicitedOutput(threadID string) bool {
	return h.acp.HasUnsolicitedOutput(threadID)
}

func (h *Harness) EvictThread(threadID string) {
	h.acp.EvictThread(threadID)
	if !h.acp.HasResidentProcess(threadID) {
		h.acpAccounts.Delete(threadID)
	}
}

func (h *Harness) Close() error {
	err := h.acp.Close()
	h.acpAccounts.Range(func(key, _ any) bool {
		h.acpAccounts.Delete(key)
		return true
	})
	return err
}
