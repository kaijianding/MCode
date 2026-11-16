const assert = { equal: (a: unknown, b: unknown) => { if (a !== b) throw new Error(`${a} !== ${b}`); }, deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { connectionNoticeText } from './connectionNotice';

const lastConnectedAt = new Date(2026, 0, 1, 9, 5).getTime();
assert.equal(connectionNoticeText('connected', true, 0, lastConnectedAt, 5000), null);
assert.equal(connectionNoticeText('disconnected', true, null, lastConnectedAt, 5000), null);
assert.equal(connectionNoticeText('reconnecting', true, 0, lastConnectedAt, 4999), null);
assert.equal(connectionNoticeText('disconnected', false, 0, null, 5000), '网络已断开');
assert.equal(connectionNoticeText('reconnecting', true, 0, null, 5000), '连不上电脑');
assert.equal(connectionNoticeText('disconnected', false, 0, lastConnectedAt, 5000), '网络已断开 · 09:05');
assert.equal(connectionNoticeText('reconnecting', true, 0, lastConnectedAt, 5000), '连不上电脑 · 09:05');
