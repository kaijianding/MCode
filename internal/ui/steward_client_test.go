package mcodeui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// stewardFakeClient 在内存假 Client 上补齐总管接口：记录创建次数，可配置返回的会话 ID 与错误，
// 并可用 gate 模拟首次创建耗时，用来检查防重入；昵称与头像也可配置，用来验证身份回落。
type stewardFakeClient struct {
	*FakeClient

	stewardMu sync.Mutex
	threadID  string        // 当前总管会话 ID，空串表示还没有
	openID    string        // OpenStewardThread 成功后返回并写入 threadID 的 ID
	openErr   error         // 非 nil 时创建返回该错误
	opens     int           // OpenStewardThread 被调用的次数
	resetID   string        // ResetStewardThread 成功后返回并写入 threadID 的 ID
	resetErr  error         // 非 nil 时重置返回该错误
	resets    int           // ResetStewardThread 被调用的次数
	gate      chan struct{} // 非 nil 时创建阻塞到它被关闭
	name      string        // StewardIdentity 返回的昵称原文，空串表示让界面回落「总管」
	avatar    string        // StewardIdentity 返回的头像字符，空串表示取 stewardName(nickname) 首字
}

var _ StewardClient = (*stewardFakeClient)(nil)

func newStewardFakeClient(threadID string) *stewardFakeClient {
	return &stewardFakeClient{FakeClient: NewFakeClient(), threadID: threadID, openID: "t1", resetID: stewardTestThreadID}
}

// stewardTestThreadID 是总管用例里用的会话 ID：故意和默认选中的 t1 区分开，
// 否则「跳转到总管」的断言在默认状态下就已经成立，测不出任何东西。
const stewardTestThreadID = "t2"

// addStewardTestThread 往假客户端里加一个比 t1 旧的会话，并把创建后的总管 ID 指向它。
// t1 更近更新，所以启动时默认选中的仍是 t1。
func addStewardTestThread(fake *stewardFakeClient) {
	now := time.Now()
	fake.SetProjectsAndThreads(
		[]Project{{ID: "p1", Name: "mcode-test", Path: "/path/to/test"}},
		map[string][]Thread{"p1": {
			{ID: "t1", ProjectID: "p1", Title: "测试会话1", UpdatedAt: now},
			{ID: stewardTestThreadID, ProjectID: "p1", Title: "测试会话2", UpdatedAt: now.Add(-time.Hour)},
		}},
	)
	fake.openID = stewardTestThreadID
}

func (f *stewardFakeClient) StewardThreadID() string {
	f.stewardMu.Lock()
	defer f.stewardMu.Unlock()
	return f.threadID
}

func (f *stewardFakeClient) OpenStewardThread(_ context.Context) (string, error) {
	f.stewardMu.Lock()
	f.opens++
	gate := f.gate
	f.stewardMu.Unlock()

	if gate != nil {
		<-gate
	}

	f.stewardMu.Lock()
	defer f.stewardMu.Unlock()
	if f.openErr != nil {
		return "", f.openErr
	}
	f.threadID = f.openID
	return f.openID, nil
}

func (f *stewardFakeClient) ResetStewardThread(_ context.Context) (string, error) {
	f.stewardMu.Lock()
	f.resets++
	if f.resetErr != nil {
		err := f.resetErr
		f.stewardMu.Unlock()
		return "", err
	}
	id := f.resetID
	f.threadID = id
	f.stewardMu.Unlock()
	return id, nil
}

func (f *stewardFakeClient) StewardIdentity() (string, string) {
	f.stewardMu.Lock()
	defer f.stewardMu.Unlock()
	return f.name, f.avatar
}

// openCalls 在锁内读取创建次数。
func (f *stewardFakeClient) openCalls() int {
	f.stewardMu.Lock()
	defer f.stewardMu.Unlock()
	return f.opens
}

func (f *stewardFakeClient) resetCalls() int {
	f.stewardMu.Lock()
	defer f.stewardMu.Unlock()
	return f.resets
}

