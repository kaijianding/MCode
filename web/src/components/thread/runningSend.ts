export function runningSendParams(isRunning: boolean, isStewardThread: boolean): { whenRunning?: 'queue' } {
  return isRunning && isStewardThread ? { whenRunning: 'queue' } : {};
}

export function runningSubmitLabel(isRunning: boolean, isStewardThread: boolean, insertIntoRunningTurn: boolean): '插入' | '排队' | '发送' {
  if (!isRunning || isStewardThread) return '发送';
  return insertIntoRunningTurn ? '插入' : '排队';
}
