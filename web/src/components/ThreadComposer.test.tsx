// 同 ComposerQuotaBar.test.tsx：web/ 没有测试运行器，用 react-dom/server 渲染成字符串后直接断言。
// 覆盖三件事：iOS 空 MIME 的图片识别、附件缩略图与大图预览、附件按钮只留加号。
import { renderToStaticMarkup } from 'react-dom/server';
import type { RemoteAttachmentUploadResult } from '../types/contract';
import type { RemotePersona, RemoteThreadsSetPersonaParams } from '../types/generated';
import { isPreviewableImageFile } from '../context/remoteImages';
import { RemoteProvider } from '../context/RemoteContext';
import {
  AttachmentPreviewOverlay,
  ComposerAttachButton,
  ComposerAttachmentStrip,
  PersonaBadge,
  PersonaSection,
  applyPersonaSelection,
  fileDragIntent,
  ThreadComposer,
  modelSheetPageOnEscape
} from './ThreadComposer';
import { executeDraftSubmit } from './ThreadDraftView';

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

function assertEqual<T>(actual: T, expected: T, what: string): void {
  if (actual !== expected) {
    throw new Error(`期望${what}为 ${String(expected)}，实际为 ${String(actual)}`);
  }
}

// ---- isPreviewableImageFile：iOS 选图时 file.type 可能为空，要按扩展名兜底 ----
// 只构造判断用到的两个字段，避免依赖运行时是否提供 File 构造器。
function fakeFile(name: string, type: string): File {
  return { name, type } as File;
}

assertEqual(isPreviewableImageFile(fakeFile('IMG_0550.jpg', '')), true, '空 MIME 的 .jpg 识别');
assertEqual(isPreviewableImageFile(fakeFile('IMG_0550.HEIC', '')), true, '空 MIME 的 .HEIC 识别');
assertEqual(isPreviewableImageFile(fakeFile('scan.PNG', 'application/octet-stream')), true, '错 MIME 的 .PNG 识别');
assertEqual(isPreviewableImageFile(fakeFile('photo', 'image/jpeg')), true, 'MIME 为 image/jpeg 识别');
assertEqual(isPreviewableImageFile(fakeFile('report.pdf', 'application/pdf')), false, 'PDF 不识别为图片');
assertEqual(isPreviewableImageFile(fakeFile('notes', '')), false, '无扩展名且无 MIME 不识别');

// ---- 附件条：有预览 URL 时渲染可点击的缩略图 ----
function attachment(overrides: Partial<RemoteAttachmentUploadResult>): RemoteAttachmentUploadResult {
  return {
    attachmentID: 'att-1',
    name: 'IMG_0550.jpg',
    mimeType: 'image/jpeg',
    byteCount: 2048,
    expiresAt: '2026-11-06T00:00:00Z',
    ...overrides
  };
}

const imageHtml = renderToStaticMarkup(
  <ComposerAttachmentStrip
    attachments={[attachment({})]}
    attachmentPreviews={{ 'att-1': 'blob:preview-1' }}
    onRemoveAttachment={() => {}}
  />
);
assertIncludes(imageHtml, 'src="blob:preview-1"');
// 缩略图本身是按钮，点它才打开大图；移除按钮单独一个 aria-label，不会触发预览。
assertIncludes(imageHtml, 'aria-label="预览附件 IMG_0550.jpg"');
assertIncludes(imageHtml, 'aria-label="移除附件 IMG_0550.jpg"');
assertExcludes(imageHtml, 'role="dialog"');

// 没有预览 URL 的附件仍显示文件名与大小，不渲染 img。
const fileHtml = renderToStaticMarkup(
  <ComposerAttachmentStrip
    attachments={[attachment({ attachmentID: 'att-2', name: 'notes.pdf', mimeType: 'application/pdf', byteCount: 512 })]}
    onRemoveAttachment={() => {}}
  />
);
assertIncludes(fileHtml, 'notes.pdf');
assertIncludes(fileHtml, '512 B');
assertExcludes(fileHtml, '<img');

