import { renderToStaticMarkup } from 'react-dom/server';
import { TimelineEntry, type ThreadMessage } from './ThreadTimeline';

const renderMessage = (message: ThreadMessage) => renderToStaticMarkup(
  <TimelineEntry
    activity={{}} threadID="thread" item={{ type: 'message', message }} expanded={false}
    copyState={null} minuteTick={0} onToggleExpanded={() => {}} onToggleWorkRecord={() => {}}
    segmentOpenState={{}} onToggleToolSegment={() => {}} autoCollapse={false}
    onCopyMessage={() => {}} canBranch={false} highlighted={false} decisionAnswers={{}}
  />
);
const base = { createdAt: '2026-11-06T08:00:00.000Z', turnIndex: 42, text: '正文' };
const assistant = renderMessage({ ...base, id: 'assistant', role: 'assistant', kind: 'assistant', isFinalResponse: true });
const user = renderMessage({ ...base, id: 'user', role: 'user', kind: 'user' });
const error = renderMessage({ ...base, id: 'error', role: 'system', kind: 'error', errKind: 'api_error' });
for (const html of [assistant, user, error]) {
  if (html.includes('助手 · 答复') || />\s*#42(?:\s|<|·)/.test(html)) throw new Error('消息不应显示类型标签或轮次编号');
  if (!html.includes('title=')) throw new Error('消息应通过 title 提供时间');
}
if (!error.includes('❌ 接口错误')) throw new Error('错误标题应保留');
