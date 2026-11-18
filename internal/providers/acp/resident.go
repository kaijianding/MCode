package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"mcode/internal/harness"
	"mcode/internal/procutil"
)

var backgroundTaskPattern = regexp.MustCompile(`Tool is running as a background task with task id: ([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/task-\d+)`)
var backgroundTaskEndPattern = regexp.MustCompile(`Task id "([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/task-\d+)" (finished|was canceled) with result:`)

type acpProcess struct {
	cmd     *exec.Cmd
	conn    *Conn
	stderr  *procutil.TailBuffer
	args    []string
	cancel  context.CancelFunc
	cleanup func()

	initialized    bool
	initResp       acpInitializeResult
	sessionID      string
	sessionOptions []map[string]any
	sessionModes   acpSessionModes
	waited         bool
	waitErr        error
	done           chan struct{}
	resident       bool
	watchOnce      sync.Once

	// 以下状态由 Harness.residentMu 保护；只有执行本轮的协程访问上面的会话配置。
	busy         bool
	keep         bool
	expectedExit bool
	dead         bool
	tasks        map[string]string
	lastUpdate   time.Time
}

func (p *acpProcess) wait() error {
	if p.resident {
		<-p.done
		return p.waitErr
	}
	p.waitErr = p.cmd.Wait()
	p.waited = true
	return p.waitErr
}

func (h *Harness) SetUnsolicitedTurnHandler(fn func(string)) {
	h.residentMu.Lock()
	h.unsolicitedHandler = fn
	h.residentMu.Unlock()
}

func (h *Harness) SetProcessExitHandler(fn func(string, time.Time)) {
	h.residentMu.Lock()
	h.processExitHandler = fn
	h.residentMu.Unlock()
}

func (h *Harness) HasUnsolicitedOutput(threadID string) bool {
	h.residentMu.Lock()
	defer h.residentMu.Unlock()
	return len(h.finishedTasks[threadID]) > 0
}

// HasResidentProcess 供 agy 在固定账号的进程上禁用换号重试。
func (h *Harness) HasResidentProcess(threadID string) bool {
	h.residentMu.Lock()
	defer h.residentMu.Unlock()
	p := h.residents[threadID]
	return p != nil && !p.dead
}

// RunResident 原子地接管已有进程；返回 false 时调用方才需要为新进程选择账号。
func (h *Harness) RunResident(ctx context.Context, turn harness.Turn, emit func(harness.Event)) (bool, error) {
	if !h.opts.HoldBackgroundTasks {
		return false, nil
	}
	return h.runHeld(ctx, turn, emit, true)
}

func (h *Harness) runHeld(ctx context.Context, turn harness.Turn, emit func(harness.Event), residentOnly bool) (bool, error) {
	// 先拿启动锁、再拿表锁；所有等待、IO、回调均在释放表锁后执行。
	h.residentStartMu.Lock()
	h.residentMu.Lock()
	p := h.residents[turn.ThreadID]
	if p == nil && residentOnly {
		h.residentMu.Unlock()
		h.residentStartMu.Unlock()
		return false, nil
	}
	if turn.Attach && len(h.finishedTasks[turn.ThreadID]) == 0 {
		h.residentMu.Unlock()
		h.residentStartMu.Unlock()
		return true, nil
	}
	if p != nil && p.busy {
		h.residentMu.Unlock()
		h.residentStartMu.Unlock()
		return true, errors.New("本线程的 ACP 进程正在处理另一轮")
	}
	var finished []harness.Event
	if turn.Attach {
		finished = h.finishedTasks[turn.ThreadID]
		delete(h.finishedTasks, turn.ThreadID)
		delete(h.unsolicitedNotified, turn.ThreadID)
		turn.Prompt = "后台任务已结束，请根据结果继续。"
		turn.Images = nil
	}
	if p != nil {
		p.busy, p.keep = true, false
	}
	h.residentMu.Unlock()
	if p == nil {
		var err error
		p, err = h.startProcess(ctx, turn)
		if err != nil {
			h.residentMu.Lock()
			if len(finished) > 0 {
				h.finishedTasks[turn.ThreadID] = append(finished, h.finishedTasks[turn.ThreadID]...)
				delete(h.unsolicitedNotified, turn.ThreadID)
			}
			h.residentMu.Unlock()
			h.residentStartMu.Unlock()
			return true, err
		}
		h.residentMu.Lock()
		if h.residents == nil {
			h.residents = make(map[string]*acpProcess)
		}
		p.busy = true
		h.residents[turn.ThreadID] = p
		h.residentMu.Unlock()
		go h.waitResident(turn.ThreadID, p)
	}
	h.residentStartMu.Unlock()
	for _, event := range finished {
		_ = harness.Emit(emit, event)
	}

	err := h.runRound(ctx, turn, emit, p)
	h.residentMu.Lock()
	keep := err == nil && ctx.Err() == nil && p.keep && !p.dead
	if keep {
		p.busy = false
	} else {
		p.expectedExit = true
	}
	h.residentMu.Unlock()
	if !keep {
		if err != nil && p.sessionID != "" {
			_ = p.conn.Notify("session/cancel", map[string]any{"sessionId": p.sessionID})
		}
		_ = p.conn.Close()
		_ = killProcessGroup(p.cmd, syscall.SIGKILL)
		<-p.done
	} else {
		p.watchOnce.Do(func() { go h.watchBackgroundTasks(turn.ThreadID, p) })
	}
	return true, err
}

