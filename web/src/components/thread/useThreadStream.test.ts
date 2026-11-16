import { protectedHistoryMessageIDs } from './useThreadStream';

function expectIDs(actual: Set<string>, expected: string[], message: string) {
  if (JSON.stringify([...actual].sort()) !== JSON.stringify(expected.sort())) throw new Error(message);
}

const initial = new Map([['unchanged', 1], ['updated', 2]]);
const current = new Map([['unchanged', 1], ['updated', 3], ['added', 4]]);
expectIDs(
  protectedHistoryMessageIDs(current, new Set(['removed']), initial, 'run-1', 'run-1'),
  ['updated', 'added', 'removed'],
  '历史在途期间新增、更新、删除的消息都应优先于历史响应'
);
expectIDs(
  protectedHistoryMessageIDs(current, new Set(['removed']), initial, 'run-1', 'run-2'),
  ['unchanged', 'updated', 'added', 'removed'],
  'run 切换后即使序列号相同，也应保留当前 run 的投影'
);
expectIDs(
  protectedHistoryMessageIDs(initial, new Set(), new Map(initial), null, null),
  [],
  '没有增量变化时，历史响应可以更新已有消息'
);
expectIDs(
  protectedHistoryMessageIDs(new Map(), new Set(['removed']), initial, 'run-1', 'run-1'),
  ['removed'],
  '删除消息后即使序列记录已清除，旧历史也不能使它复活'
);
if (initial.get('updated') !== 2 || current.get('updated') !== 3) throw new Error('保护集合计算不能修改序列快照');
