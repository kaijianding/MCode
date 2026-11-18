package swarm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mcode/internal/reviewcheck"
)

func TestParseDelegationsAcceptance(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{`[" true ", "", "printf 'a,b'", false]`, []string{"true", "printf 'a,b'"}},
		{`" printf 'a,b' "`, []string{"printf 'a,b'"}},
		{`false`, nil},
		{`{"command":"true"}`, nil},
		{`["", 1]`, nil},
		{`["1","2","3","4","5","6","7","8","9"]`, []string{"1", "2", "3", "4", "5", "6", "7", "8"}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			entries, _, err := ParseDelegations("```swarm\n[{\"prompt\":\"edit\",\"acceptance\":" + tc.raw + "}]\n```")
			if err != nil || len(entries) != 1 {
				t.Fatalf("parse: %v %v", entries, err)
			}
			if !reflect.DeepEqual(entries[0].Acceptance, tc.want) {
				t.Fatalf("acceptance=%v want=%v", entries[0].Acceptance, tc.want)
			}
		})
	}
}

func TestManagerAcceptanceModes(t *testing.T) {
	for _, mode := range []string{"ai", "human", "off"} {
		for _, pass := range []bool{false, true} {
			name := mode + "/false"
			command := "printf 'acceptance failed' >&2; false"
			if pass {
				name = mode + "/true"
				command = "test -f internal/feature.go"
			}
			t.Run(name, func(t *testing.T) {
				fake := &funnelFake{}
				m, dir := newFunnelManager(t, fake)
				defer m.Stop()
				settings := m.Settings()
				settings.ReviewMode = mode
				settings.RetryFailed = true
				m.SetSettingsFunc(func() Settings { return settings })
				bees, err := m.Dispatch(context.Background(), DispatchRequest{
					LeadThreadID: "lead-acceptance", LeadWorkDir: dir,
					Bees: []BeeSpec{{Name: "Aspen", Task: "修改 internal/feature.go", Acceptance: []string{command}}},
				})
				if err != nil {
					t.Fatal(err)
				}
				bee := bees[0]
				writeBeeFile(t, bee, "internal/feature.go", "package feature\n")
				completeBee(t, m, bee, "完成")
				h := waitFunnel(t, m, "lead-acceptance", bee.ThreadID, func(b Bee) bool {
					if mode == "off" {
						if pass {
							return b.Merged
						}
						return b.Status == StatusFailed
					}
					if !pass {
						return isRejected(b)
					}
					if mode == "human" {
						return isNeedsHuman(b)
					}
					return isApproved(b)
				})
				if mode == "off" {
					if check, generate := fake.calls(); check != 0 || generate != 0 {
						t.Fatalf("off ran project check or review: %d, %d", check, generate)
					}
				}
				if !pass {
					if h.Merged {
						t.Fatal("failed acceptance merged")
					}
					if _, err := os.Stat(filepath.Join(dir, "internal/feature.go")); !os.IsNotExist(err) {
						t.Fatalf("main copy changed: %v", err)
					}
					if mode == "off" {
						if h.Status != StatusFailed || h.PendingMerge || !strings.Contains(BuildReport([]Bee{h}), "验收命令失败："+command+"（退出码 1）") || !strings.Contains(BuildReport([]Bee{h}), "acceptance failed") {
							t.Fatalf("failed result: %+v", h)
						}
						if _, err := os.Stat(h.WorktreePath); err != nil {
							t.Fatal(err)
						}
						if _, err := os.Stat(h.ArchivedPatchPath); err != nil {
							t.Fatal(err)
						}
						m.Tick(context.Background())
						if got := m.State("lead-acceptance").Bees; len(got) != 1 || got[0].ThreadID != bee.ThreadID {
							t.Fatalf("unexpected retry: %+v", got)
						}
					} else if !strings.HasPrefix(h.ReviewReason, "验收命令失败：") || !strings.Contains(h.ReviewReason, "退出码 1") || !strings.Contains(h.ReviewReason, "acceptance failed") {
						t.Fatalf("reason=%q", h.ReviewReason)
					}
					return
				}
				if !containsReviewConcern(h.ReviewConcerns, "验收命令通过：1 条") {
					t.Fatalf("missing concern: %v", h.ReviewConcerns)
				}
				if mode == "human" {
					if h.Merged {
						t.Fatal("human review skipped")
					}
					if _, err := m.ApproveMerge(context.Background(), "lead-acceptance", bee.ThreadID); err != nil {
						t.Fatal(err)
					}
				} else if !h.Merged {
					t.Fatalf("not merged: %+v", h)
				}
			})
		}
	}
}

