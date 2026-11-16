export function questionShortcut(
  event: Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey'>,
  composing: boolean,
  editable: boolean,
  optionCount: number
): number | null {
  if (event.ctrlKey || event.metaKey || event.altKey || composing || editable || !/^[1-9]$/.test(event.key)) return null;
  const index = Number(event.key) - 1;
  return index < optionCount ? index : null;
}
