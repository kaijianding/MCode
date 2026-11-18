// 会话更多操作面板的「移到项目」子页：列出普通项目，把「对话」会话移入目标项目。

import React, { useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react';
import type { RemoteProjectSummary } from '../../../types/contract';
import type {
  RemoteThreadsMoveToProjectParams,
  RemoteThreadsMoveToProjectResponse
} from '../../../types/generated';
import type { UseRemoteCallResult } from '../../../context/useRemoteCall';
import { useRemoteCall } from '../../../context/useRemoteCall';
import { moveTargetProjects } from '../../../utils/builtinProjects';
import { FolderIcon } from '../../Icons';
import { MenuRow } from './MenuRow';
import type { ThreadMenuPageHandle } from './menuPages';

export const REMOTE_THREADS_MOVE_TO_PROJECT_METHOD = 'threads.moveToProject';

export async function requestMoveToProject(
  call: UseRemoteCallResult['call'],
  params: RemoteThreadsMoveToProjectParams
): Promise<RemoteThreadsMoveToProjectResponse['result']> {
  return call<RemoteThreadsMoveToProjectResponse['result']>(
    REMOTE_THREADS_MOVE_TO_PROJECT_METHOD,
    params
  );
}

export interface ThreadMenuMoveToProjectViewProps {
  projects: readonly RemoteProjectSummary[];
  movingProjectID: string | null;
  error: string | null;
  onSelectProject: (projectID: string) => void;
}

export function ThreadMenuMoveToProjectView({
  projects,
  movingProjectID,
  error,
  onSelectProject
}: ThreadMenuMoveToProjectViewProps) {
  const targetProjects = moveTargetProjects(projects);
  const isMoving = movingProjectID !== null;

  return (
    <div className="mt-4">
      <div className="sheet-actions !mt-0">
        {targetProjects.map((project) => (
          <MenuRow
            key={project.id}
            icon={<FolderIcon className="h-4 w-4" />}
            label={project.name}
            value={movingProjectID === project.id ? '移动中…' : undefined}
            disabled={isMoving}
            onClick={() => onSelectProject(project.id)}
          />
        ))}
      </div>
      {error && (
        <p className="mt-1.5 text-mc-caption text-[var(--danger-foreground)]" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

export interface ThreadMenuMoveToProjectPanelProps {
  threadID: string;
  projects: readonly RemoteProjectSummary[];
  onCloseMenu: () => void;
}

export const ThreadMenuMoveToProjectPanel = React.forwardRef<
  ThreadMenuPageHandle,
  ThreadMenuMoveToProjectPanelProps
>(function ThreadMenuMoveToProjectPanel({ threadID, projects, onCloseMenu }, ref) {
  const { call } = useRemoteCall();
  const [movingProjectID, setMovingProjectID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const isMoving = movingProjectID !== null;
  useImperativeHandle(ref, () => ({ requestClose: () => !isMoving }), [isMoving]);

  const handleSelectProject = useCallback(
    async (projectID: string) => {
      if (movingProjectID !== null) return;
      setMovingProjectID(projectID);
      setError(null);
      try {
        await requestMoveToProject(call, { threadID, projectID });
        if (!mountedRef.current) return;
        onCloseMenu();
      } catch (err) {
        if (!mountedRef.current) return;
        setError(err instanceof Error && err.message ? err.message : '移到项目失败');
      } finally {
        if (mountedRef.current) setMovingProjectID(null);
      }
    },
    [call, movingProjectID, onCloseMenu, threadID]
  );

  return (
    <ThreadMenuMoveToProjectView
      projects={projects}
      movingProjectID={movingProjectID}
      error={error}
      onSelectProject={(projectID) => void handleSelectProject(projectID)}
    />
  );
});
