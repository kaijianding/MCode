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
