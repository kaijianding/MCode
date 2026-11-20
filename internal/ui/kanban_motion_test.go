package mcodeui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestKanbanCardAndListRowListMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "reduced"}[reduced], func(t *testing.T) {
			setAppReduceMotion(t, reduced)
			s := newChatState(NewFakeClient())
			defer s.close()
			ks := &kanbanState{scopeID: "p1"}
			task := KanbanTask{ID: "motion", ProjectID: "p1", Title: "动效任务", Status: "todo", Column: "todo"}
			show := false
			tt := ui.NewTesterWith(func(c *ui.Context) {
				ui.Column(c.Key("kanban-motion-root")).Children(func() {
					if show {
						renderTaskCard(c, s, ks, task, kanbanTaskCardState{})
						renderKanbanListRow(c, s, task)
					}
				})
			}, 600, 400, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: reduced}})

			show = true
			tt.Frame()
			if _, ok := tt.Find(task.Title); !ok {
				t.Fatal("卡片未渲染")
			}
			if _, ok := tt.Find("kanban-list-row-motion"); !ok {
				t.Fatal("列表行未渲染")
			}
			if tt.FramePending() == reduced {
				t.Fatalf("新增卡片和列表行的待处理帧错误：%v", tt.FramePending())
			}

			if !reduced {
				tt.Advance(DurBase + DurSnap)
			}
			show = false
			tt.Frame()
			if tt.FramePending() == reduced {
				t.Fatalf("移除卡片和列表行的待处理帧错误：%v", tt.FramePending())
			}
		})
	}
}

func TestKanbanEmptyColumnPlaceholder(t *testing.T) {
	setAppReduceMotion(t, true)
	for _, tc := range []struct {
		name      string
		col       string
		tasks     []KanbanTask
		all       []KanbanTask
		searching bool
		want      bool
	}{
		{name: "todo empty", col: "todo"},
		{name: "review empty after search", col: "review", all: []KanbanTask{{ID: "filtered", Title: "被过滤任务", Status: "review", Column: "review"}}, searching: true, want: true},
		{name: "review populated", col: "review", tasks: []KanbanTask{{ID: "filled", Title: "已有任务", Status: "review", Column: "review"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newChatState(NewFakeClient())
			defer s.close()
			ks := &kanbanState{scopeID: "p1"}
			var def kanbanColumnDef
			for _, candidate := range kanbanColumnDefs {
				if candidate.key == tc.col {
					def = candidate
					break
				}
			}
			tt := ui.NewTesterWith(func(c *ui.Context) {
				renderKanbanColumn(c, s, ks, def, tc.tasks, tc.all, tc.searching, 280, kanbanTaskCardState{})
			}, 400, 300, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
			if _, got := tt.Find(def.label + "为空"); got != tc.want {
				t.Fatalf("占位框存在=%v，期望=%v", got, tc.want)
			}
		})
	}
}
