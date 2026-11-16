export const TOOL_OUTPUT_PREVIEW_LINES = 8;

export function previewLines<T>(lines: T[], limit: number, expanded: boolean): { lines: T[]; hidden: number } {
  return { lines: expanded ? lines : lines.slice(0, limit), hidden: Math.max(0, lines.length - limit) };
}

export function previewToolOutput(text: string, expanded: boolean): { text: string; hidden: number } {
  const lines = text.split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  const preview = previewLines(lines, TOOL_OUTPUT_PREVIEW_LINES, expanded);
  const head = Math.ceil(TOOL_OUTPUT_PREVIEW_LINES / 2);
  return { text: expanded || !preview.hidden ? text : [...lines.slice(0, head), '…', ...lines.slice(-(TOOL_OUTPUT_PREVIEW_LINES - head))].join('\n'), hidden: preview.hidden };
}
