const assert = { equal: (a: unknown, b: unknown) => { if (a !== b) throw new Error(`${a} !== ${b}`); }, deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { nextStableLabel } from './useStableLabel';
import { previewLines, previewToolOutput } from './previewLines';
import { DIFF_PREVIEW_LINES } from './ToolEntryDiff';

const initial = { label: 'A', since: 100 };
assert.deepEqual(nextStableLabel(initial, 'B', 250), { state: { label: 'B', since: 250 }, delay: 0 });
assert.deepEqual(nextStableLabel(initial, 'B', 200), { state: initial, delay: 50 });
const pending = nextStableLabel(initial, 'B', 200);
assert.deepEqual(nextStableLabel(pending.state, 'C', 250).state, { label: 'C', since: 250 });
for (const count of [9, 10]) {
  const result = previewLines(Array.from({ length: count }, (_, i) => i), DIFF_PREVIEW_LINES, false);
  assert.equal(result.hidden, count - 9);
  assert.equal(result.lines.length, 9);
}
for (const count of [8, 9]) {
  const text = Array.from({ length: count }, (_, i) => String(i)).join('\n');
  assert.equal(previewToolOutput(text, false).hidden, count - 8);
  assert.equal(previewToolOutput(text, true).text, text);
}
