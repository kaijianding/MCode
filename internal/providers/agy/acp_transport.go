package agy

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"mcode/internal/harness"
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

// shortcut: ACP 通道暂不做限流换号重试，需要时参照 PrintMode 的 MarkLimited 循环补上。
func (h *Harness) runACP(ctx context.Context, turn harness.Turn, emit func(harness.Event)) error {
	var accountID string
	var args, env []string
	if h.opts.Router != nil {
		var err error
		accountID, env, args, err = h.opts.Router.Route(turn.SessionID, turn.Model, nil)
		if err != nil {
			return err
		}
	}
	turn.Env = append(append([]string{}, turn.Env...), env...)
	// ACP 不使用 Turn.ID；每次调用单独分配路由键，空 ID 与并发调用也不会串账号参数。
	turn.ID = uuid.NewString()
	h.acpArgs.Store(turn.ID, append([]string(nil), args...))
	defer h.acpArgs.Delete(turn.ID)
	if tracker, ok := h.opts.Router.(interface{ QuotaUse(string) func() }); ok {
		if end := tracker.QuotaUse(accountID); end != nil {
			defer end()
		}
	}
	return h.acp.Run(ctx, turn, func(event harness.Event) {
		if event.Type == harness.EventSession && h.opts.Router != nil && strings.TrimSpace(event.SessionID) != "" {
			h.opts.Router.Bind(event.SessionID, accountID)
		}
		_ = harness.Emit(emit, event)
	})
}
