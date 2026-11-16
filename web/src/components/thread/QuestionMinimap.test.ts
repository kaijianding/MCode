const assert = { equal: (a: unknown, b: unknown) => { if (a !== b) throw new Error(`${a} !== ${b}`); }, deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { nearestTickIndex } from './QuestionMinimap';

const ticks = [10, 20, 30];
assert.equal(nearestTickIndex(-5, ticks), 0);
assert.equal(nearestTickIndex(50, ticks), 2);
assert.equal(nearestTickIndex(20, ticks), 1);
assert.equal(nearestTickIndex(15, ticks), 0);
assert.equal(nearestTickIndex(16, ticks), 1);
assert.equal(nearestTickIndex(0, []), -1);
