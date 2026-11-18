import { renderToStaticMarkup } from 'react-dom/server';
import type { RemoteProjectSummary } from '../../../types/contract';
import type { UseRemoteCallResult } from '../../../context/useRemoteCall';
import {
  REMOTE_THREADS_MOVE_TO_PROJECT_METHOD,
  ThreadMenuMoveToProjectView,
  requestMoveToProject
} from './ThreadMenuMoveToProjectPanel';

function assertEqual<T>(actual: T, expected: T, what: string): void {
  if (actual !== expected) {
    throw new Error(`期望${what}为 ${String(expected)}，实际为 ${String(actual)}`);
  }
}

function assertIncludes(haystack: string, needle: string): void {
  if (!haystack.includes(needle)) {
    throw new Error(`期望渲染结果包含 “${needle}”，实际为：${haystack}`);
  }
}

function assertNotIncludes(haystack: string, needle: string): void {
  if (haystack.includes(needle)) {
    throw new Error(`期望渲染结果不包含 “${needle}”，实际为：${haystack}`);
  }
}

const projects: RemoteProjectSummary[] = [
  { id: 'chats', name: '对话', path: '', isExpanded: true, threadCount: 2, kind: 'chats' },
  { id: 'steward', name: '总管', path: '', isExpanded: true, threadCount: 1, kind: 'steward' },
  { id: 'p1', name: 'Alpha', path: '/tmp/alpha', isExpanded: true, threadCount: 3 },
  { id: 'p2', name: 'Beta', path: '/tmp/beta', isExpanded: false, threadCount: 0 }
];

const idleHtml = renderToStaticMarkup(
  <ThreadMenuMoveToProjectView
    projects={projects}
    movingProjectID={null}
    error={null}
    onSelectProject={() => {}}
  />
);
assertIncludes(idleHtml, 'Alpha');
assertIncludes(idleHtml, 'Beta');
assertNotIncludes(idleHtml, '对话');
assertNotIncludes(idleHtml, '总管');
assertNotIncludes(idleHtml, 'disabled=""');
assertNotIncludes(idleHtml, 'role="alert"');

const movingHtml = renderToStaticMarkup(
  <ThreadMenuMoveToProjectView
    projects={projects}
    movingProjectID="p1"
    error={null}
    onSelectProject={() => {}}
  />
);
assertIncludes(movingHtml, '移动中…');
assertEqual(movingHtml.split('disabled=""').length - 1, 2, '移动中所有项目行均禁用');

const errorHtml = renderToStaticMarkup(
  <ThreadMenuMoveToProjectView
    projects={projects}
    movingProjectID={null}
    error="运行中的会话不能移到其他项目"
    onSelectProject={() => {}}
  />
);
assertIncludes(errorHtml, 'role="alert"');
assertIncludes(errorHtml, '运行中的会话不能移到其他项目');

async function runAsyncChecks(): Promise<void> {
  const calls: Array<{ method: string; params: unknown }> = [];
  const fakeCall: UseRemoteCallResult['call'] = async (method, params) => {
    calls.push({ method, params });
    return { id: 't1', projectID: 'p2' } as never;
  };
  const moved = await requestMoveToProject(fakeCall, { threadID: 't1', projectID: 'p2' });
  assertEqual(calls.length, 1, 'RPC 调用次数');
  assertEqual(calls[0].method, REMOTE_THREADS_MOVE_TO_PROJECT_METHOD, 'RPC 方法名');
  assertEqual(JSON.stringify(calls[0].params), JSON.stringify({ threadID: 't1', projectID: 'p2' }), 'RPC 参数');
  assertEqual((moved as { projectID: string }).projectID, 'p2', '返回的 projectID');
}

await runAsyncChecks();
