import { composerAttachmentLimit, fileDragIntent } from './useComposerAttachments';

function assertEqual(actual: unknown, expected: unknown): void {
  if (actual !== expected) throw new Error(`期望 ${String(expected)}，实际 ${String(actual)}`);
}

assertEqual(composerAttachmentLimit(undefined), 4);
assertEqual(composerAttachmentLimit(0), 4);
assertEqual(composerAttachmentLimit(1.5), 4);
assertEqual(composerAttachmentLimit(6), 6);
assertEqual(fileDragIntent(['Files'], true).acceptsFiles, true);
assertEqual(fileDragIntent(['Files'], false).preventDefault, true);
assertEqual(fileDragIntent(['text/plain'], true).preventDefault, false);
