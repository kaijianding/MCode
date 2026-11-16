import { useEffect, useRef, useState } from 'react';

export interface StableLabelState { label: string; since: number }

export function nextStableLabel(state: StableLabelState, incoming: string, nowMs: number, minDwellMs = 150): { state: StableLabelState; delay: number } {
  if (incoming === state.label) return { state, delay: 0 };
  const delay = Math.max(0, state.since + minDwellMs - nowMs);
  return delay ? { state, delay } : { state: { label: incoming, since: nowMs }, delay: 0 };
}

export function useStableLabel(label: string, minDwellMs = 150): string {
  const [state, setState] = useState<StableLabelState>(() => ({ label, since: Date.now() }));
  const latest = useRef(label);
  latest.current = label;
  useEffect(() => {
    const next = nextStableLabel(state, label, Date.now(), minDwellMs);
    if (next.state !== state) setState(next.state);
    if (next.delay) {
      const timer = setTimeout(() => setState(current => nextStableLabel(current, latest.current, Date.now(), minDwellMs).state), next.delay);
      return () => clearTimeout(timer);
    }
  }, [label, minDwellMs, state]);
  return state.label;
}