// 没有附件时整条不渲染。
const emptyHtml = renderToStaticMarkup(
  <ComposerAttachmentStrip attachments={[]} onRemoveAttachment={() => {}} />
);
assertEqual(emptyHtml, '', '无附件时的渲染结果');

// ---- 大图预览：全屏黑底、Esc 与遮罩关闭由组件内的 effect 负责，这里断言结构 ----
const overlayHtml = renderToStaticMarkup(
  <AttachmentPreviewOverlay url="blob:preview-1" name="IMG_0550.jpg" onClose={() => {}} />
);
assertIncludes(overlayHtml, 'role="dialog"');
assertIncludes(overlayHtml, 'aria-modal="true"');
assertIncludes(overlayHtml, 'aria-label="图片预览 IMG_0550.jpg"');
assertIncludes(overlayHtml, 'src="blob:preview-1"');
assertIncludes(overlayHtml, 'aria-label="关闭图片预览"');
assertIncludes(overlayHtml, 'fixed inset-0');

// ---- 附件按钮：正方形图标按钮，没有「附件」文字，靠 aria-label 找到 ----
const attachHtml = renderToStaticMarkup(
  <ComposerAttachButton disabled={false} title="添加附件" onPicked={() => {}} />
);
assertIncludes(attachHtml, 'aria-label="添加附件"');
assertIncludes(attachHtml, 'title="添加附件"');
assertIncludes(attachHtml, 'h-[30px]');
assertIncludes(attachHtml, 'w-[30px]');
assertIncludes(attachHtml, 'justify-center');
assertExcludes(attachHtml, '>附件<');
assertExcludes(attachHtml, 'px-2.5');
assertExcludes(attachHtml, 'disabled=""');

const attachDisabledHtml = renderToStaticMarkup(
  <ComposerAttachButton disabled title="添加附件" onPicked={() => {}} />
);
assertIncludes(attachDisabledHtml, 'disabled=""');

// ---- 输入框状态提示：浮在卡片上方，不占文档流 ----
const composerProps = {
  draft: '',
  isSending: false,
  sendError: null,
  isPolling: false,
  sendDisabled: false,
  attachments: [],
  isUploadingAttachment: false,
  submitLabel: '发送',
  onDraftChange: () => {},
  onAddAttachments: () => {},
  onRemoveAttachment: () => {},
  onSend: () => {}
};
const blockedComposerHtml = renderToStaticMarkup(
  <RemoteProvider><ThreadComposer {...composerProps} sendBlockedReason="正在同步最新状态，请稍候" /></RemoteProvider>
);
assertIncludes(blockedComposerHtml, '正在同步最新状态，请稍候');
if (!/<div class="[^"]*\babsolute\b[^"]*" aria-live="polite"><div class="floating-status__pill[^\"]*"><p>正在同步最新状态，请稍候<\/p>/.test(blockedComposerHtml)) {
  throw new Error('发送受阻提示应位于绝对定位的浮层内');
}
const readyComposerHtml = renderToStaticMarkup(
  <RemoteProvider><ThreadComposer {...composerProps} sendBlockedReason={null} /></RemoteProvider>
);
assertExcludes(readyComposerHtml, 'aria-live="polite"><div class="floating-status__pill');

// ---- 模型浮层两级导航：Esc 退级 ----
// Esc：二级页先退回摘要页，摘要页才关整个浮层。
assertEqual(modelSheetPageOnEscape('lead'), 'summary', '主代理页 Esc 的目标页');
assertEqual(modelSheetPageOnEscape('bees'), 'summary', '子代理页 Esc 的目标页');
assertEqual(modelSheetPageOnEscape('summary'), null, '摘要页 Esc 的结果（null 表示关浮层）');