// 场景 1：客户端没实现总管接口时不渲染总管段，侧栏其余内容照常。
func TestSidebarStewardEntryHiddenWithoutStewardClient(t *testing.T) {
	state, view := buildRoot(NewFakeClient())
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool { return tt.HasText("测试会话1") })
	if tt.HasText("总管") {
		t.Fatalf("未实现 StewardClient 时不应渲染总管段，当前文本: %q", tt.Texts())
	}
}

// 场景 2：总管会话还不存在时不渲染总管段，免得没启用的人多出一行。
func TestSidebarStewardEntryHiddenWithoutThread(t *testing.T) {
	state, view := buildRoot(newStewardFakeClient(""))
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool { return tt.HasText("测试会话1") })
	if tt.HasText("总管") {
		t.Fatalf("总管会话 ID 为空时不应渲染总管段，当前文本: %q", tt.Texts())
	}
}

// 场景 3：会话已存在时渲染合并行，点击后跳到该会话。
func TestSidebarStewardEntryClickRevealsThread(t *testing.T) {
	fake := newStewardFakeClient(stewardTestThreadID)
	addStewardTestThread(fake)
	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool {
		// 等总管段和默认选中都就绪，免得启动期的默认选中把后面的跳转盖掉。
		return tt.HasText("总管") && selectedThreadForTest(state) == "t1"
	})
	if err := tt.Click("总管"); err != nil {
		t.Fatalf("点击总管段失败: %v", err)
	}
	waitFor(t, tt, func() bool { return selectedThreadForTest(state) == stewardTestThreadID })
	if calls := fake.openCalls(); calls != 0 {
		t.Fatalf("会话已存在时不应创建，实际创建 %d 次", calls)
	}
}

// 场景 4：还没有会话时点击会在后台创建，创建完成后跳到新会话。
func TestStewardOpenCreatesThreadAndReveals(t *testing.T) {
	fake := newStewardFakeClient("")
	addStewardTestThread(fake)
	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	// 等启动期默认选中完成，免得默认选中 t1 盖掉后面的跳转。
	waitFor(t, tt, func() bool { return selectedThreadForTest(state) == "t1" })
	state.openStewardThread()

	waitFor(t, tt, func() bool { return selectedThreadForTest(state) == stewardTestThreadID })
	if calls := fake.openCalls(); calls != 1 {
		t.Fatalf("应只创建一次，实际创建 %d 次", calls)
	}
	// 创建完成后总管段出现。
	waitFor(t, tt, func() bool { return tt.HasText("总管") })
}

// 场景 5：创建失败时用 Toast 提示错误，不跳转。
func TestStewardOpenShowsError(t *testing.T) {
	fake := newStewardFakeClient("")
	fake.openErr = errors.New("总管未启用")
	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	// 等启动期默认选中完成，再记下当前会话，失败时比对有没有被改掉。
	waitFor(t, tt, func() bool { return selectedThreadForTest(state) == "t1" })
	before := selectedThreadForTest(state)
	state.openStewardThread()

	waitFor(t, tt, func() bool { return tt.HasText("总管未启用") })
	if selected := selectedThreadForTest(state); selected != before {
		t.Fatalf("创建失败时不应跳转，选中从 %q 变成 %q", before, selected)
	}
	if pending := takeStewardPendingThread(state); pending != "" {
		t.Fatalf("创建失败时不应登记待跳转，实际登记 %q", pending)
	}
}

// 场景 6：创建还没完成时再次点击不会发起第二次创建。
func TestStewardOpenIgnoresRepeatedClicks(t *testing.T) {
	fake := newStewardFakeClient("")
	addStewardTestThread(fake)
	gate := make(chan struct{})
	fake.gate = gate
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	// 断言提前失败时也要放行后台创建，避免 goroutine 一直挂住。
	t.Cleanup(release)

	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	tt := ui.NewTester(view, 1000, 700)

	// 等启动期默认选中完成，再点两次，避免默认选中与后续跳转互相覆盖。
	waitFor(t, tt, func() bool { return selectedThreadForTest(state) == "t1" })
	state.openStewardThread()
	state.openStewardThread()
	tt.Frame()

	release()
	waitFor(t, tt, func() bool { return selectedThreadForTest(state) == stewardTestThreadID })
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if calls := fake.openCalls(); calls != 1 {
		t.Fatalf("连续点击应只创建一次，实际创建 %d 次", calls)
	}
}

