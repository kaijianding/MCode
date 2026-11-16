import React from 'react';
import { SheetFrame } from '../components/sheet/SheetFrame';
import { XMarkIcon } from '../components/Icons';

type Props = {
  createKind: 'task' | 'goal' | 'thread' | null;
  setCreateKind: React.Dispatch<React.SetStateAction<'task' | 'goal' | 'thread' | null>>;
  createBusy: boolean;
  createProjectID: string;
  setCreateProjectID: React.Dispatch<React.SetStateAction<string>>;
  projects: { id: string; name: string }[];
  createTitle: string;
  setCreateTitle: React.Dispatch<React.SetStateAction<string>>;
  createNotes: string;
  setCreateNotes: React.Dispatch<React.SetStateAction<string>>;
  createError: string | null;
  kanbanWritesBlocked: boolean;
  submitCreate: (event: React.FormEvent<HTMLFormElement>) => void;
};

export const CreateSheet: React.FC<Props> = ({ createKind, setCreateKind, createBusy, createProjectID, setCreateProjectID, projects, createTitle, setCreateTitle, createNotes, setCreateNotes, createError, kanbanWritesBlocked, submitCreate }) => (
  <>
      {(createKind === 'task' || createKind === 'goal') && (
        <SheetFrame onRequestClose={() => !createBusy && setCreateKind(null)} size="bare" cardClassName="create-sheet" labelledBy="create-kanban-title">
            <div className="sheet-handle" aria-hidden="true" />
            <div className="flex items-center justify-between gap-3">
              <div><p className="eyebrow">看板</p><h2 id="create-kanban-title" className="sheet-title">{createKind === 'task' ? '新任务' : '新目标'}</h2></div>
              <button type="button" className="icon-button" onClick={() => setCreateKind(null)} disabled={createBusy} aria-label="关闭" title="关闭"><XMarkIcon className="h-4 w-4" /></button>
            </div>
            <form className="mt-4 space-y-3" onSubmit={submitCreate}>
              <label className="settings-label" htmlFor="app-kanban-create-project">项目</label>
              <select id="app-kanban-create-project" value={createProjectID} onChange={(event) => setCreateProjectID(event.target.value)} className="sheet-select" disabled={createBusy}>
                <option value="">选择一个项目</option>
                {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
              </select>
              {createKind === 'task' ? (
                <>
                  <label className="settings-label" htmlFor="app-kanban-task-title">标题</label>
                  <input id="app-kanban-task-title" value={createTitle} onChange={(event) => setCreateTitle(event.target.value)} className="pairing-input" placeholder="要完成什么？" disabled={createBusy} autoFocus />
                  <label className="settings-label" htmlFor="app-kanban-task-notes">说明</label>
                  <textarea id="app-kanban-task-notes" value={createNotes} onChange={(event) => setCreateNotes(event.target.value)} className="pairing-input min-h-24 resize-y" placeholder="可选" disabled={createBusy} />
                </>
              ) : (
                <>
                  <label className="settings-label" htmlFor="app-kanban-goal-prompt">目标描述</label>
                  <textarea id="app-kanban-goal-prompt" value={createNotes} onChange={(event) => setCreateNotes(event.target.value)} className="pairing-input min-h-28 resize-y" placeholder="一句话描述希望 Mac 完成的目标" disabled={createBusy} autoFocus />
                  <p className="settings-note">目标使用 Mac 默认策略：串行、先预览、自动验收、最多两次尝试。</p>
                </>
              )}
              {createError && <p className="settings-error" role="alert">{createError}</p>}
              <button type="submit" className="primary-button primary-button--wide" disabled={createBusy || kanbanWritesBlocked || !createProjectID || (createKind === 'task' ? !createTitle.trim() : !createNotes.trim())} data-host-actionable="true">
                {createBusy ? '提交中…' : '创建'}
              </button>
            </form>
          </SheetFrame>
      )}
  </>
);
