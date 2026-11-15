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