// 场景 7：侧栏折叠时后台创建完成也要跳转；跳转不能依赖总管段渲染。
func TestStewardOpenRevealsWhenSidebarCollapsed(t *testing.T) {
	fake := newStewardFakeClient("")
	addStewardTestThread(fake)
	state, view := buildRoot(fake)
	t.Cleanup(state.close)
	state.mu.Lock()
	state.sidebarCollapsed = true
	state.mu.Unlock()
	tt := ui.NewTester(view, 1000, 700)

	waitFor(t, tt, func() bool { return selectedThreadForTest(state) == "t1" })
	state.openStewardThread()

	// 折叠态下总管段不渲染，只有每帧的 consumeStewardPendingThread 能完成跳转。
	waitFor(t, tt, func() bool { return selectedThreadForTest(state) == stewardTestThreadID })
	state.mu.Lock()
	collapsed := state.sidebarCollapsed
	state.mu.Unlock()
	if !collapsed {
		t.Fatalf("跳转不应顺带展开侧栏")
	}
	if calls := fake.openCalls(); calls != 1 {
		t.Fatalf("应只创建一次，实际创建 %d 次", calls)
	}
}

// 客户端不实现启用接口时按未知处理，界面不凭空画未启用入口。
func TestStewardOptionalClientsAbsent(t *testing.T) {
	s := newChatState(newStewardFakeClient(""))
	if _, known := s.stewardEnabledState(); known {
		t.Fatal("客户端不报告启用状态时应为未知")
	}
}

type stewardCountingAsyncFakeClient struct {
	*stewardFakeClient

	mu sync.Mutex

	idCalls   int
	idNick    string
	idAvatar  string
	idGate    chan struct{}
	idStarted chan struct{}

	enCalls   int
	enVal     bool
	enGate    chan struct{}
	enStarted chan struct{}
}

func (f *stewardCountingAsyncFakeClient) StewardIdentity() (string, string) {
	f.mu.Lock()
	f.idCalls++
	gate := f.idGate
	started := f.idStarted
	f.mu.Unlock()

	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		<-gate
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	return f.idNick, f.idAvatar
}

func (f *stewardCountingAsyncFakeClient) StewardEnabled() bool {
	f.mu.Lock()
	f.enCalls++
	gate := f.enGate
	started := f.enStarted
	f.mu.Unlock()

	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		<-gate
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enVal
}

func (f *stewardCountingAsyncFakeClient) identityCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.idCalls
}

func (f *stewardCountingAsyncFakeClient) enabledCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enCalls
}

func waitChanWithTimeout(t *testing.T, ch <-chan struct{}, desc string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
		t.Fatalf("等待 %s 超时", desc)
	}
}

func pollCondition(t *testing.T, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("轮询 %s 超时", desc)
}

