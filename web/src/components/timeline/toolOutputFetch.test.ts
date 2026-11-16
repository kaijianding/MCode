import { fetchToolOutput, needsToolOutputFetch } from './toolOutputFetch';

const assert = { equal: (actual: unknown, expected: unknown) => { if (actual !== expected) throw new Error(`${actual} !== ${expected}`); } };
assert.equal(needsToolOutputFetch(3, '界'), false);
assert.equal(needsToolOutputFetch(4, '界'), true);
assert.equal(needsToolOutputFetch(undefined, 'text'), false);
assert.equal(needsToolOutputFetch(0, ''), false);

const offsets: number[] = [];
const full = await fetchToolOutput(async (params) => {
  assert.equal(params.threadID, 'thread');
  assert.equal(params.messageID, 'message');
  assert.equal(params.length, 524288);
  offsets.push(params.offset ?? 0);
  return params.offset === 0
    ? { text: '界', offset: 0, totalBytes: 7, eof: false }
    : { text: 'a界', offset: 3, totalBytes: 7, eof: true };
}, 'thread', 'message');
assert.equal(full, '界a界');
assert.equal(offsets.join(','), '0,3');

let attempts = 0;
const retryRead = async () => {
  attempts++;
  if (attempts === 1) throw new Error('read failed');
  return { text: '完整输出', offset: 0, totalBytes: 12, eof: true };
};
let rejected = false;
try { await fetchToolOutput(retryRead, 'thread', 'message'); } catch { rejected = true; }
assert.equal(rejected, true);
assert.equal(await fetchToolOutput(retryRead, 'thread', 'message'), '完整输出');

rejected = false;
try {
  await fetchToolOutput(async () => ({ text: '', offset: 0, totalBytes: 3, eof: false }), 'thread', 'message');
} catch { rejected = true; }
assert.equal(rejected, true);
