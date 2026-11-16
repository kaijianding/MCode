const assert = { equal: (a: unknown, b: unknown) => { if (a !== b) throw new Error(`${a} !== ${b}`); }, deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { formatHitCount } from './remoteMessageSearchApi';

assert.equal(formatHitCount(0), '0 条');
assert.equal(formatHitCount(29), '29 条');
assert.equal(formatHitCount(30), '30+ 条');
