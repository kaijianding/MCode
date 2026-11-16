interface ThreadListWindow {
  activeLimit: number;
  dormantExpanded: boolean;
  dormantLimit: number;
}

const defaultWindow: ThreadListWindow = { activeLimit: 5, dormantExpanded: false, dormantLimit: 10 };
// 移动端进入会话会卸载列表，返回时要先恢复窗口才能恢复滚动位置。
const windows = new Map<string, ThreadListWindow>();

export function recallThreadListWindow(key: string): ThreadListWindow {
  return windows.get(key) ?? defaultWindow;
}

export function rememberThreadListWindow(key: string, patch: Partial<ThreadListWindow>): void {
  windows.set(key, { ...recallThreadListWindow(key), ...patch });
}
