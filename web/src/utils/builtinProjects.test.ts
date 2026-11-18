const assert = {
  equal: (a: unknown, b: unknown) => {
    if (a !== b) throw new Error(`${a} !== ${b}`);
  },
  deepEqual: (a: unknown, b: unknown) => {
    if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配');
  }
};
import type { RemoteProjectSummary } from '../types/contract';
import { chatsProject, pickableProjects } from './builtinProjects';

type ProjectItem = Pick<RemoteProjectSummary, 'id' | 'kind'>;

const withBuiltins: ProjectItem[] = [
  { id: 'p1' },
  { id: 'steward', kind: 'steward' },
  { id: 'p2' },
  { id: 'chats', kind: 'chats' },
  { id: 'p3' }
];

assert.equal(chatsProject(withBuiltins)?.id, 'chats');
assert.deepEqual(
  pickableProjects(withBuiltins).map((project) => project.id),
  ['chats', 'p1', 'p2', 'p3']
);
// 不修改原数组顺序
assert.deepEqual(
  withBuiltins.map((project) => project.id),
  ['p1', 'steward', 'p2', 'chats', 'p3']
);

const withoutChats: ProjectItem[] = [
  { id: 'steward', kind: 'steward' },
  { id: 'p1' },
  { id: 'p2' }
];

assert.equal(chatsProject(withoutChats), undefined);
assert.deepEqual(
  pickableProjects(withoutChats).map((project) => project.id),
  ['p1', 'p2']
);
assert.deepEqual(pickableProjects([{ id: 'steward', kind: 'steward' }]), []);
assert.deepEqual(pickableProjects([]), []);
