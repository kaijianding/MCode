package accounts

import (
	"reflect"
	"testing"
	"time"

	"mcode/internal/providers/multiaccount"
)

func TestModelsEnv(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name      string
		defaultID string
		accounts  []Account
		overrides map[string]AccountRoutingOverride
		wantID    string
	}{
		{name: "优先默认账号", defaultID: "b", accounts: []Account{{ID: "a", On: true, Status: StatusActive}, {ID: "b", On: true, Status: StatusActive}}, wantID: "b"},
		{name: "默认账号停用", defaultID: "a", accounts: []Account{{ID: "a", On: false, Status: StatusActive}, {ID: "b", On: true, Status: StatusActive}}, wantID: "b"},
		{name: "覆盖停用默认账号", defaultID: "a", accounts: []Account{{ID: "a", On: true, Status: StatusActive}, {ID: "b", On: true, Status: StatusActive}}, overrides: map[string]AccountRoutingOverride{"a": {On: &off}}, wantID: "b"},
		{name: "覆盖启用默认账号", defaultID: "b", accounts: []Account{{ID: "a", On: true, Status: StatusActive}, {ID: "b", Status: StatusActive}}, overrides: map[string]AccountRoutingOverride{"b": {On: &on}}, wantID: "b"},
		{name: "默认账号非active", defaultID: "a", accounts: []Account{{ID: "a", On: true, Status: StatusInvalid}, {ID: "b", On: true, Status: StatusActive}}, wantID: "b"},
		{name: "空账号列表"},
		{name: "没有可用账号", accounts: []Account{{ID: "a", On: false, Status: StatusActive}, {ID: "b", On: true, Status: StatusDisabled}}},
		{name: "系统账号", defaultID: SystemAccountID, accounts: []Account{{ID: SystemAccountID, On: true, Status: StatusActive}}, wantID: SystemAccountID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{opts: Options{DataDir: t.TempDir()}, index: Index{DefaultAccountID: tc.defaultID, Accounts: tc.accounts}, routing: RoutingStore{Accounts: tc.overrides}}
			env, args, err := m.ModelsEnv()
			if tc.wantID == "" {
				if err == nil || env != nil || args != nil {
					t.Fatalf("无账号应报错: env=%v args=%v err=%v", env, args, err)
				}
				return
			}
			_, wantEnv, wantArgs, _ := m.buildRuntimeEnv(tc.wantID)
			if err != nil || !reflect.DeepEqual(env, wantEnv) || !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("账号环境 = %v %v %v，期望 %v %v", env, args, err, wantEnv, wantArgs)
			}
		})
	}
}

func TestModelsEnvDoesNotChangeRouting(t *testing.T) {
	for _, strategy := range []multiaccount.Strategy{multiaccount.StrategyRotate, multiaccount.StrategyWeight} {
		t.Run(string(strategy), func(t *testing.T) {
			m := &Manager{
				opts: Options{DataDir: t.TempDir(), Now: time.Now},
				index: Index{Accounts: []Account{
					{ID: "a", Status: StatusActive, On: true, Weight: 100},
					{ID: "b", Status: StatusActive, On: true, Weight: 100},
				}},
				routing: RoutingStore{Config: multiaccount.Config{Strategy: strategy, Affinity: multiaccount.AffinitySession}},
			}
			persisted := false
			m.router = multiaccount.NewRouter(map[string]string{"existing": "b"}, func(map[string]string) error { persisted = true; return nil })
			control := multiaccount.NewRouter(nil, nil)
			for range 4 {
				for range 3 {
					if _, _, err := m.ModelsEnv(); err != nil {
						t.Fatal(err)
					}
				}
				states, _ := m.accountStatesLocked("gemini", time.Now())
				want, _ := control.Pick(m.routing.Config, "", states, nil, time.Now())
				got, _, _, err := m.Route("", "", nil)
				if err != nil || got != want {
					t.Fatalf("查询扰动选号: got=%q want=%q err=%v", got, want, err)
				}
			}
			if bound, ok := m.router.Bound("existing"); !ok || bound != "b" || persisted {
				t.Fatalf("查询改动绑定或持久化: %q %v %v", bound, ok, persisted)
			}
		})
	}
}