func (h *Harness) waitResident(threadID string, p *acpProcess) {
	p.waitErr = p.cmd.Wait()
	// 进程已退出，组内不再保留无人管理的后台命令；然后等读循环排空末尾响应。
	_ = killProcessGroup(p.cmd, syscall.SIGKILL)
	<-p.conn.closed
	_ = p.conn.Close()
	p.cancel()
	if p.cleanup != nil {
		p.cleanup()
	}
	h.roots.Clear(threadID, p.cmd.Process.Pid)
	h.residentMu.Lock()
	p.dead = true
	if h.residents[threadID] == p {
		delete(h.residents, threadID)
	}
	fn := h.processExitHandler
	notifyExit := !p.expectedExit
	for _, status := range p.tasks {
		if status == "" {
			notifyExit = true
		}
	}
	h.residentMu.Unlock()
	close(p.done)
	if notifyExit && fn != nil {
		fn(threadID, time.Time{})
	}
}

func (h *Harness) EvictThread(threadID string) {
	h.residentMu.Lock()
	p := h.residents[threadID]
	if p != nil && p.busy {
		h.residentMu.Unlock()
		return
	}
	delete(h.finishedTasks, threadID)
	delete(h.unsolicitedNotified, threadID)
	if p != nil {
		p.expectedExit = true
		delete(h.residents, threadID)
	}
	h.residentMu.Unlock()
	if p != nil {
		_ = p.conn.Close()
		_ = killProcessGroup(p.cmd, syscall.SIGKILL)
		<-p.done
	}
}

// roundHandlers 的门闩先于工具锁、表锁；表锁内不会获取门闩。
// 轮次结束后，已经被 Conn 取走的旧回调也不能再触碰旧 emit、Approver 或 ctx。
func (h *Harness) roundHandlers(p *acpProcess) (func(RequestHandler), func(NotificationHandler), func()) {
	if !h.opts.HoldBackgroundTasks {
		return p.conn.SetRequestHandler, p.conn.SetNotificationHandler, func() {}
	}
	var mu sync.RWMutex
	var detachOnce sync.Once
	active := true
	touch := func(method string) {
		if method == "session/update" {
			h.residentMu.Lock()
			p.lastUpdate = time.Now()
			h.residentMu.Unlock()
		}
	}
	request := func(fn RequestHandler) {
		p.conn.SetRequestHandler(func(method string, params json.RawMessage) (any, error) {
			mu.RLock()
			defer mu.RUnlock()
			if !active {
				return nil, errors.New("ACP 轮次已结束")
			}
			return fn(method, params)
		})
	}
	notification := func(fn NotificationHandler) {
		p.conn.SetNotificationHandler(func(method string, params json.RawMessage) {
			touch(method)
			mu.RLock()
			defer mu.RUnlock()
			if active {
				fn(method, params)
			}
		})
	}
	return request, notification, func() {
		detachOnce.Do(func() {
			mu.Lock()
			active = false
			mu.Unlock()
			p.conn.SetRequestHandler(nil)
			p.conn.SetNotificationHandler(func(method string, _ json.RawMessage) { touch(method) })
		})
	}
}

func (h *Harness) backgroundTaskStarts(p *acpProcess, toolID string, outputs []string, emit func(harness.Event)) {
	for _, output := range outputs {
		for _, match := range backgroundTaskPattern.FindAllStringSubmatch(output, -1) {
			taskID := match[1]
			h.residentMu.Lock()
			_, seen := p.tasks[taskID]
			if !seen {
				p.tasks[taskID] = ""
			}
			h.residentMu.Unlock()
			if !seen {
				_ = harness.Emit(emit, harness.Event{Type: harness.EventBackgroundTaskStart, ToolID: toolID, TaskID: taskID})
			}
		}
	}
}

func (h *Harness) holdProcess(p *acpProcess, threadID string) bool {
	h.residentMu.Lock()
	defer h.residentMu.Unlock()
	p.keep = len(h.finishedTasks[threadID]) > 0
	for _, status := range p.tasks {
		if status == "" {
			p.keep = true
		}
	}
	return p.keep
}

