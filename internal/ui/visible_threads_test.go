package mcodeui

import (
	"reflect"
	"testing"
)

func TestVisibleThreadsFallbackExcludesProvisional(t *testing.T) {
	s := &chatState{
		projects: []Project{{ID: "p1"}},
		threadsByProj: map[string][]Thread{
			"p1": {
				{ID: "provisional", IsProvisional: true},
				{ID: "t1"},
				{ID: "t2"},
			},
		},
	}
	t.Cleanup(func() { releaseChatUIState(s) })
	var ids []string
	for _, th := range s.visibleThreads() {
		ids = append(ids, th.ID)
	}
	if !reflect.DeepEqual(ids, []string{"t1", "t2"}) {
		t.Fatalf("回退列表应只包含普通会话，实际 = %v", ids)
	}
}

func TestVisibleThreadsFallbackAndSnapshot(t *testing.T) {
	s := &chatState{
		sidebarCollapsed: true,
		projects:         []Project{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		collapsedProj:    map[string]bool{"c": true},
		threadsByProj: map[string][]Thread{
			"a": {{ID: "a1", Title: "Match A"}, {ID: "sleep", Title: "Match sleep", Dormant: true}, {ID: "other", Title: "Other"}},
			"b": {{ID: "b1", Title: "Match B"}},
			"c": {{ID: "c1", Title: "Match C"}},
		},
	}
	storeSidebarProjectOrder(s, []string{"b", "a", "c"})
	t.Cleanup(func() {
		sidebarMu.Lock()
		delete(sidebarSearch, s)
		sidebarMu.Unlock()
		releaseChatUIState(s)
	})
	sidebarMu.Lock()
	sidebarSearch[s] = &sidebarSearchState{query: " MATCH "}
	sidebarMu.Unlock()

	ids := func() []string {
		var result []string
		for _, th := range s.visibleThreads() {
			result = append(result, th.ID)
		}
		return result
	}
	if got := ids(); !reflect.DeepEqual(got, []string{"b1", "a1"}) {
		t.Fatalf("折叠时回退列表 = %v", got)
	}

	sidebarMu.Lock()
	sidebarSearch[s].visible = []Thread{{ID: "a1"}, {ID: "b1"}}
	sidebarMu.Unlock()
	s.mu.Lock()
	s.threadsByProj["b"] = nil // 归档和删除都会从活跃列表移除会话。
	s.mu.Unlock()
	if got := ids(); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Fatalf("折叠时移除会话后的列表 = %v", got)
	}

	s.mu.Lock()
	s.sidebarCollapsed = false
	s.mu.Unlock()
	if got := ids(); !reflect.DeepEqual(got, []string{"a1", "b1"}) {
		t.Fatalf("展开时快照列表 = %v", got)
	}

	sidebarMu.Lock()
	sidebarSearch[s].visible = nil
	sidebarMu.Unlock()
	if got := ids(); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Fatalf("展开且快照为空时的回退列表 = %v", got)
	}
}
