import { McodeRpcClient } from './rpc';

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

(globalThis as any).WebSocket = { OPEN: 1 };
(globalThis as any).window.addEventListener = () => {};

const summary = {
  id: 'thread-1', projectID: 'project-1', title: 'Thread', provider: 'openai', model: null, effort: null,
  isRunning: false, isArchived: false, isPinned: false, isDormant: false, hasUnread: false,
  attention: '', lastActivityText: null, section: 'inbox', orderKey: 'a', updatedAt: '2024-01-01T00:00:00Z',
  lastStatus: null, hasPendingRequest: false
};
const request = {
  threadID: 'thread-1', turnID: null, runID: 'run-1', requestID: 'request-1', kind: 'question',
  title: null, detail: null, reason: null, options: [], questions: []
};
const board = { schemaVersion: 1 as const, revision: 1, projects: [], tasks: [], goals: [] };

const calls: Record<string, number> = {};
const client = new McodeRpcClient({
  onConnectionChange: () => {}, onBoardUpdated: () => { calls.board = (calls.board ?? 0) + 1; },
  onAuthFailed: () => {}, onCredentialsChanged: () => {},
  onThreadsChanged: () => { calls.threads = (calls.threads ?? 0) + 1; },
  onRequestsChanged: () => { calls.requests = (calls.requests ?? 0) + 1; },
  onQueueChanged: () => { calls.queue = (calls.queue ?? 0) + 1; },
  onThreadEvent: () => { calls.event = (calls.event ?? 0) + 1; },
  onSwarmChanged: () => { calls.swarm = (calls.swarm ?? 0) + 1; }
}, null);
client.addCompactSuggestionListener(() => { calls.compact = (calls.compact ?? 0) + 1; });
const socket = { readyState: 1 };
const internal = client as any;
internal.ws = socket;
internal.isAuthenticated = true;
internal.authenticatedGeneration = internal.authGeneration;
const notify = (method: string, params: unknown) => internal.handleMessage({ currentTarget: socket, data: JSON.stringify({ jsonrpc: '2.0', method, params }) });

const methods = [
  ['kanban.taskUpdated', board, 'board'],
  ['threads.changed', { revision: 1, threads: [summary], removedThreadIDs: [] }, 'threads'],
  ['requests.changed', { threadID: 'thread-1', requests: [request] }, 'requests'],
  ['threads.queue.changed', { threadID: 'thread-1', revision: 1, followUps: [] }, 'queue'],
  ['threads.event', { kind: 'resync', threadID: 'thread-1', runID: 'run-1', sequence: 1, payload: { thread: summary } }, 'event'],
  ['threads.swarm.changed', { threadID: 'thread-1', swarmAvailable: true, swarmEnabled: true, isBee: false, bees: [] }, 'swarm'],
  ['threads.compactSuggestion', { threadID: 'thread-1', usedTokens: 10, idleSince: '2024-01-01T00:00:00Z' }, 'compact']
] as const;

for (const [method, params, key] of methods) {
  let observed = 0;
  client.onNotification(method, () => { observed += 1; });
  notify(method, params);
  assert(observed === 1, `${method} 应触发通用监听器`);
  assert(calls[key] === 1, `${method} 应触发内置回调`);
}

internal.isAuthenticated = false;
let notReady = 0;
client.onNotification('threads.changed', () => { notReady += 1; });
notify('threads.changed', { revision: 1, threads: [summary], removedThreadIDs: [] });
assert(notReady === 0, '未就绪时不应分发通知');
internal.isAuthenticated = true;

let invalid = 0;
client.onNotification('threads.changed', () => { invalid += 1; });
notify('threads.changed', { revision: 'bad' });
assert(invalid === 1 && calls.threads === 1, '解析失败不应触发内置回调');

let survived = 0;
client.onNotification('requests.changed', () => { throw new Error('test'); });
client.onNotification('requests.changed', () => { survived += 1; });
notify('requests.changed', { threadID: 'thread-1', requests: [request] });
assert(survived === 1, '单个监听器异常不应影响其它监听器');
