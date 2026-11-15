package harness

import (
	"strings"
	"sync"
)

// dynamicCatalogProviders holds providers registered at runtime. The switch in
// IsCLICatalogProvider is a compile-time enumeration of built-in CLIs; custom ACP
// agents are only known from config, so they register themselves on startup.
var dynamicCatalogProviders sync.Map

// RegisterCLICatalogProvider marks a provider as one whose model catalog is listed
// dynamically by its CLI. Safe for concurrent use; empty ids are ignored.
func RegisterCLICatalogProvider(provider string) {
	id := normalizeCatalogProvider(provider)
	if id == "" {
		return
	}
	dynamicCatalogProviders.Store(id, struct{}{})
}

func normalizeCatalogProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// IsCLICatalogProvider 判断模型目录是否由 CLI 动态列出。
func IsCLICatalogProvider(provider string) bool {
	switch normalizeCatalogProvider(provider) {
	case "commandcode", "cline", "pi", "omp", "copilot", "droid", "cursor", "opencode", "grok", "devin":
		return true
	}
	_, ok := dynamicCatalogProviders.Load(normalizeCatalogProvider(provider))
	return ok
}
