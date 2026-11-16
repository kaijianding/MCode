const assert = { equal: (a: unknown, b: unknown) => { if (a !== b) throw new Error(`${a} !== ${b}`); }, deepEqual: (a: unknown, b: unknown) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error('结果不匹配'); } };
import { orderProjectsByRecent, pushRecentProject } from './projectOrder';

assert.deepEqual(pushRecentProject(['a', 'b', 'a', 'b'], 'a'), ['a', 'b']);
assert.deepEqual(pushRecentProject(['a', 'b', 'c'], 'd', 2), ['d', 'a']);
assert.equal(pushRecentProject(Array.from({ length: 20 }, (_, i) => String(i)), 'new').length, 20);
const projects = ['a', 'b', 'c', 'd'].map((id) => ({ id }));
assert.deepEqual(orderProjectsByRecent(projects, ['missing', 'c', 'a']).map((project) => project.id), ['c', 'a', 'b', 'd']);
assert.deepEqual(projects.map((project) => project.id), ['a', 'b', 'c', 'd']);