func TestUIReview1StewardIdentityAsyncCache(t *testing.T) {
	gate1 := make(chan struct{})
	started1 := make(chan struct{}, 4)
	var release1 sync.Once
	t.Cleanup(func() { release1.Do(func() { close(gate1) }) })

	fake := &stewardCountingAsyncFakeClient{
		stewardFakeClient: newStewardFakeClient(""),
		idNick:            "阿默",
		idAvatar:          "✦",
		idGate:            gate1,
		idStarted:         started1,
	}
	s := newChatState(fake)
	t.Cleanup(s.close)

	// 同步路径不得阻塞或直接调 client；刷新期间连续多次调用只起一个 goroutine。
	for i := 0; i < 5; i++ {
		nick, av := s.stewardIdentity()
		if nick != "" || av != "总" {
			t.Fatalf("首次未命中缓存应返回默认身份 (\"\", \"总\")，第 %d 次得到 (%q, %q)", i, nick, av)
		}
	}
	waitChanWithTimeout(t, started1, "首次 StewardIdentity 后台拉取")
	if calls := fake.identityCalls(); calls != 1 {
		t.Fatalf("刷新期间应只起 1 个 goroutine，实际调用 %d 次", calls)
	}

	release1.Do(func() { close(gate1) })
	pollCondition(t, func() bool {
		nick, av := s.stewardIdentity()
		return nick == "阿默" && av == "✦"
	}, "identity 异步刷新返回新值")

	// 缓存有效期内再次调用不触发新的后台拉取。
	for i := 0; i < 3; i++ {
		nick, av := s.stewardIdentity()
		if nick != "阿默" || av != "✦" {
			t.Fatalf("缓存命中应返回 (\"阿默\", \"✦\")，得到 (%q, %q)", nick, av)
		}
	}
	if calls := fake.identityCalls(); calls != 1 {
		t.Fatalf("缓存有效期内不应再次调用 client，实际 %d 次", calls)
	}

	// 模拟 TTL 过期：同步仍返回旧缓存，并异步刷新成新身份。
	gate2 := make(chan struct{})
	started2 := make(chan struct{}, 4)
	var release2 sync.Once
	t.Cleanup(func() { release2.Do(func() { close(gate2) }) })

	fake.mu.Lock()
	fake.idNick = "新总管"
	fake.idAvatar = ""
	fake.idGate = gate2
	fake.idStarted = started2
	fake.mu.Unlock()

	st := getStewardOpenState(s)
	st.mu.Lock()
	st.identityAt = time.Now().Add(-stewardEnabledCacheTTL - time.Second)
	st.mu.Unlock()

	nick, av := s.stewardIdentity()
	if nick != "阿默" || av != "✦" {
		t.Fatalf("缓存过期同步返回应仍为旧值 (\"阿默\", \"✦\")，得到 (%q, %q)", nick, av)
	}
	waitChanWithTimeout(t, started2, "过期后 StewardIdentity 异步刷新")
	if calls := fake.identityCalls(); calls != 2 {
		t.Fatalf("过期后刷新应只发起第 2 次调用，实际 %d 次", calls)
	}

	release2.Do(func() { close(gate2) })
	pollCondition(t, func() bool {
		n, a := s.stewardIdentity()
		return n == "新总管" && a == "新"
	}, "identity 过期后异步刷新成新值")

	var nilState *chatState
	if n, a := nilState.stewardIdentity(); n != "" || a != "总" {
		t.Fatalf("nil chatState 应返回默认值，得到 (%q, %q)", n, a)
	}
}

func TestUIReview1StewardEnabledAsyncCache(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 4)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(gate) }) })

	fake := &stewardCountingAsyncFakeClient{
		stewardFakeClient: newStewardFakeClient(""),
		enVal:             true,
		enGate:            gate,
		enStarted:         started,
	}
	s := newChatState(fake)
	t.Cleanup(s.close)

	// 从未取过时返回 known=false，不阻塞在 gate 上，且只起一个 goroutine。
	for i := 0; i < 4; i++ {
		if en, known := s.stewardEnabledState(); known || en {
			t.Fatalf("未完成首次刷新时应返回 (false, false)，得到 (%v, %v)", en, known)
		}
	}
	waitChanWithTimeout(t, started, "首次 StewardEnabled 后台拉取")
	if calls := fake.enabledCalls(); calls != 1 {
		t.Fatalf("刷新期间应只起 1 个 goroutine，实际 %d 次", calls)
	}

	release.Do(func() { close(gate) })
	pollCondition(t, func() bool {
		en, known := s.stewardEnabledState()
		return known && en
	}, "enabled 首次异步刷新完成")

	// invalidateStewardEnabledState 清时间戳但保留旧值，并触发下一次异步刷新。
	fake.mu.Lock()
	fake.enVal = false
	fake.enGate = nil
	fake.enStarted = nil
	fake.mu.Unlock()

	invalidateStewardEnabledState(s)
	if en, known := s.stewardEnabledState(); !known || !en {
		t.Fatalf("invalidate 后同步返回应保留旧值 (true, true)，得到 (%v, %v)", en, known)
	}
	pollCondition(t, func() bool {
		en, known := s.stewardEnabledState()
		return known && !en
	}, "invalidate 后异步刷新成新值")
}
