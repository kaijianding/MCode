// 同 ComposerQuotaBar.test.tsx：web/ 没有测试运行器，顶层直接断言，失败时 throw。
// 额度配色按用量分档：<50 绿、<80 黄、<95 橙、其余红；百分比文字在低用量时用弱化色。
import { renderToStaticMarkup } from 'react-dom/server';
import type { RemoteQuotaBudget } from '../types/quota';
import {
  ComposerQuotaSheet,
  agyAccountChoices,
  agyAccountSelectable,
  agyLeadAccountLocked,
  usageColor,
  usagePercentColor
} from './ComposerQuotaSheet';

function assertEqual<T>(actual: T, expected: T, label: string): void {
  if (actual !== expected) {
    throw new Error(`${label} 期望 “${String(expected)}”，实际为 “${String(actual)}”`);
  }
}

function assertIncludes(haystack: string, needle: string): void {
  if (!haystack.includes(needle)) {
    throw new Error(`期望渲染结果包含 “${needle}”，实际为：${haystack}`);
  }
}

function assertExcludes(haystack: string, needle: string): void {
  if (haystack.includes(needle)) {
    throw new Error(`期望渲染结果不含 “${needle}”，实际为：${haystack}`);
  }
}

assertEqual(usageColor(0), 'var(--quota-low)', 'usageColor(0)');
assertEqual(usageColor(49.9), 'var(--quota-low)', 'usageColor(49.9)');
assertEqual(usageColor(50), 'var(--quota-mid)', 'usageColor(50)');
assertEqual(usageColor(80), 'var(--quota-high)', 'usageColor(80)');
assertEqual(usageColor(95), 'var(--quota-critical)', 'usageColor(95)');
assertEqual(usageColor(100), 'var(--quota-critical)', 'usageColor(100)');

assertEqual(usagePercentColor(30), 'var(--foreground-muted)', 'usagePercentColor(30)');
assertEqual(usagePercentColor(74), 'var(--quota-mid)', 'usagePercentColor(74)');

function makeAgyBudget(
  accountID: string,
  account: string,
  windows: RemoteQuotaBudget['windows'],
  overrides: Partial<RemoteQuotaBudget> = {}
): RemoteQuotaBudget {
  return {
    provider: 'agy',
    providerName: 'Antigravity',
    role: 'lead',
    profile: null,
    state: 'used',
    windows,
    amount: null,
    readAt: null,
    account,
    accountID,
    ...overrides
  };
}

// ---- agyAccountChoices：过滤、按 accountID 去重保序、全部 disabled / 5h 满禁用 ----
const choiceBudgets: RemoteQuotaBudget[] = [
  // 非 agy 与空 accountID 应被忽略
  {
    provider: 'codex',
    providerName: 'Codex',
    role: 'lead',
    profile: null,
    state: 'used',
    windows: [{ period: '5h', title: '5h', usedPercent: 10, resetsAt: null }],
    amount: null,
    readAt: null,
    account: 'codex@a.com',
    accountID: 'codex-1'
  },
  makeAgyBudget('', '无 ID 账号', [{ period: '5h', title: '5h', usedPercent: 10, resetsAt: null }]),
  // acc-1 分散在两条 budget 中，应去重并保留首次出现位置
  makeAgyBudget('acc-1', '个人账号', [{ id: 'Gemini', period: '5h', title: 'Gemini', usedPercent: 25, resetsAt: null }]),
  // acc-2 通过 id 识别 5h 窗口且已满（100%），应为 disabled
  makeAgyBudget('acc-2', '工作账号', [{ id: '5h', period: null, title: '5h', usedPercent: 100, resetsAt: null }]),
  makeAgyBudget('acc-1', '个人账号重复', [{ id: 'Claude 7d', period: '7d', title: '7d', usedPercent: 100, resetsAt: null }]),
  // acc-3 所有窗口均 disabled，应为 disabled
  makeAgyBudget('acc-3', '已停用账号', [
    { id: 'Gemini', period: '5h', title: '5h', usedPercent: 15, resetsAt: null, disabled: true },
    { id: 'Claude 7d', period: '7d', title: '7d', usedPercent: 20, resetsAt: null, disabled: true }
  ])
];

