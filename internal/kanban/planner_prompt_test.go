package kanban

import (
	"strings"
	"testing"

	"mcode/internal/store"
)

func TestPlanPromptRequirementsGranularity(t *testing.T) {
	t.Parallel()
	prompt := buildPlanPrompt(store.Goal{Prompt: "目标"}, "")
	for _, want := range []string{"最小够用", "单独验证", "并入需要它的任务", "合成一个任务"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("规划提示词缺少 %q", want)
		}
	}
}