// ---- fileDragIntent：附件不可用时也要先识别文件拖拽，拦下浏览器默认打开行为 ----
const unavailableDrag = fileDragIntent(['Files'], false);
assertEqual(unavailableDrag.preventDefault, true, '附件不可用时仍拦截文件拖拽的默认行为');
assertEqual(unavailableDrag.acceptsFiles, false, '附件不可用时不接收文件');
const availableDrag = fileDragIntent(['Files'], true);
assertEqual(availableDrag.preventDefault, true, '附件可用时拦截文件拖拽的默认行为');
assertEqual(availableDrag.acceptsFiles, true, '附件可用时接收文件');
assertEqual(fileDragIntent(['text/plain'], true).preventDefault, false, '纯文本拖拽不拦截默认行为');

// ---- 草稿人设：无 threadID 时渲染与选择都走 draftPersona，不触发 threads.setPersona ----
const testPersonas: RemotePersona[] = [
  { id: 'concise', name: '简洁风格' },
  { id: 'strict', name: '严谨审查' }
];
const draftBadgeHtml = renderToStaticMarkup(
  <RemoteProvider>
    <PersonaBadge draftPersona="concise" personas={testPersonas} />
  </RemoteProvider>
);
assertIncludes(draftBadgeHtml, '简洁风格');
assertIncludes(draftBadgeHtml, '当前人设：简洁风格');

const emptyDraftBadgeHtml = renderToStaticMarkup(
  <RemoteProvider>
    <PersonaBadge draftPersona="" personas={testPersonas} />
  </RemoteProvider>
);
assertEqual(emptyDraftBadgeHtml, '', '草稿未选人设时不渲染人设徽标');

const draftPersonaSectionHtml = renderToStaticMarkup(
  <RemoteProvider>
    <PersonaSection
      draftPersona="strict"
      personas={testPersonas}
      saving={false}
      message={null}
      onSelect={() => {}}
    />
  </RemoteProvider>
);
if (!/<option\b(?=[^>]*\bvalue="strict")(?=[^>]*\bselected(?:="")?)[^>]*>严谨审查<\/option>/.test(draftPersonaSectionHtml)) {
  throw new Error(`期望严谨审查选项处于选中状态，实际为：${draftPersonaSectionHtml}`);
}

