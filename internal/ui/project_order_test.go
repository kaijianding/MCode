package mcodeui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestApplyProjectOrder 验证按保存顺序重排项目：未在顺序中的项目追加到末尾。
func TestApplyProjectOrder(t *testing.T) {
	projects := []Project{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}}

	got := applyProjectOrder(projects, []string{"p3", "p1"})
	want := []Project{{ID: "p3"}, {ID: "p1"}, {ID: "p2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("applyProjectOrder = %+v, want %+v", got, want)
	}

	// 顺序为空：保持原顺序
	if got := applyProjectOrder(projects, nil); !reflect.DeepEqual(got, projects) {
		t.Fatalf("空顺序应保持原顺序，got %+v", got)
	}

	// 顺序含未知 ID 与重复项：忽略未知，重复只取一次
	got = applyProjectOrder(projects, []string{"p2", "p2", "不存在", "p1"})
	want = []Project{{ID: "p2"}, {ID: "p1"}, {ID: "p3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("含未知/重复 ID 的顺序 = %+v, want %+v", got, want)
	}

	// 顺序覆盖全部项目：完全按顺序
	got = applyProjectOrder(projects, []string{"p2", "p3", "p1"})
	want = []Project{{ID: "p2"}, {ID: "p3"}, {ID: "p1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("全量顺序 = %+v, want %+v", got, want)
	}

	if got := applyProjectOrder(nil, []string{"p1"}); len(got) != 0 {
		t.Fatalf("无项目时结果应为空，got %+v", got)
	}
}

// TestMoveProjectInOrder 验证把项目移动到指定下标：删除原位置、插入目标位置并夹取下标。
func TestMoveProjectInOrder(t *testing.T) {
	base := []string{"a", "b", "c"}

	tests := []struct {
		name    string
		order   []string
		id      string
		toIndex int
		want    []string
	}{
		{"移到最前", base, "c", 0, []string{"c", "a", "b"}},
		{"移到中间", base, "a", 1, []string{"b", "a", "c"}},
		{"移到末尾", base, "a", 2, []string{"b", "c", "a"}},
		{"下标越界夹取到末尾", base, "a", 99, []string{"b", "c", "a"}},
		{"负下标夹取到最前", base, "b", -3, []string{"b", "a", "c"}},
		{"移到自身位置保持不变", base, "b", 1, []string{"a", "b", "c"}},
		{"id 不在列表中时插入", base, "d", 1, []string{"a", "d", "b", "c"}},
		{"空 id 返回原顺序副本", base, "", 0, []string{"a", "b", "c"}},
		{"空顺序插入", nil, "a", 0, []string{"a"}},
	}
	for _, tc := range tests {
		got := moveProjectInOrder(tc.order, tc.id, tc.toIndex)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: moveProjectInOrder(%v, %q, %d) = %v, want %v", tc.name, tc.order, tc.id, tc.toIndex, got, tc.want)
		}
	}

	// 不修改入参
	order := []string{"a", "b", "c"}
	_ = moveProjectInOrder(order, "c", 0)
	if !reflect.DeepEqual(order, base) {
		t.Fatalf("moveProjectInOrder 不应修改入参，got %v", order)
	}
}

// TestUIStateProjectOrderPersistence 验证项目顺序与侧边栏状态同文件读写。
func TestUIStateProjectOrderPersistence(t *testing.T) {
	dataDir := settingsTestDir(t)
	t.Setenv("MCODE_DATA", dataDir)
	t.Cleanup(waitUIStateWrites)

	file := uiStateFile{
		UIState:      UIState{SidebarWidth: 300, SidebarHidden: true},
		ProjectOrder: []string{"p2", "p1"},
	}
	if err := saveUIStateFile(file); err != nil {
		t.Fatalf("saveUIStateFile failed: %v", err)
	}

	st := loadUIState()
	if st.SidebarWidth != 300 || !st.SidebarHidden {
		t.Fatalf("侧边栏状态未往返：%+v", st)
	}
	if got := loadProjectOrder(); !reflect.DeepEqual(got, []string{"p2", "p1"}) {
		t.Fatalf("项目顺序未往返：%v", got)
	}

	// 启动恢复：applyUIState 一并恢复项目顺序
	s := newChatState(NewFakeClient())
	applyUIState(s, st)
	if got := sidebarProjectOrder(s); !reflect.DeepEqual(got, []string{"p2", "p1"}) {
		t.Fatalf("applyUIState 未恢复项目顺序：%v", got)
	}

	// 旧文件（无 project_order 字段）仍可读取，顺序为空
	old := []byte(`{"sidebar_width": 320, "sidebar_hidden": false}`)
	if err := os.WriteFile(filepath.Join(dataDir, uiStateFileName), old, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadProjectOrder(); len(got) != 0 {
		t.Fatalf("旧文件的项目顺序应为空，got %v", got)
	}
	if got := loadUIState(); got.SidebarWidth != 320 || got.SidebarHidden {
		t.Fatalf("旧文件读取失败：%+v", got)
	}
}
