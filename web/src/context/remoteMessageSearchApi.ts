// 消息全文搜索的远程调用封装，对应宿主 internal/remote/message_search_methods.go。
// 消息类型来自 web/src/types/generated.ts（由 docs/remote-control.schema.json 生成），此处按原名再导出。

import type {
  RemoteMessageSearchHit,
  RemoteMessageSearchResult
} from '../types/generated';
import type { UseRemoteCallResult } from './useRemoteCall';

export type {
  RemoteMessageSearchHit,
  RemoteMessageSearchResult
};

export const MESSAGES_SEARCH_METHOD = 'messages.search';

// 宿主默认返回条数；结果达到该数量时列表显示 N+，提示可能还有更多。
export const MESSAGE_SEARCH_LIMIT = 30;

export function formatHitCount(n: number): string {
  return n >= MESSAGE_SEARCH_LIMIT ? `${n}+ 条` : `${n} 条`;
}

/**
 * 跨未归档会话搜索消息正文；query 只有空白时不发请求。
 * 宿主默认返回 30 条、上限 100 条，limit 省略时用宿主默认值。
 */
export async function searchMessages(
  call: UseRemoteCallResult['call'],
  query: string,
  limit?: number
): Promise<RemoteMessageSearchHit[]> {
  const needle = query.trim();
  if (!needle) return [];
  const params: { query: string; limit?: number } = { query: needle };
  if (typeof limit === 'number' && Number.isFinite(limit) && limit > 0) {
    params.limit = Math.min(Math.floor(limit), 100);
  }
  const result = await call<RemoteMessageSearchResult>(MESSAGES_SEARCH_METHOD, params);
  return Array.isArray(result?.hits) ? result.hits : [];
}
