export function toolFilePath(_toolName: string | undefined, toolInput: unknown): string | null {
  let input = toolInput;
  if (typeof input === 'string') {
    try { input = JSON.parse(input); } catch { return null; }
  }
  if (!input || typeof input !== 'object' || Array.isArray(input)) return null;
  const record = input as Record<string, unknown>;
  for (const key of ['file_path', 'filePath', 'path', 'notebook_path']) {
    const value = record[key];
    if (typeof value === 'string' && value.trim()) return value;
  }
  return null;
}
