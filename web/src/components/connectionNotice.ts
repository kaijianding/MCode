export const CONNECTION_NOTICE_DELAY_MS = 5000;

export function connectionNoticeText(state: string, online: boolean, since: number | null, lastConnectedAt: number | null, now: number): string | null {
  if ((state !== 'reconnecting' && state !== 'disconnected') || since === null || now - since < CONNECTION_NOTICE_DELAY_MS) return null;
  const message = online ? '连不上电脑' : '网络已断开';
  if (lastConnectedAt === null) return message;
  const date = new Date(lastConnectedAt);
  const time = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
  return `${message} · ${time}`;
}