func TestRunAcceptanceStopsAndSkips(t *testing.T) {
	m := NewManager(Options{})
	dir := t.TempDir()
	h := Bee{WorktreePath: dir, ChangedFiles: []string{"a.go"}, Acceptance: []string{"printf before; exit 7", "touch should-not-run"}}
	passed, detail := m.runAcceptance(context.Background(), "lead", h)
	if passed || !strings.Contains(detail, "退出码 7") || !strings.Contains(detail, "before") {
		t.Fatalf("passed=%v detail=%q", passed, detail)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-run")); !os.IsNotExist(err) {
		t.Fatalf("second command ran: %v", err)
	}
	for _, skip := range []Bee{
		{ReadOnly: true, WorktreePath: dir, ChangedFiles: h.ChangedFiles, Acceptance: []string{"false"}},
		{WorktreePath: dir, Acceptance: []string{"false"}},
		{WorktreePath: dir, ChangedFiles: h.ChangedFiles},
	} {
		if ok, detail := m.runAcceptance(context.Background(), "lead", skip); !ok || detail != "" {
			t.Fatalf("skip: %v %q", ok, detail)
		}
	}
}

func TestRunAcceptanceReportsProgress(t *testing.T) {
	for _, outcome := range []string{"通过", "失败", "取消", "超时"} {
		t.Run(outcome, func(t *testing.T) {
			m := NewManager(Options{ReviewProjectConfig: func(string, string) (reviewcheck.ProjectConfig, error) {
				return reviewcheck.ProjectConfig{TimeoutSeconds: 1}, nil
			}})
			dir := t.TempDir()
			command := "touch started; i=0; while [ ! -f finish ] && [ $i -lt 300 ]; do sleep 0.01; i=$((i+1)); done; test -f finish"
			if outcome == "失败" {
				command += "; exit 7"
			}
			h := Bee{ThreadID: "bee", WorktreePath: dir, ChangedFiles: []string{"a.go"}, Acceptance: []string{"true", command}}
			m.leads["lead"] = &leadState{leadThreadID: "lead", bees: []Bee{h}}
			updates := make(chan Bee, 4)
			m.SetOnChange(func(id string) { updates <- m.State(id).Bees[0] })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				passed bool
				detail string
			}
			done := make(chan result, 1)
			go func() {
				passed, detail := m.runAcceptance(ctx, "lead", h)
				done <- result{passed, detail}
			}()
			// 失败时也取消并等待，避免验收子进程或通知回调遗留到下一用例。
			finished := false
			defer func() {
				cancel()
				if !finished {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("验收取消后未退出")
					}
				}
			}()
			for step := 1; step <= 2; step++ {
				select {
				case b := <-updates:
					if b.AcceptanceStep != step || b.AcceptanceStartedAt == nil {
						t.Fatalf("命令进度不符: %+v", b)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("未收到验收进度通知")
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("第 2 条命令未启动")
				}
				time.Sleep(5 * time.Millisecond)
			}
			b := m.State("lead").Bees[0]
			if b.AcceptanceStep != 2 || b.AcceptanceStartedAt == nil {
				t.Fatalf("运行中的进度不符: %+v", b)
			}
			// UI 快照保留进度并隔离时间指针，JSON 持久化则丢弃瞬时状态。
			*b.AcceptanceStartedAt = time.Time{}
			if m.State("lead").Bees[0].AcceptanceStartedAt.IsZero() {
				t.Fatal("快照未独立复制验收时间")
			}
			data, err := json.Marshal(m.State("lead").Bees[0])
			if err != nil {
				t.Fatal(err)
			}
			var restored Bee
			if err := json.Unmarshal(data, &restored); err != nil || restored.AcceptanceStep != 0 || restored.AcceptanceStartedAt != nil {
				t.Fatalf("持久化恢复了瞬时进度: %+v, %v", restored, err)
			}
			switch outcome {
			case "通过", "失败":
				if err := os.WriteFile(filepath.Join(dir, "finish"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			case "取消":
				cancel()
			}
			select {
			case got := <-done:
				finished = true
				if got.passed != (outcome == "通过") || (outcome == "超时" && !strings.Contains(got.detail, "超时")) {
					t.Fatalf("验收结果不符: %+v", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("验收未结束")
			}
			b = m.State("lead").Bees[0]
			if b.AcceptanceStep != 0 || b.AcceptanceStartedAt != nil {
				t.Fatalf("验收结束未清理进度: %+v", b)
			}
			if b := <-updates; b.AcceptanceStep != 0 || b.AcceptanceStartedAt != nil {
				t.Fatalf("未通知进度清理: %+v", b)
			}
		})
	}
}

func TestRunAcceptanceRewritesAbsoluteCdToWorktree(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	worktree := filepath.Join(parent, "copy")
	other := source + "-other"
	for _, dir := range []string{source, worktree, other} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, "only-copy"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(Options{})
	h := Bee{TargetWorkDir: source, WorktreePath: worktree, ChangedFiles: []string{"a.go"}, Acceptance: []string{"cd '" + source + "' && test -f only-copy"}}
	if passed, detail := m.runAcceptance(context.Background(), "lead", h); !passed {
		t.Fatalf("passed=%v detail=%q", passed, detail)
	}
	h.Acceptance = []string{"cd '" + other + "' && test ! -f only-copy"}
	if passed, detail := m.runAcceptance(context.Background(), "lead", h); !passed {
		t.Fatalf("同前缀路径不应改写: passed=%v detail=%q", passed, detail)
	}
	h.Acceptance = []string{"cd '" + source + "' && false"}
	if passed, detail := m.runAcceptance(context.Background(), "lead", h); passed || !strings.Contains(detail, h.Acceptance[0]) {
		t.Fatalf("失败详情应保留原始命令: passed=%v detail=%q", passed, detail)
	}
}

func TestRunAcceptanceTimeoutAndCancel(t *testing.T) {
	m := NewManager(Options{ReviewProjectConfig: func(string, string) (reviewcheck.ProjectConfig, error) {
		return reviewcheck.ProjectConfig{TimeoutSeconds: 1}, nil
	}})
	h := Bee{WorktreePath: t.TempDir(), ChangedFiles: []string{"a.go"}, Acceptance: []string{"sleep 10", "touch should-not-run"}}
	if passed, detail := m.runAcceptance(context.Background(), "lead", h); passed || !strings.Contains(detail, "超时") {
		t.Fatalf("timeout: %v %q", passed, detail)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if passed, _ := m.runAcceptance(ctx, "lead", h); passed {
		t.Fatal("canceled acceptance passed")
	}
	if budget := m.reviewCheckBudget("lead", h); budget != 3*time.Second+reviewCheckBudgetMargin {
		t.Fatalf("budget=%v", budget)
	}
}

func TestAcceptancePlanAndPersistence(t *testing.T) {
	m, dir := newFunnelManager(t, &funnelFake{})
	commands := []string{"true", "false"}
	plan := m.planEntry(context.Background(), "lead", Delegation{Prompt: "edit", Acceptance: commands}, nil, "", dir, map[int]BeeStatus{}, 0)
	commands[0] = "changed"
	if !reflect.DeepEqual(plan.spec.Acceptance, []string{"true", "false"}) {
		t.Fatal(plan.spec.Acceptance)
	}
	for _, value := range []any{plan.spec, Bee{Acceptance: plan.spec.Acceptance}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Acceptance []string `json:"acceptance"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded.Acceptance, plan.spec.Acceptance) {
			t.Fatalf("roundtrip: %s %v", data, err)
		}
	}
	m.mu.Lock()
	m.leads["lead"] = &leadState{leadThreadID: "lead"}
	m.mu.Unlock()
	m.recordFailedBee("lead", "", dir, plan.spec, 0, nil)
	if got := m.State("lead").Bees; len(got) != 1 || !reflect.DeepEqual(got[0].Acceptance, plan.spec.Acceptance) {
		t.Fatalf("启动失败丢失验收命令：%+v", got)
	}
	bee := Bee{Acceptance: plan.spec.Acceptance}
	copy := bee.clone()
	copy.Acceptance[0] = "changed"
	if bee.Acceptance[0] != "true" {
		t.Fatal("clone shares acceptance")
	}
}

func TestAcceptanceContinuesToL2(t *testing.T) {
	fake := &funnelFake{}
	m, dir := newFunnelManager(t, fake)
	settings := m.Settings()
	settings.ReviewMaxChangedLines = 0
	m.SetSettingsFunc(func() Settings { return settings })
	bees, err := m.Dispatch(context.Background(), DispatchRequest{
		LeadThreadID: "lead-l2-acceptance", LeadWorkDir: dir,
		Bees: []BeeSpec{{Name: "Aspen", Task: "修改 internal/feature.go", Acceptance: []string{"true"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeBeeFile(t, bees[0], "internal/feature.go", "package feature\n")
	completeBee(t, m, bees[0], "完成")
	h := waitFunnel(t, m, "lead-l2-acceptance", bees[0].ThreadID, isApproved)
	if h.ReviewLayer != "l2" || !containsReviewConcern(h.ReviewConcerns, "验收命令通过：1 条") {
		t.Fatalf("L2丢失验收结果：%+v", h)
	}
}

func TestAcceptanceCommitLanding(t *testing.T) {
	for _, command := range []string{"true", "false"} {
		t.Run(command, func(t *testing.T) {
			m, _, root := newCommitLandingManager(t)
			bee := startCommitLandingBee(t, m, root, "lead-commit-acceptance", 0)
			m.mu.Lock()
			m.leads["lead-commit-acceptance"].bees[0].Acceptance = []string{command}
			m.mu.Unlock()
			before := gitTestRun(t, root, "rev-parse", "HEAD")
			writePipelineFile(t, bee.WorktreePath, "lines.txt", strings.Replace(landingBase, "one\n", "changed\n", 1))
			completeBee(t, m, bee, "完成")
			bee = waitFunnel(t, m, "lead-commit-acceptance", bee.ThreadID, func(b Bee) bool {
				return b.Merged || b.Status == StatusFailed
			})
			m.wg.Wait()
			if command == "false" {
				if bee.Merged || bee.Status != StatusFailed || gitTestRun(t, root, "rev-parse", "HEAD") != before {
					t.Fatalf("failed acceptance landed: %+v", bee)
				}
				assertLandingFile(t, root, landingBase)
			} else {
				assertLandingCommit(t, m, root, bee)
			}
		})
	}
}

func TestAcceptanceOffTickDoesNotBlock(t *testing.T) {
	for _, command := range []string{"sleep 3", ""} {
		t.Run(command, func(t *testing.T) {
			fake := &funnelFake{}
			m, dir := newFunnelManager(t, fake)
			settings := m.Settings()
			settings.ReviewMode = "off"
			m.SetSettingsFunc(func() Settings { return settings })
			var acceptance []string
			if command != "" {
				acceptance = []string{command}
			}
			bees, err := m.Dispatch(context.Background(), DispatchRequest{
				LeadThreadID: "lead-async-acceptance", LeadWorkDir: dir,
				Bees: []BeeSpec{{Name: "Aspen", Task: "修改 internal/feature.go", Acceptance: acceptance}},
			})
			if err != nil {
				t.Fatal(err)
			}
			bee := bees[0]
			writeBeeFile(t, bee, "internal/feature.go", "package feature\n")
			completeBee(t, m, bee, "完成")
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer func() { cancel(); m.wg.Wait() }()
			started := time.Now()
			m.Tick(ctx)
			if elapsed := time.Since(started); elapsed >= time.Second {
				t.Fatalf("Tick blocked: %v", elapsed)
			}
			h := beeByThread(t, m, "lead-async-acceptance", bee.ThreadID)
			if command == "" {
				if !h.Merged || h.PendingMerge || h.ReviewState != ReviewNone {
					t.Fatalf("bee without acceptance did not merge directly: %+v", h)
				}
			} else {
				if !h.PendingMerge || h.Merged || h.ReviewStage != ReviewStageL0 {
					t.Fatalf("acceptance not queued: %+v", h)
				}
				waitFunnel(t, m, "lead-async-acceptance", bee.ThreadID, isApproved)
			}
			if check, generate := fake.calls(); check != 0 || generate != 0 {
				t.Fatalf("off ran project check or review: %d, %d", check, generate)
			}
		})
	}
}

func TestAcceptanceOffReportsAfterSettlement(t *testing.T) {
	for _, result := range []string{"false", "true"} {
		t.Run(result, func(t *testing.T) {
			m, dir := newFunnelManager(t, &funnelFake{})
			settings := m.Settings()
			settings.ReviewMode = "off"
			m.SetSettingsFunc(func() Settings { return settings })
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer func() { cancel(); m.wg.Wait() }()
			var reports []string
			idle := 0
			m.opts.OnReport = func(leadID, report string) error {
				h := m.State(leadID).Bees[0]
				if h.PendingMerge || h.ReviewState == ReviewRunning {
					t.Errorf("reported before settlement: %+v", h)
				}
				if result == "true" {
					if !h.Merged {
						t.Error("reported success before merge")
					}
					if _, err := os.Stat(filepath.Join(dir, "internal/feature.go")); err != nil {
						t.Errorf("reported success before file landed: %v", err)
					}
				} else if h.Status != StatusFailed {
					t.Errorf("reported failure before failed status: %+v", h)
				}
				reports = append(reports, report)
				return nil
			}
			m.opts.OnBeesIdle = func(context.Context, string) { idle++ }
			command := "touch started; while ! test -f release; do sleep 0.02; done; printf 'acceptance result'; " + result
			bees, err := m.Dispatch(ctx, DispatchRequest{
				LeadThreadID: "lead-report-acceptance", LeadWorkDir: dir,
				Bees: []BeeSpec{
					{Name: "Aspen", Task: "修改 internal/feature.go", Acceptance: []string{command}},
					{Name: "Birch", Task: "修改 internal/other.go"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			bee := bees[0]
			writeBeeFile(t, bee, "internal/feature.go", "package feature\n")
			completeBee(t, m, bee, "完成")
			writeBeeFile(t, bees[1], "internal/other.go", "package feature\n")
			completeBee(t, m, bees[1], "完成")
			m.Tick(ctx)
			for {
				if _, err := os.Stat(filepath.Join(bee.WorktreePath, "started")); err == nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("acceptance did not start")
				case <-time.After(5 * time.Millisecond):
				}
			}
			for range 3 {
				m.Tick(ctx)
			}
			if h := beeByThread(t, m, "lead-report-acceptance", bee.ThreadID); len(reports) != 0 || idle != 0 || h.Reported {
				t.Fatalf("acceptance still running: reports=%v idle=%d reported=%v", reports, idle, h.Reported)
			}
			if h := beeByThread(t, m, "lead-report-acceptance", bees[1].ThreadID); !h.Merged || h.Reported {
				t.Fatalf("completed sibling must wait for acceptance: %+v", h)
			}
			if err := os.WriteFile(filepath.Join(bee.WorktreePath, "release"), nil, 0644); err != nil {
				t.Fatal(err)
			}
			m.wg.Wait()
			for range 3 {
				m.Tick(ctx)
			}
			if len(reports) != 1 || !beeByThread(t, m, "lead-report-acceptance", bee.ThreadID).Reported {
				t.Fatalf("expected one final report: %v", reports)
			}
			if !strings.Contains(reports[0], "Birch") || !beeByThread(t, m, "lead-report-acceptance", bees[1].ThreadID).Reported {
				t.Fatalf("missing sibling in batch report: %v", reports)
			}
			if result == "true" {
				if !strings.Contains(reports[0], "合并结果：已合入") {
					t.Fatalf("missing merge result: %s", reports[0])
				}
			} else if !strings.Contains(reports[0], "合并结果：失败") || !strings.Contains(reports[0], "验收命令失败："+command+"（退出码 1）") || !strings.Contains(reports[0], "acceptance result") {
				t.Fatalf("missing acceptance failure: %s", reports[0])
			}
		})
	}
}

func TestAcceptanceOffStoppedBeeDoesNotMerge(t *testing.T) {
	for _, action := range []string{"stop", "stop-all", "delete"} {
		for _, result := range []string{"true", "false"} {
			t.Run(action+"/"+result, func(t *testing.T) {
				m, dir := newFunnelManager(t, &funnelFake{})
				settings := m.Settings()
				settings.ReviewMode = "off"
				m.SetSettingsFunc(func() Settings { return settings })
				bees, err := m.Dispatch(context.Background(), DispatchRequest{
					LeadThreadID: "lead-stop-acceptance", LeadWorkDir: dir,
					Bees: []BeeSpec{{Name: "Aspen", Task: "修改 internal/feature.go", Acceptance: []string{"touch started; while ! test -f release; do sleep 0.02; done; " + result}}},
				})
				if err != nil {
					t.Fatal(err)
				}
				bee := bees[0]
				writeBeeFile(t, bee, "internal/feature.go", "package feature\n")
				completeBee(t, m, bee, "完成")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer func() { cancel(); m.wg.Wait() }()
				m.Tick(ctx)
				for {
					if _, err := os.Stat(filepath.Join(bee.WorktreePath, "started")); err == nil {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("acceptance did not start")
					case <-time.After(5 * time.Millisecond):
					}
				}
				if action == "stop" {
					if err := m.StopBee(ctx, "lead-stop-acceptance", bee.ThreadID); err != nil {
						t.Fatal(err)
					}
				} else if action == "stop-all" {
					if err := m.StopAll(ctx, "lead-stop-acceptance"); err != nil {
						t.Fatal(err)
					}
				} else {
					m.BeeThreadDeleted(ctx, bee.ThreadID)
				}
				before := beeByThread(t, m, "lead-stop-acceptance", bee.ThreadID)
				if before.AcceptanceStep != 0 || before.AcceptanceStartedAt != nil {
					t.Fatal("停止或删除后仍显示验收进度")
				}
				if err := os.WriteFile(filepath.Join(bee.WorktreePath, "release"), nil, 0644); err != nil {
					t.Fatal(err)
				}
				m.wg.Wait()
				h := beeByThread(t, m, "lead-stop-acceptance", bee.ThreadID)
				if !reflect.DeepEqual(before, h) || h.Merged || h.PendingMerge || (action != "delete" && h.Status != StatusStopped) {
					t.Fatalf("late result changed stopped/deleted bee: before=%+v after=%+v", before, h)
				}
				if _, err := os.Stat(filepath.Join(dir, "internal/feature.go")); !os.IsNotExist(err) {
					t.Fatalf("main copy changed: %v", err)
				}
			})
		}
	}
}

func TestAcceptanceHumanCannotApproveBeforeChecks(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	fake := &funnelFake{checkFn: func(ctx context.Context, _ string, _ []string, _ string, _ time.Duration) reviewcheck.Result {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return reviewcheck.Result{OK: true}
	}}
	m, dir := newFunnelManager(t, fake)
	settings := m.Settings()
	settings.ReviewMode = "human"
	m.SetSettingsFunc(func() Settings { return settings })
	bees, err := m.Dispatch(context.Background(), DispatchRequest{
		LeadThreadID: "lead-human-acceptance", LeadWorkDir: dir,
		Bees: []BeeSpec{{Name: "Aspen", Task: "修改 internal/feature.go", Acceptance: []string{"false"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeBeeFile(t, bees[0], "internal/feature.go", "package feature\n")
	completeBee(t, m, bees[0], "完成")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.Tick(ctx)
	defer func() { close(release); m.wg.Wait() }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("L0 did not start")
	}
	if _, err := m.ApproveMerge(ctx, "lead-human-acceptance", bees[0].ThreadID); err == nil {
		t.Fatal("approved before acceptance")
	}
	m.markReviewNeedsHuman("lead-human-acceptance", bees[0].ThreadID, "L0 检查超时")
	if _, err := m.ApproveMerge(ctx, "lead-human-acceptance", bees[0].ThreadID); err == nil {
		t.Fatal("timeout bypassed acceptance")
	}
}

func TestRewriteWorkDirBackslashBoundary(t *testing.T) {
	got := rewriteWorkDir(`cd /source\sub`, "/source", "/copy")
	if got != `cd /copy\sub` {
		t.Fatalf("rewrite = %q", got)
	}
}

func TestStopBeeCancelsAcceptance(t *testing.T) {
	for _, action := range []string{"stop", "stop-all", "delete"} {
		m := NewManager(Options{})
		dir := t.TempDir()
		secondFile := filepath.Join(dir, "second-ran")
		h := Bee{
			ThreadID:      "bee-stop-cancel-" + action,
			WorktreePath:  dir,
			ChangedFiles:  []string{"a.go"},
			Acceptance:    []string{"sleep 30", "touch '" + filepath.ToSlash(secondFile) + "'"},
			Status:        StatusCompleted,
			PendingMerge:  true,
			ReviewState:   ReviewRunning,
			ReviewStage:   ReviewStageL0,
			TargetWorkDir: dir,
		}
		leadID := "lead-stop-cancel-" + action
		m.leads[leadID] = &leadState{leadThreadID: leadID, leadWorkDir: dir, bees: []Bee{h}}

		started := make(chan struct{}, 1)
		m.SetOnChange(func(id string) {
			st := m.State(id)
			if len(st.Bees) > 0 && st.Bees[0].AcceptanceStep == 1 {
				select {
				case started <- struct{}{}:
				default:
				}
			}
		})

		type accResult struct {
			passed bool
			detail string
		}
		done := make(chan accResult, 1)
		go func() {
			passed, detail := m.runAcceptance(context.Background(), leadID, h)
			done <- accResult{passed: passed, detail: detail}
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: 验收命令未在 5 秒内启动", action)
		}

		switch action {
		case "stop":
			if err := m.StopBee(context.Background(), leadID, h.ThreadID); err != nil {
				t.Fatalf("StopBee 失败: %v", err)
			}
		case "stop-all":
			if err := m.StopAll(context.Background(), leadID); err != nil {
				t.Fatalf("StopAll 失败: %v", err)
			}
		case "delete":
			m.BeeThreadDeleted(context.Background(), h.ThreadID)
		}

		select {
		case got := <-done:
			if got.passed {
				t.Fatalf("%s: 被取消的验收不应通过: %+v", action, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: 取消后 runAcceptance 未在 5 秒内返回", action)
		}

		if _, err := os.Stat(secondFile); !os.IsNotExist(err) {
			t.Fatalf("%s: 被取消后不应执行第二条验收命令: %v", action, err)
		}
		after := m.State(leadID).Bees[0]
		if after.ReviewState != ReviewNone || after.PendingMerge || !after.MergeDiscarded || (action != "delete" && after.Status != StatusStopped) {
			t.Fatalf("%s: 停止后状态未按停止路径落定: %+v", action, after)
		}
	}
}

func TestAcceptanceProblem(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{name: "grep_short_q", command: "grep -q foo a.go", want: acceptanceErrGrepQuiet},
		{name: "grep_merged_qE", command: "grep -qE 'a|b' a.go", want: acceptanceErrGrepQuiet},
		{name: "grep_merged_Eq", command: "grep -Eq 'a|b' a.go", want: acceptanceErrGrepQuiet},
		{name: "grep_quiet", command: "grep --quiet foo a.go", want: acceptanceErrGrepQuiet},
		{name: "grep_silent", command: "grep --silent foo a.go", want: acceptanceErrGrepQuiet},
		{name: "grep_in_sh_c", command: `sh -c '! grep -q "x" a.go'`, want: acceptanceErrGrepQuiet},
		{name: "timeout_leading", command: "timeout 10 go test -run Foo ./p/", want: acceptanceErrTimeout},
		{name: "gtimeout_after_and", command: "cd sub && gtimeout 30 go test -run Foo ./p/", want: acceptanceErrTimeout},
		{name: "timeout_after_pipe", command: "cat a.go | timeout 5 grep -n foo", want: acceptanceErrTimeout},
		{name: "timeout_after_or", command: "false || timeout 5 true", want: acceptanceErrTimeout},
		{name: "timeout_after_semi", command: "true; timeout 5 true", want: acceptanceErrTimeout},
		{name: "go_test_no_run", command: "go test ./internal/ui/", want: acceptanceErrGoTestRun},
		{name: "go_test_timeout_no_run", command: "go test -count=1 -timeout 300s ./internal/ui/", want: acceptanceErrGoTestRun},
		{name: "go_test_empty_run", command: "go test -run= ./internal/ui/", want: acceptanceErrGoTestRun},
		{name: "ok_go_test_run_single_quote", command: "go test -count=1 -timeout 300s -run 'Foo' ./internal/ui/", want: ""},
		{name: "ok_go_test_run_or_pipe", command: "go test -count=1 -timeout 300s -run 'Foo|Bar' ./internal/ui/", want: ""},
		{name: "ok_go_test_run_eq", command: "go test -run=Foo ./internal/ui/", want: ""},
		{name: "ok_sh_grep_n", command: `sh -c '! grep -n "x" a.go'`, want: ""},
		{name: "ok_go_vet", command: "go vet ./...", want: ""},
		{name: "ok_cd_go_test_run", command: "cd /abs/path && go test -run X ./p/", want: ""},
		{name: "ok_grep_pattern_quiet", command: `grep -n "quiet" a.go`, want: ""},
		{name: "ok_grep_e_dash_q", command: `grep -e -q a.go`, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := acceptanceProblem(tc.command); got != tc.want {
				t.Fatalf("acceptanceProblem(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func TestAcceptanceLintRejectsEntryBeforeStart(t *testing.T) {
	m, dir := newFunnelManager(t, &funnelFake{})
	defer m.Stop()
	settings := m.Settings()
	settings.Enabled = true
	settings.AutoDelegate = true
	m.SetSettingsFunc(func() Settings { return settings })

	ctx := context.Background()
	badCmd := "go test ./internal/ui/"

	// 1. DispatchFromReply：同批坏条目不启动、不占花名册名额，正常条目拿到第一个名字照常启动。
	reply := "```swarm\n" +
		`[{"task":"坏任务","prompt":"修复坏任务","acceptance":["` + badCmd + `"]},` +
		`{"task":"好任务","prompt":"修复好任务","acceptance":["go test -run TestGood ./internal/swarm/"]}]` +
		"\n```"
	bees, notices, err := m.DispatchFromReply(ctx, ReplyDispatchRequest{
		LeadThreadID: "lintbatch",
		ProjectID:    "proj",
		LeadWorkDir:  dir,
		ReplyText:    reply,
	})
	if err != nil {
		t.Fatalf("batch DispatchFromReply unexpected error: %v", err)
	}
	if len(bees) != 1 || bees[0].Name != RosterName(0) || bees[0].Task != "好任务" {
		t.Fatalf("正常条目应拿到首个名字 %s 并启动，实际 bees=%+v", RosterName(0), bees)
	}
	if len(notices) == 0 || !strings.Contains(notices[0], "条目「坏任务」未启动") ||
		!strings.Contains(notices[0], badCmd) || !strings.Contains(notices[0], acceptanceErrGoTestRun) ||
		!strings.Contains(notices[0], "改正后重新派发") {
		t.Fatalf("拒绝提示格式不符: %v", notices)
	}
	if state := m.State("lintbatch"); len(state.Bees) != 1 {
		t.Fatalf("坏条目不应产生 bee 记录: %+v", state.Bees)
	}

	// 2. DispatchSingleEntry：仅坏条目时直接拒绝，不占名字、不建副本、不启动 bee。
	bee, singleNotices, err := m.DispatchSingleEntry(ctx, SingleEntryRequest{
		LeadThreadID: "lintsingle",
		ProjectID:    "proj",
		LeadWorkDir:  dir,
		Entry: Delegation{
			Prompt:     "只写了 prompt 没有 task 的条目用于检查三十字截断效果和后续说明",
			Acceptance: []string{"grep -q foo a.go"},
		},
	})
	if err == nil {
		t.Fatal("DispatchSingleEntry 应拒绝不合规验收命令")
	}
	if bee.ThreadID != "" || bee.WorktreePath != "" {
		t.Fatalf("被拒条目不应启动或建副本: %+v", bee)
	}
	if len(singleNotices) != 1 || !strings.Contains(singleNotices[0], "未启动") || !strings.Contains(singleNotices[0], acceptanceErrGrepQuiet) {
		t.Fatalf("singleNotices=%v", singleNotices)
	}
	if state := m.State("lintsingle"); len(state.Bees) != 0 {
		t.Fatalf("被拒条目不应写入 state.Bees: %+v", state.Bees)
	}
	// 改正后重新派发仍拿到首个花名册名字 Aspen。
	fixed, _, err := m.DispatchSingleEntry(ctx, SingleEntryRequest{
		LeadThreadID: "lintsingle",
		ProjectID:    "proj",
		LeadWorkDir:  dir,
		Entry: Delegation{
			Task:       "改正后的任务",
			Prompt:     "只写了 prompt 没有 task 的条目",
			Acceptance: []string{"go test -run TestFoo ./internal/swarm/"},
		},
	})
	if err != nil {
		t.Fatalf("改正后派发失败: %v", err)
	}
	if fixed.Name != RosterName(0) {
		t.Fatalf("被拒条目不应占用名字，改正后期望 %s，实际 %s", RosterName(0), fixed.Name)
	}
}

func TestAcceptanceNoTestsRunFails(t *testing.T) {
	m := NewManager(Options{})
	dir := t.TempDir()

	noTestsCmd := "printf 'ok  \\tmcode/x\\t0.1s [no tests to run]\\n' || go test -run TestMissing ./x"
	passed, detail := m.runAcceptance(context.Background(), "lead", Bee{
		WorktreePath: dir,
		ChangedFiles: []string{"a.go"},
		Acceptance:   []string{noTestsCmd},
	})
	if passed || !strings.Contains(detail, "没有执行任何测试") || !strings.Contains(detail, "[no tests to run]") {
		t.Fatalf("只有 [no tests to run] 时应判失败并附输出: passed=%v detail=%q", passed, detail)
	}

	noFilesCmd := "printf '?   \\tmcode/x\\t[no test files]\\n' || go test -run TestMissing ./x"
	passed, detail = m.runAcceptance(context.Background(), "lead", Bee{
		WorktreePath: dir,
		ChangedFiles: []string{"a.go"},
		Acceptance:   []string{noFilesCmd},
	})
	if passed || !strings.Contains(detail, "没有执行任何测试") {
		t.Fatalf("只有 [no test files] 时应判失败: passed=%v detail=%q", passed, detail)
	}

	mixedPassCmd := "printf 'ok  \\tmcode/x\\t0.1s [no tests to run]\\nok  \\tmcode/y\\t0.2s\\n' || go test -run TestReal ./..."
	passed, detail = m.runAcceptance(context.Background(), "lead", Bee{
		WorktreePath: dir,
		ChangedFiles: []string{"a.go"},
		Acceptance:   []string{mixedPassCmd},
	})
	if !passed || detail != "" {
		t.Fatalf("至少有一个包真正跑了测试时应判通过: passed=%v detail=%q", passed, detail)
	}
}

func TestAcceptancePassedMarkerHiddenFromReport(t *testing.T) {
	report := BuildReport([]Bee{{
		Name:           "Aspen",
		Task:           "改代码",
		Status:         StatusCompleted,
		Merged:         true,
		ReviewState:    ReviewApproved,
		ReviewLayer:    "l2",
		ReviewConcerns: []string{"验收命令通过：2 条", "internal/a.go:12 缺少错误处理"},
	}})
	if strings.Contains(report, "验收命令通过") {
		t.Fatalf("BuildReport 不应把验收通过闸门标记输出为疑点:\n%s", report)
	}
	if !strings.Contains(report, "审查疑点：internal/a.go:12 缺少错误处理") {
		t.Fatalf("BuildReport 应保留普通审查疑点:\n%s", report)
	}
}
