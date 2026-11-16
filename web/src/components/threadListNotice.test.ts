const assert = { equal: (a: unknown, b: unknown) => { if (a !== b) throw new Error(`${a} !== ${b}`); }, deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { undoKind } from './threadListNotice';

assert.equal(undoKind(null), null);
assert.equal(undoKind({}), null);
assert.equal(undoKind({ undoToken: 'token' }), 'bulk');
assert.equal(undoKind({ archivedThread: { id: 'thread' } }), 'archive');
assert.equal(undoKind({ undoToken: 'token', archivedThread: { id: 'thread' } }), 'archive');
