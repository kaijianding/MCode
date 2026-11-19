package mcp

import (
	"encoding/json"
	"testing"
)

func TestReadOnlyTool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tool string
		args json.RawMessage
		want bool
	}{
		{name: "mcp__mcode__session_get", tool: "mcp__mcode__session_get", want: true},
		{name: "mcp_mcode_session_get", tool: "mcp_mcode_session_get", want: true},
		{name: "mcode/session_get", tool: "mcode/session_get", want: true},
		{name: "mcp__mcode__swarm_delegate", tool: "mcp__mcode__swarm_delegate", want: false},
		{name: "kanban read_board", tool: "mcp__mcode__kanban", args: json.RawMessage(`{"action":"read_board"}`), want: true},
		{name: "kanban create_task", tool: "mcp__mcode__kanban", args: json.RawMessage(`{"action":"create_task"}`), want: false},
		{name: "kanban help", tool: "mcp__mcode__kanban", args: json.RawMessage(`{"action":"help"}`), want: true},
		{name: "kanban missing action", tool: "mcp__mcode__kanban", args: json.RawMessage(`{}`), want: false},
		{name: "kanban nil args", tool: "mcp__mcode__kanban", want: false},
		{name: "kanban invalid args", tool: "mcp__mcode__kanban", args: json.RawMessage(`{"action":123}`), want: false},
		{name: "memory search", tool: "mcp__mcode__memory", args: json.RawMessage(`{"action":"search"}`), want: true},
		{name: "memory thread_search alias", tool: "mcp__mcode__memory", args: json.RawMessage(`{"action":"thread_search"}`), want: true},
		{name: "memory propose", tool: "mcp__mcode__memory", args: json.RawMessage(`{"action":"propose"}`), want: false},
		{name: "automation list", tool: "mcp_mcode_automation", args: json.RawMessage(`{"action":"list"}`), want: true},
		{name: "automation create", tool: "mcp_mcode_automation", args: json.RawMessage(`{"action":"create"}`), want: false},
		{name: "other server", tool: "mcp__other__session_get", want: false},
		{name: "bare tool name", tool: "session_get", want: false},
		{name: "unknown tool", tool: "mcp__mcode__nonexistent_tool", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReadOnlyTool(tc.tool, tc.args); got != tc.want {
				t.Fatalf("ReadOnlyTool(%q, %s) = %v, want %v", tc.tool, string(tc.args), got, tc.want)
			}
		})
	}
}
