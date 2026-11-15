package mcodeui

import (
	"time"

	"mcode/internal/config"
	"mcode/internal/native"
)

// autoDormantScanInterval 是自动休眠扫描的最小间隔：距上次扫描不足该间隔时不再重复扫描。
const autoDormantScanInterval = time.Hour

// autoDormantCandidates 返回满足自动休眠条件的会话 ID：无新消息超过 days 天
// （UpdatedAt 早于 now-days*24h），且未休眠、未置顶、未在运行（含子线程）、无待审批请求。
// days <= 0 表示关闭，返回空；UpdatedAt 为零值的会话无法判定空闲时长，跳过。
// 已归档会话不在活跃会话列表中（isThreadDormantOrArchived 只看 Dormant），无需在此判断。
func autoDormantCandidates(threads []Thread, days int, now time.Time) []string {
	if days <= 0 {
		return nil
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	var ids []string
	for _, th := range threads {
		if th.Dormant || th.Pinned || th.IsRunning || th.SwarmBusy || th.HasPendingRequest {
			continue
		}
		if th.UpdatedAt.IsZero() || !th.UpdatedAt.Before(cutoff) {
			continue
		}
		ids = append(ids, th.ID)
	}
	return ids
}

// autoDormantDays 读取自动休眠天数：优先取设置面板的内存配置，
// 没有设置面板时按同包其它 Archive 配置的读法回退读取磁盘配置。
func (s *chatState) autoDormantDays() int {
	if st := getChatSettings(s); st != nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.cfg.Archive.AutoDormantDays()
	}
	dataDir := resolveDataDirFallback()
	if dataDir == "" {
		return config.Defaults().Archive.AutoDormantDays()
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		return config.Defaults().Archive.AutoDormantDays()
	}
	return cfg.Archive.AutoDormantDays()
}

// dormantSoundEnabled 读取「会话休眠时播放提示音」开关：优先取设置面板的内存配置，
// 没有设置面板时按同包其它 Notifications 配置的读法回退读取磁盘配置。
func (s *chatState) dormantSoundEnabled() bool {
	if st := getChatSettings(s); st != nil {
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.cfg.Notifications.PlayDormantSound()
	}
	dataDir := resolveDataDirFallback()
	if dataDir == "" {
		return config.Defaults().Notifications.PlayDormantSound()
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		return config.Defaults().Notifications.PlayDormantSound()
	}
	return cfg.Notifications.PlayDormantSound()
}

// playDormantChimeFn 负责实际发声，测试可替换为静音实现，避免跑测试时真去开音频设备。
var playDormantChimeFn = native.PlayDormantChime

// playDormantChime 播放会话休眠提示音。
func (s *chatState) playDormantChime() {
	if s == nil || !s.dormantSoundEnabled() {
		return
	}
	playDormantChimeFn()
}

// maybeAutoDormant 在距上次扫描不足 autoDormantScanInterval 时直接返回；
// 否则把超过 days 天无新消息的会话逐个休眠。
func (s *chatState) maybeAutoDormant(now time.Time, days int) {
	if s == nil || days <= 0 {
		return
	}

	s.mu.Lock()
	if !s.autoDormantScannedAt.IsZero() && now.Sub(s.autoDormantScannedAt) < autoDormantScanInterval {
		s.mu.Unlock()
		return
	}
	s.autoDormantScannedAt = now
	startedAt := time.Now()
	candidates := autoDormantCandidates(s.threads, days, now)
	s.mu.Unlock()

	if len(candidates) == 0 {
		return
	}
	go func() {
		var ops []bulkUndoOp
		var firstErr error
		for _, id := range candidates {
			token, err := s.client.MarkDormantWithUndo(id)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			projectID := s.markAutoDormantLocal(id)
			if token != "" {
				ops = append(ops, bulkUndoOp{projectID, token})
			}
		}
		if len(ops) > 0 {
			projectID := ops[0].projectID
			for _, op := range ops[1:] {
				if op.projectID != projectID {
					projectID = ""
					break
				}
			}
			s.mu.Lock()
			installed := s.lastBulk == nil || s.lastBulk.at.Before(startedAt)
			if installed {
				s.lastBulk = &lastBulkInfo{projectID: projectID, undo: ops, kind: "dormant", count: len(ops), at: time.Now()}
			}
			s.mu.Unlock()
			if installed {
				s.scheduleLastBulkExpiry()
			}
		}
		if firstErr != nil {
			s.setToast("标记休眠失败: " + firstErr.Error())
		}
		s.invalidate()
	}()
}

func (s *chatState) markAutoDormantLocal(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var projectID string
	for pid, list := range s.threadsByProj {
		for i := range list {
			if list[i].ID == id {
				list[i].Dormant = true
				projectID = pid
			}
		}
		s.threadsByProj[pid] = SortThreads(list)
	}
	for i := range s.threads {
		if s.threads[i].ID == id {
			s.threads[i].Dormant = true
			if s.threads[i].ProjectID != "" {
				projectID = s.threads[i].ProjectID
			}
		}
	}
	s.threads = SortThreads(s.threads)
	return projectID
}
