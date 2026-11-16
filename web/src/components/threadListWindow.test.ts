const assert = { deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { recallThreadListWindow, rememberThreadListWindow } from './threadListWindow';

assert.deepEqual(recallThreadListWindow('missing'), { activeLimit: 5, dormantExpanded: false, dormantLimit: 10 });
rememberThreadListWindow('first', { activeLimit: 15 });
rememberThreadListWindow('first', { dormantExpanded: true });
assert.deepEqual(recallThreadListWindow('first'), { activeLimit: 15, dormantExpanded: true, dormantLimit: 10 });
rememberThreadListWindow('second', { dormantLimit: 35 });
assert.deepEqual(recallThreadListWindow('second'), { activeLimit: 5, dormantExpanded: false, dormantLimit: 35 });
assert.deepEqual(recallThreadListWindow('first'), { activeLimit: 15, dormantExpanded: true, dormantLimit: 10 });
