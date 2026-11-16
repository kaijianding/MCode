export function undoKind(notice: { undoToken?: string; archivedThread?: unknown } | null): 'bulk' | 'archive' | null {
  if (notice?.archivedThread) return 'archive';
  if (notice?.undoToken) return 'bulk';
  return null;
}
