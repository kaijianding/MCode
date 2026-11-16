import React, { useState } from 'react';
import { useRemoteStatic } from '../context/RemoteContext';
import { MessageIcon } from '../components/Icons';
import { parsePairingInput } from '../services/storage';

export const PairingScreen: React.FC = () => {
  const {
    connectionState,
    connectionMessage,
    authError,
    pairingError,
    pairingInProgress,
    rememberDevice,
    setRememberDevice,
    credentialNotice,
    pairWithToken,
    clearAuth
  } = useRemoteStatic();
  const [pairingInput, setPairingInput] = useState('');
  const [pairingInputError, setPairingInputError] = useState<string | null>(null);

  const pairingBusy = pairingInProgress && (connectionState === 'connecting' || connectionState === 'reconnecting');
  const pairingFeedback = pairingInputError || pairingError || (connectionState === 'auth_failed' ? authError : null);
  const showStatus = pairingInProgress && connectionState !== 'auth_failed';

  const submitPairing = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pairingBusy) return;
    const result = parsePairingInput(pairingInput);
    if (!result.token) {
      setPairingInputError(result.error || '配对码无效');
      return;
    }
    setPairingInputError(null);
    pairWithToken(result.token);
  };

  return (
    <main className="pairing-screen">
      <section className="pairing-panel" aria-labelledby="pairing-title">
        <div className="pairing-brand" aria-hidden="true"><span>D</span></div>
        <p className="eyebrow">mcode</p>
        <h1 id="pairing-title" className="pairing-title">
          {connectionState === 'auth_failed' ? '配对凭据已失效' : '连接你的 Mac'}
        </h1>
        <p className="pairing-copy">
          {connectionState === 'auth_failed'
            ? '使用 Mac 设置中刚生成的配对码重新建立连接。'
            : '输入 Mac 设置中显示的配对码，或粘贴完整配对链接。'}
        </p>

        {credentialNotice && <p className="inline-alert inline-alert--warning" role="status">{credentialNotice}</p>}

        <form onSubmit={submitPairing} className="pairing-form" aria-busy={pairingBusy}>
          <label htmlFor="pairing-input" className="settings-label">配对码或配对链接</label>
          <input
            id="pairing-input"
            type="text"
            value={pairingInput}
            onChange={(event) => {
              setPairingInput(event.target.value);
              if (pairingInputError) setPairingInputError(null);
            }}
            placeholder="输入 6 位授权码、64 位配对码或完整链接"
            autoComplete="off"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            enterKeyHint="go"
            aria-invalid={Boolean(pairingFeedback)}
            className="pairing-input select-text"
          />
          <label className="settings-checkbox">
            <input type="checkbox" checked={rememberDevice} onChange={(event) => setRememberDevice(event.target.checked)} disabled={pairingBusy} />
            <span>记住此设备</span>
          </label>
          {pairingFeedback && <p className="settings-error" role="alert">{pairingFeedback}</p>}
          {showStatus && <p className="settings-note" role="status" aria-live="polite">{connectionState === 'reconnecting' ? `正在重试连接。${connectionMessage ? ` ${connectionMessage}` : ''}` : '正在连接 Mac，请稍候。'}</p>}
          <div className="pairing-actions">
            <button type="submit" className="primary-button primary-button--wide" disabled={pairingBusy || !pairingInput.trim()}>{pairingBusy ? '配对中…' : '连接'}</button>
            <button type="button" className="secondary-button" onClick={() => { setPairingInput(''); setPairingInputError(null); clearAuth(); }}>清除</button>
          </div>
        </form>

        <div className="pairing-help">
          <span className="pairing-help__icon"><MessageIcon className="h-4 w-4" /></span>
          <div><p className="font-semibold">在 Mac 获取配对码</p><p>设置 → 远程控制 → 配对新设备。</p></div>
        </div>
      </section>
    </main>
  );
};

