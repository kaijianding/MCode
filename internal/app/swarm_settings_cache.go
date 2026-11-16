package app

import (
	"os"
	"sync"

	"mcode/internal/config"
	"mcode/internal/swarm"
)

// newCachedSwarmSettings 缓存文件未变时的设置，避免热路径逐线程调用时重复解析 config.json。
func newCachedSwarmSettings(dataDir string) func() swarm.Settings {
	var mu sync.Mutex
	var cached swarm.Settings
	var cachedModTime int64
	var cachedSize int64
	var valid bool

	return func() swarm.Settings {
		mu.Lock()
		defer mu.Unlock()

		info, statErr := os.Stat(config.ConfigPath(dataDir))
		if statErr == nil && valid && info.ModTime().UnixNano() == cachedModTime && info.Size() == cachedSize {
			return cached
		}
		valid = false

		c, err := config.Load(dataDir)
		if err != nil {
			return SwarmSettingsFromConfig(config.OrchestrationConfig{})
		}
		config.ApplyEnv(&c, os.LookupEnv)
		settings := SwarmSettingsFromConfig(c.Orchestration)
		if statErr == nil {
			cached = settings
			cachedModTime = info.ModTime().UnixNano()
			cachedSize = info.Size()
			valid = true
		}
		return settings
	}
}
