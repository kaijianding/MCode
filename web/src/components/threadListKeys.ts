export function threadListTargetIndex(key: string, index: number, count: number): number | null {
  if (count === 0 || index < 0 || index >= count) return null;
  switch (key) {
    case 'ArrowDown': return Math.min(index + 1, count - 1);
    case 'ArrowUp': return Math.max(index - 1, 0);
    case 'Home': return 0;
    case 'End': return count - 1;
    default: return null;
  }
}
