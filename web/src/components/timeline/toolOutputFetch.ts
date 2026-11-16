import type { RemoteThreadsToolOutputParams, RemoteThreadsToolOutputResult } from '../../types/generated';

export function needsToolOutputFetch(outputBytes: number | undefined, text: string): boolean {
  return typeof outputBytes === 'number' && outputBytes > new TextEncoder().encode(text).length;
}

export async function fetchToolOutput(
  read: (params: RemoteThreadsToolOutputParams) => Promise<RemoteThreadsToolOutputResult>,
  threadID: string,
  messageID: string,
): Promise<string> {
  const chunks: string[] = [];
  let offset = 0;
  while (true) {
    const result = await read({ threadID, messageID, offset, length: 524288 });
    chunks.push(result.text);
    if (result.eof) return chunks.join('');
    const next = result.offset + new TextEncoder().encode(result.text).length;
    if (next <= offset) throw new Error('Tool output read made no progress');
    offset = next;
  }
}
