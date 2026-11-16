const assert = { equal: (a: unknown, b: unknown) => { if (a !== b) throw new Error(`${a} !== ${b}`); }, deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { threadListTargetIndex } from './threadListKeys';

assert.equal(threadListTargetIndex('ArrowUp', 0, 3), 0);
assert.equal(threadListTargetIndex('ArrowDown', 2, 3), 2);
assert.equal(threadListTargetIndex('ArrowUp', 1, 3), 0);
assert.equal(threadListTargetIndex('ArrowDown', 1, 3), 2);
assert.equal(threadListTargetIndex('Home', 1, 3), 0);
assert.equal(threadListTargetIndex('End', 1, 3), 2);
assert.equal(threadListTargetIndex('Enter', 1, 3), null);
assert.equal(threadListTargetIndex('Home', 0, 0), null);
