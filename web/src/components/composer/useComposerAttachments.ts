import { useCallback, useEffect, useRef, useState } from 'react';
import type { RemoteAttachmentUploadResult } from '../../types/contract';
import { isImageFile } from '../../context/remoteImages';

/**
 * 附件数量上限：宿主未声明 attachmentLimit 时按旧版约定的 4 处理；
 * 非正整数视为非法，同样退回 4。
 */
export function composerAttachmentLimit(limit?: number): number {
  if (typeof limit !== 'number' || !Number.isInteger(limit) || limit < 1) return 4;
  return limit;
}

/** 拖拽意图：命中 Files 就先拦浏览器默认行为，附件可用才接收文件。 */
export function fileDragIntent(types: Iterable<string>, attachmentsAvailable: boolean): {
  preventDefault: boolean;
  acceptsFiles: boolean;
} {
  const hasFiles = Array.from(types).includes('Files');
  return { preventDefault: hasFiles, acceptsFiles: hasFiles && attachmentsAvailable };
}

export function useComposerAttachments({ attachmentsSupported, isUploadingAttachment, attachments, attachmentLimit, imagesUnsupported, onAddAttachments }: {
  attachmentsSupported: boolean;
  isUploadingAttachment: boolean;
  attachments: RemoteAttachmentUploadResult[];
  attachmentLimit?: number;
  imagesUnsupported: boolean;
  onAddAttachments: (files: FileList | null) => void;
}) {
  const [attachNotice, setAttachNotice] = useState<string | null>(null);
  const [isDraggingFiles, setIsDraggingFiles] = useState(false);
  const dragDepthRef = useRef(0);
  // 附件不再限于图片：任意文件都可附加，图片由宿主压缩；当前模型不支持图片时只跳过图片
  const handlePickedAttachments = useCallback(
    (files: FileList | null) => {
      if (!files || files.length === 0) return;
      if (!imagesUnsupported) {
        setAttachNotice(null);
        onAddAttachments(files);
        return;
      }
      const picked = Array.from(files);
      const images = picked.filter(isImageFile);
      const others = picked.filter((file) => !isImageFile(file));
      setAttachNotice(images.length > 0 ? '当前模型不支持图片，已跳过选中的图片' : null);
      if (others.length === 0) return;
      try {
        // FileList 不能直接构造，用 DataTransfer 把非图片文件重新打包交给上传
        const transfer = new DataTransfer();
        others.forEach((file) => transfer.items.add(file));
        onAddAttachments(transfer.files);
      } catch {
        setAttachNotice('当前浏览器不支持筛选附件，请只选择非图片文件');
      }
    },
    [imagesUnsupported, onAddAttachments]
  );

  const attachmentsAvailable =
    attachmentsSupported && !isUploadingAttachment && attachments.length < composerAttachmentLimit(attachmentLimit);
  useEffect(() => {
    if (!attachmentsAvailable) {
      dragDepthRef.current = 0;
      setIsDraggingFiles(false);
    }
  }, [attachmentsAvailable]);

  return { attachmentsAvailable, attachNotice, isDraggingFiles, dragDepthRef, setIsDraggingFiles, handlePickedAttachments };
}
