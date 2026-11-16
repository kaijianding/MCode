import { useEffect, useMemo, useState } from 'react';
import type { MutableRefObject } from 'react';
import { useRemote } from '../../context/RemoteContext';
import type { RemoteAttachmentUploadResult, RemoteThreadSummary } from '../../types/contract';
import type { QueuedFollowUpSectionProps } from './QueuedFollowUpSection';

interface ThreadQueueOptions {
  thread: RemoteThreadSummary;
  latestSummary: RemoteThreadSummary;
  queueSupported: boolean;
  isConnected: boolean;
  mountedRef: MutableRefObject<boolean>;
  draft: string;
  attachments: RemoteAttachmentUploadResult[];
}

// 操作与编辑租借继续由 QueuedFollowUpSection 管理；这里负责会话页的队列读取和接线。
export function useThreadQueue({
  thread, latestSummary, queueSupported, isConnected, mountedRef, draft, attachments
}: ThreadQueueOptions) {
  const { queueSnapshots, listThreadQueue } = useRemote();
  const queueSnapshot = queueSnapshots[thread.id];
  const [queuePollError, setQueuePollError] = useState<string | null>(null);
  const [queueRevealRequest, setQueueRevealRequest] = useState<{ followUpID: string; nonce: number } | null>(null);

  useEffect(() => {
    if (!queueSupported || !isConnected) return;
    setQueuePollError(null);
    void listThreadQueue(thread.id).catch((err) => {
      if (mountedRef.current) setQueuePollError(err instanceof Error ? err.message : '加载跟进队列失败');
    });
  }, [isConnected, listThreadQueue, queueSupported, thread.id]);


  // 跟进队列编辑组借出时需要把输入框当前草稿与附件一起寄存给宿主
  const queueComposerDraft = useMemo(
    () => ({ text: draft, attachmentIDs: attachments.map((attachment) => attachment.attachmentID) }),
    [attachments, draft]
  );

  const queueSectionProps: Omit<QueuedFollowUpSectionProps, 'onRestoreDraft'> = {
    threadID: thread.id,
    followUps: queueSnapshot?.followUps ?? [],
    revealRequest: queueRevealRequest,
    isRunning: latestSummary.isRunning,
    provider: latestSummary.provider,
    composerDraft: queueComposerDraft
  };
  return { queuePollError, setQueueRevealRequest, queueSectionProps };
}
