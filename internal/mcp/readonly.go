package mcp

import (
	"encoding/json"
	"strings"
	"sync"
)

var (
	readOnlyRegistryOnce sync.Once
	readOnlyRegistry     *Registry
)

// builtinRegistry 懒初始化内置 MCP 工具注册表；各 register*Tools 仅注册定义，不会在注册阶段解引用 Server 字段。
func builtinRegistry() *Registry {
	readOnlyRegistryOnce.Do(func() {
		readOnlyRegistry = newRegistry(&Server{})
	})
	return readOnlyRegistry
}

// stripBuiltinPrefix 识别并剥离 mcode 内置 MCP 服务的三种工具名前缀。
func stripBuiltinPrefix(name string) (string, bool) {
	for _, prefix := range []string{
		"mcp__" + ServerName + "__",
		"mcp_" + ServerName + "_",
		ServerName + "/",
	} {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix), true
		}
	}
	return "", false
}

// ReadOnlyTool 判断工具调用是否属于 mcode 内置 MCP 服务的只读工具。
// name 必须带有 mcode 服务前缀（mcp__mcode__、mcp_mcode_ 或 mcode/），其他服务或裸名返回 false。
// 入口工具（kanban、automation、memory）按 args 中的 action 字段映射到内部工具名再判定；
// action 为 help 时视为只读，缺少 action、非法 JSON 或无法映射均返回 false。
func ReadOnlyTool(name string, args json.RawMessage) bool {
	toolName, ok := stripBuiltinPrefix(name)
	if !ok || toolName == "" {
		return false
	}

	if family := entryFamilyOf(toolName); family != nil {
		if len(args) == 0 {
			return false
		}
		var call struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(args, &call); err != nil {
			return false
		}
		action := strings.TrimSpace(call.Action)
		if action == entryActionHelp {
			return true
		}
		if action == "" {
			return false
		}
		mapped, isAction := family.toolName(action)
		if !isAction {
			return false
		}
		toolName = mapped
	}

	def, exists := builtinRegistry().Get(toolName)
	if !exists {
		return false
	}
	return !def.IsWrite
}