const choices = agyAccountChoices(choiceBudgets);
assertEqual(choices.length, 3, 'agyAccountChoices 去重数量');
assertEqual(choices[0].accountID, 'acc-1', 'choices[0].accountID');
assertEqual(choices[0].account, '个人账号', 'choices[0].account');
assertEqual(choices[0].disabled, false, 'choices[0].disabled（仅 7d 满不禁用）');
assertEqual(choices[1].accountID, 'acc-2', 'choices[1].accountID');
assertEqual(choices[1].disabled, true, 'choices[1].disabled（5h 满禁用）');
assertEqual(choices[2].accountID, 'acc-3', 'choices[2].accountID');
assertEqual(choices[2].disabled, true, 'choices[2].disabled（窗口全部 disabled）');

// ---- agyAccountSelectable：需 hasThread 且至少 2 个窗口未全部 disabled 的不同账号 ----
assertEqual(agyAccountSelectable(choiceBudgets, false), false, '无 thread 时不可选');
assertEqual(agyAccountSelectable(choiceBudgets, true), true, 'acc-1 与 acc-2 窗口未全 disabled 时可选');
assertEqual(
  agyAccountSelectable(
    [
      makeAgyBudget('acc-1', '个人账号', [{ period: '5h', title: '5h', usedPercent: 10, resetsAt: null }]),
      makeAgyBudget('acc-3', '已停用账号', [{ period: '5h', title: '5h', usedPercent: 10, resetsAt: null, disabled: true }])
    ],
    true
  ),
  false,
  '仅 1 个未全 disabled 账号时不可选'
);

// ---- agyLeadAccountLocked：运行中、无消息、有消息可用、有消息但 5h 满 ----
assertEqual(
  agyLeadAccountLocked({ isRunning: true, hasMessages: false, currentAccountID: '', choices }),
  true,
  '运行中锁定'
);
assertEqual(
  agyLeadAccountLocked({ isRunning: false, hasMessages: false, currentAccountID: 'acc-1', choices }),
  false,
  '无消息解锁'
);
assertEqual(
  agyLeadAccountLocked({ isRunning: false, hasMessages: true, currentAccountID: 'acc-1', choices }),
  true,
  '有消息且账号可用时锁定'
);
assertEqual(
  agyLeadAccountLocked({ isRunning: false, hasMessages: true, currentAccountID: 'acc-2', choices }),
  false,
  '有消息且账号 5h 满时解锁'
);
assertEqual(
  agyLeadAccountLocked({ isRunning: false, hasMessages: true, currentAccountID: '', choices }),
  false,
  '有消息但 currentAccountID 为空时解锁'
);
assertEqual(
  agyLeadAccountLocked({ isRunning: false, hasMessages: true, currentAccountID: 'missing', choices }),
  false,
  '有消息但 currentAccountID 不在 choices 里时解锁'
);

// ---- ComposerQuotaSheet 渲染校验：已有会话显示 radiogroup 与「自动」，草稿保持只读 ----
const selectableHtml = renderToStaticMarkup(
  <ComposerQuotaSheet
    isOpen
    onClose={() => {}}
    provider="agy"
    role="lead"
    budgets={choiceBudgets}
    hasThread
    selectedAccountID=""
    isRunning={false}
    hasMessages={false}
    onRefresh={() => {}}
  />
);
assertIncludes(selectableHtml, 'role="radiogroup"');
assertIncludes(selectableHtml, 'role="radio"');
assertIncludes(selectableHtml, '自动');
assertIncludes(selectableHtml, 'aria-checked="true"');

const draftHtml = renderToStaticMarkup(
  <ComposerQuotaSheet
    isOpen
    onClose={() => {}}
    provider="agy"
    role="lead"
    budgets={choiceBudgets}
    hasThread={false}
    onRefresh={() => {}}
  />
);
assertExcludes(draftHtml, 'role="radiogroup"');
assertExcludes(draftHtml, '自动');
