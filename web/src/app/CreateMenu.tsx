import React from 'react';
import { MessageIcon, PlusIcon, SparklesIcon } from '../components/Icons';

type Props = {
  split: boolean;
  fabMenuOpen: boolean;
  setFabMenuOpen: React.Dispatch<React.SetStateAction<boolean>>;
  handleNewThread: () => void;
  canCreateTask: boolean;
  canCreateGoal: boolean;
  kanbanWritesBlocked: boolean;
  setCreateKind: React.Dispatch<React.SetStateAction<'task' | 'goal' | 'thread' | null>>;
  setCreateTitle: React.Dispatch<React.SetStateAction<string>>;
  setCreateNotes: React.Dispatch<React.SetStateAction<string>>;
  setCreateError: React.Dispatch<React.SetStateAction<string | null>>;
};

export const CreateMenu: React.FC<Props> = ({ split, fabMenuOpen, setFabMenuOpen, handleNewThread, canCreateTask, canCreateGoal, kanbanWritesBlocked, setCreateKind, setCreateTitle, setCreateNotes, setCreateError }) => (
  <>
      {(
        <>
          <button
            type="button"
            className="fab-button"
            onClick={() => setFabMenuOpen((open) => !open)}
            aria-label="新建"
            aria-expanded={fabMenuOpen}
            title="新建"
          >
            <PlusIcon className={`h-6 w-6 text-white transition-transform ${fabMenuOpen ? 'rotate-45' : ''}`} />
          </button>
          {fabMenuOpen && (
            <div
              className={split ? 'absolute inset-0 z-30' : 'fixed inset-0 z-30'}
              onClick={() => setFabMenuOpen(false)}
            >
              <div
                className={`${split ? 'absolute' : 'fixed'} z-40 min-w-44 overflow-hidden rounded-2xl border border-border bg-card p-1.5 shadow-2xl animate-fadeIn`}
                style={{
                  right: split ? '16px' : 'max(1rem, calc(1rem + var(--sar)))',
                  bottom: 'calc(env(safe-area-inset-bottom, 0px) + 5rem)'
                }}
                onClick={(event) => event.stopPropagation()}
                role="menu"
                aria-label="新建操作"
              >
                <button
                  type="button"
                  className="flex w-full items-center gap-2.5 rounded-xl px-3.5 py-3 text-sm font-medium text-foreground hover:bg-row-hover active:bg-row-hover"
                  onClick={() => {
                    setFabMenuOpen(false);
                    handleNewThread();
                  }}
                  role="menuitem"
                >
                  <MessageIcon className="h-4 w-4 text-primary" />
                  <span>新会话</span>
                </button>
                {canCreateTask && (
                  <button
                    type="button"
                    className="flex w-full items-center gap-2.5 rounded-xl px-3.5 py-3 text-sm font-medium text-foreground hover:bg-row-hover active:bg-row-hover disabled:opacity-40 disabled:pointer-events-none"
                    onClick={() => {
                      setFabMenuOpen(false);
                      setCreateKind('task');
                      setCreateTitle('');
                      setCreateNotes('');
                      setCreateError(null);
                    }}
                    disabled={kanbanWritesBlocked}
                    role="menuitem"
                  >
                    <PlusIcon className="h-4 w-4 text-primary" />
                    <span>新任务</span>
                  </button>
                )}
                {canCreateGoal && (
                  <button
                    type="button"
                    className="flex w-full items-center gap-2.5 rounded-xl px-3.5 py-3 text-sm font-medium text-foreground hover:bg-row-hover active:bg-row-hover disabled:opacity-40 disabled:pointer-events-none"
                    onClick={() => {
                      setFabMenuOpen(false);
                      setCreateKind('goal');
                      setCreateNotes('');
                      setCreateError(null);
                    }}
                    disabled={kanbanWritesBlocked}
                    role="menuitem"
                  >
                    <SparklesIcon className="h-4 w-4 text-primary" />
                    <span>新目标</span>
                  </button>
                )}
              </div>
            </div>
          )}
        </>
      )}
  </>
);
