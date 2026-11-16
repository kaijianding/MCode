export function runningSendParams(isRunning: boolean, isStewardThread: boolean): { whenRunning?: 'queue' } {
  return isRunning && isStewardThread ? { whenRunning: 'queue' } : {};
}
