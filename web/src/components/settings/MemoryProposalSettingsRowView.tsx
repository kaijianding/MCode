// 记忆提议设置行的展示部分单独成文件：保持纯粹的展示，不直接或间接引入任何 .css，
// 这样才能在没有测试运行器的 web/ 里用 react-dom/server 静态渲染断言。

import React from 'react';
import { SettingsRow, SettingsSection } from './SettingsLayout';

interface MemoryProposalSettingsRowViewProps {
  pendingCount: number;
  onOpen: () => void;
}

// 记忆提议行的展示部分单独导出：走设置页统一的 SettingsSection / SettingsRow，
// 也便于不带 RemoteProvider 的静态渲染测试。
export const MemoryProposalSettingsRowView: React.FC<MemoryProposalSettingsRowViewProps> = ({
  pendingCount,
  onOpen
}) => (
  <SettingsSection title="记忆">
    <SettingsRow
      itemID="web-memory-proposals"
      title="记忆提议"
      control={
        <button type="button" className="secondary-button settings-inline-button" onClick={onOpen}>
          <span>打开</span>
          {pendingCount > 0 && (
            <span className="ml-auto shrink-0 min-w-[20px] px-1.5 py-0.5 rounded-full bg-[var(--danger)] text-center text-mc-caption font-semibold leading-[14px] text-white">
              {pendingCount > 99 ? '99+' : pendingCount}
            </span>
          )}
        </button>
      }
    />
  </SettingsSection>
);
