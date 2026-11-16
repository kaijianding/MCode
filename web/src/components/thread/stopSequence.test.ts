const assert = { equal: (a: unknown, b: unknown) => { if (a !== b) throw new Error(`${a} !== ${b}`); }, deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { nextStopPress } from './stopSequence';

const first = nextStopPress(null, 100);
assert.deepEqual(first, { fire: false, last: 100 });
const second = nextStopPress(first.last, 500);
assert.deepEqual(second, { fire: true, last: null });
assert.deepEqual(nextStopPress(first.last, 700), { fire: false, last: 700 });
assert.deepEqual(nextStopPress(second.last, 600), { fire: false, last: 600 });
