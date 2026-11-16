export function pushRecentProject(ids: string[], id: string, max = 20): string[] {
  return [...new Set([id, ...ids])].slice(0, max);
}

export function orderProjectsByRecent<T extends { id: string }>(projects: T[], recentIDs: string[]): T[] {
  const ranks = new Map<string, number>();
  recentIDs.forEach((id, index) => { if (!ranks.has(id)) ranks.set(id, index); });
  return [...projects].sort((a, b) => (ranks.get(a.id) ?? Infinity) - (ranks.get(b.id) ?? Infinity));
}
