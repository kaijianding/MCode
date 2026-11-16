import { shouldAutoLoadOlder } from './autoLoadOlder';

function assert(condition: boolean): void {
  if (!condition) throw new Error('自动加载判断不符合预期');
}

const ready = { intersecting: true, hasMore: true, loading: false, failed: false };
assert(shouldAutoLoadOlder(ready));
assert(!shouldAutoLoadOlder({ ...ready, hasMore: false }));
assert(!shouldAutoLoadOlder({ ...ready, loading: true }));
assert(!shouldAutoLoadOlder({ ...ready, failed: true }));
assert(!shouldAutoLoadOlder({ ...ready, intersecting: false }));
