export const STOP_SEQUENCE_MS = 500;

export function nextStopPress(prev: number | null, now: number): { fire: boolean; last: number | null } {
  if (prev !== null && now - prev <= STOP_SEQUENCE_MS) return { fire: true, last: null };
  return { fire: false, last: now };
}