// agyTranscriptPath 返回 agy 为 sid 写下的 transcript 路径；两个已知目录都不存在时为空。
func agyTranscriptPath(p *acpProcess, sid string) string {
	if !agySessionIDPattern.MatchString(sid) {
		return ""
	}
	geminiDir := agyGeminiDir(p.args, p.cmd.Env)
	for _, dir := range []string{"antigravity-cli", "antigravity-acp"} {
		candidate := filepath.Join(geminiDir, dir, "brain", sid, ".system_generated", "logs", "transcript.jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// reconcileRoundTasks 把本轮 prompt 返回前已经结束的后台任务直接记成完成：
// holdProcess 不再为它们保留进程，上层也不会为它们补开一个 Attach 轮。
func (h *Harness) reconcileRoundTasks(p *acpProcess, threadID string, emit func(harness.Event)) {
	h.residentMu.Lock()
	sid := p.sessionID
	h.residentMu.Unlock()
	path := agyTranscriptPath(p, sid)
	statuses := make(map[string]string)
	if path != "" {
		completions, _ := readBackgroundCompletions(path, 0)
		statuses = make(map[string]string, len(completions))
		for _, event := range completions {
			if _, exists := statuses[event.TaskID]; !exists {
				statuses[event.TaskID] = event.TaskStatus
			}
		}
	}
	var ended []harness.Event
	h.residentMu.Lock()
	for taskID, status := range p.tasks {
		if status != "" {
			continue
		}
		finished, exists := statuses[taskID]
		if !exists {
			continue
		}
		p.tasks[taskID] = finished
		ended = append(ended, harness.Event{Type: harness.EventBackgroundTaskEnd, TaskID: taskID, TaskStatus: finished})
	}
	// 队列里的完成行已经由 agy 注入本轮对话，轮末一并交给本轮，避免多余的续轮。
	done := make(map[string]bool, len(ended))
	for _, event := range ended {
		done[event.TaskID] = true
	}
	for _, event := range h.finishedTasks[threadID] {
		if !done[event.TaskID] {
			done[event.TaskID] = true
			ended = append(ended, event)
		}
	}
	delete(h.finishedTasks, threadID)
	delete(h.unsolicitedNotified, threadID)
	h.residentMu.Unlock()
	for _, event := range ended {
		_ = harness.Emit(emit, event)
	}
}

func (h *Harness) watchBackgroundTasks(threadID string, p *acpProcess) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	path := ""
	var offset int64
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
		}
		h.residentMu.Lock()
		sid, dead := p.sessionID, p.dead
		h.residentMu.Unlock()
		if dead {
			return
		}
		if candidate := agyTranscriptPath(p, sid); candidate != "" && candidate != path {
			path, offset = candidate, 0
		}
		var ended []harness.Event
		if path != "" {
			ended, offset = readBackgroundCompletions(path, offset)
		}
		h.residentMu.Lock()
		if p.dead || h.residents[threadID] != p {
			h.residentMu.Unlock()
			return
		}
		for _, event := range ended {
			if status, exists := p.tasks[event.TaskID]; exists && status == "" {
				p.tasks[event.TaskID] = event.TaskStatus
				if h.finishedTasks == nil {
					h.finishedTasks = make(map[string][]harness.Event)
				}
				h.finishedTasks[threadID] = append(h.finishedTasks[threadID], event)
			}
		}
		fn := h.unsolicitedHandler
		notify := !p.dead && !p.busy && len(h.finishedTasks[threadID]) > 0 && !h.unsolicitedNotified[threadID] && time.Since(p.lastUpdate) >= 3*time.Second && fn != nil
		if notify {
			if h.unsolicitedNotified == nil {
				h.unsolicitedNotified = make(map[string]bool)
			}
			h.unsolicitedNotified[threadID] = true
		}
		h.residentMu.Unlock()
		if notify {
			go fn(threadID)
		}
	}
}

func readBackgroundCompletions(path string, offset int64) ([]harness.Event, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset
	}
	reader := bufio.NewReader(f)
	var events []harness.Event
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// 未写完的一行留到下次，不能吞掉完成通知。
			return events, offset
		}
		offset += int64(len(line))
		var step struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(line, &step) != nil {
			continue
		}
		for _, match := range backgroundTaskEndPattern.FindAllStringSubmatch(step.Content, -1) {
			status := "completed"
			if strings.HasPrefix(match[2], "was") {
				status = "canceled"
			}
			events = append(events, harness.Event{Type: harness.EventBackgroundTaskEnd, TaskID: match[1], TaskStatus: status})
		}
	}
}