async function runPersonaChecks(): Promise<void> {
  // (a) 无 threadID 且传了 onDraftPersonaChange：只更新草稿人设，不调 setThreadPersona，也不报错
  const draftUpdates: string[] = [];
  let threadSetCalls = 0;
  await applyPersonaSelection({
    threadID: undefined,
    persona: 'concise',
    personaSupported: true,
    onDraftPersonaChange: (next) => draftUpdates.push(next),
    setThreadPersona: async () => {
      threadSetCalls += 1;
      throw new Error('当前会话不支持切换人设');
    }
  });
  assertEqual(draftUpdates.length, 1, '草稿人设回调次数');
  assertEqual(draftUpdates[0], 'concise', '草稿人设回传值');
  assertEqual(threadSetCalls, 0, '草稿阶段不应调用 threads.setPersona');

  // 已有 threadID 时仍走 setThreadPersona
  const threadUpdates: string[] = [];
  await applyPersonaSelection({
    threadID: 'thread-1',
    persona: 'strict',
    personaSupported: true,
    onDraftPersonaChange: () => {
      throw new Error('已有会话不应触发 onDraftPersonaChange');
    },
    setThreadPersona: async (next) => {
      threadUpdates.push(next);
    }
  });
  assertEqual(threadUpdates.length, 1, '已有会话调用 setThreadPersona 次数');
  assertEqual(threadUpdates[0], 'strict', '已有会话写入的人设值');

  // (b) 草稿提交顺序：threads.create -> threads.setPersona -> 发送首条消息
  const sequence: string[] = [];
  let recordedPersonaParams: RemoteThreadsSetPersonaParams | null = null;
  let personaApplied = false;
  let createdID: string | null = null;

  await executeDraftSubmit({
    existingThreadID: createdID,
    draftPersona: 'concise',
    personaApplied,
    provider: 'magpie',
    model: 'magpie-default',
    create: async () => {
      sequence.push('threads.create');
      return { actionID: 'act-create-1', state: 'accepted', threadID: 'thread-new' };
    },
    onCreateActionID: () => {},
    onCreatedThreadID: (id) => {
      createdID = id;
    },
    onCreateFailed: (msg) => {
      throw new Error(msg);
    },
    callRemote: async (method, params) => {
      sequence.push(method);
      if (method === 'threads.setPersona') {
        recordedPersonaParams = params as RemoteThreadsSetPersonaParams;
      }
      return {} as never;
    },
    onPersonaApplied: () => {
      personaApplied = true;
    },
    send: async (id) => {
      sequence.push(`send:${id}`);
    }
  });
  assertEqual(
    sequence.join(' -> '),
    'threads.create -> threads.setPersona -> send:thread-new',
    '草稿提交顺序'
  );
  assertEqual(
    JSON.stringify(recordedPersonaParams),
    JSON.stringify({
      threadID: 'thread-new',
      persona: 'concise',
      provider: 'magpie',
      model: 'magpie-default'
    }),
    'threads.setPersona 参数'
  );
  assertEqual(personaApplied, true, '成功后标记人设已应用');

  // threads.setPersona 失败时抛出「切换人设失败：<原因>」且不发消息；重试时不再重复创建会话
  const retrySeq: string[] = [];
  let retryCreatedID: string | null = null;
  let retryPersonaApplied = false;
  let personaFailedMessage = '';
  try {
    await executeDraftSubmit({
      existingThreadID: retryCreatedID,
      draftPersona: 'concise',
      personaApplied: retryPersonaApplied,
      provider: 'magpie',
      model: 'magpie-default',
      create: async () => {
        retrySeq.push('threads.create');
        return { actionID: 'act-create-2', state: 'accepted', threadID: 'thread-retry' };
      },
      onCreateActionID: () => {},
      onCreatedThreadID: (id) => {
        retryCreatedID = id;
      },
      onCreateFailed: () => {},
      callRemote: async (method) => {
        retrySeq.push(method);
        throw new Error('人设不存在');
      },
      onPersonaApplied: () => {
        retryPersonaApplied = true;
      },
      send: async () => {
        retrySeq.push('send');
      }
    });
  } catch (err) {
    personaFailedMessage = err instanceof Error ? err.message : String(err);
  }
  assertEqual(personaFailedMessage, '切换人设失败：人设不存在', '失败时错误消息');
  assertEqual(retryCreatedID, 'thread-retry', '失败后保留已创建的 threadID');
  assertEqual(retryPersonaApplied, false, '失败后不标记人设已应用');
  assertEqual(retrySeq.join(' -> '), 'threads.create -> threads.setPersona', '失败时未发送消息');

  await executeDraftSubmit({
    existingThreadID: retryCreatedID,
    draftPersona: 'concise',
    personaApplied: retryPersonaApplied,
    provider: 'magpie',
    model: 'magpie-default',
    create: async () => {
      retrySeq.push('threads.create-unexpected');
      return { actionID: 'act-create-3', state: 'accepted', threadID: 'thread-other' };
    },
    onCreateActionID: () => {},
    onCreatedThreadID: () => {},
    onCreateFailed: () => {},
    callRemote: async (method) => {
      retrySeq.push(`retry:${method}`);
      return {} as never;
    },
    onPersonaApplied: () => {
      retryPersonaApplied = true;
    },
    send: async (id) => {
      retrySeq.push(`retry:send:${id}`);
    }
  });
  assertEqual(retryPersonaApplied, true, '重试成功后标记人设已应用');
  assertEqual(
    retrySeq.join(' -> '),
    'threads.create -> threads.setPersona -> retry:threads.setPersona -> retry:send:thread-retry',
    '重试不重复创建会话并补上人设后发送'
  );
}

await runPersonaChecks();
