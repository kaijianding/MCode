// 设置页的展示组件：小节卡片、行与堆叠行。
// 与桌面版 internal/ui/settings_pages.go 的 renderSettingsSection / renderSettingsRow /
// renderSettingsStackedRow 对应，样式全部在 index.css 里。

import React from 'react';

interface SettingsSectionProps {
  title?: string;
  children: React.ReactNode;
  className?: string;
}

// SettingsSection 渲染小节标题加卡片。
export const SettingsSection: React.FC<SettingsSectionProps> = ({ title, children, className }) => (
  <section className={`settings-section-block${className ? ` ${className}` : ''}`}>
    {title ? <h3 className="settings-section-title">{title}</h3> : null}
    <div className="settings-card">{children}</div>
  </section>
);

interface SettingsRowProps {
  itemID?: string;
  title?: React.ReactNode;
  desc?: React.ReactNode;
  control?: React.ReactNode;
  // 控件之下、整行内部的额外内容（例如说明性文字或按钮组）。
  children?: React.ReactNode;
  error?: string | null;
  leading?: React.ReactNode;
}

// SettingsRow 是卡片里的一行：左边标题和说明，右边控件。
export const SettingsRow: React.FC<SettingsRowProps> = ({
  itemID,
  title,
  desc,
  control,
  children,
  error,
  leading
}) => (
  <div className="settings-row" data-settings-item={itemID}>
    {leading ? <div className="settings-row-leading">{leading}</div> : null}
    <div className="settings-row-main">
      {title ? <div className="settings-row-title">{title}</div> : null}
      {desc ? <div className="settings-row-desc">{desc}</div> : null}
      {error ? <div className="settings-row-error" role="alert">{error}</div> : null}
      {children}
    </div>
    {control ? <div className="settings-row-control">{control}</div> : null}
  </div>
);

interface SettingsStackedRowProps {
  itemID?: string;
  title?: React.ReactNode;
  desc?: React.ReactNode;
  children?: React.ReactNode;
  error?: string | null;
}

// SettingsStackedRow 用于文本、数字、时间输入等较宽的控件：标题和说明在上，控件在下。
export const SettingsStackedRow: React.FC<SettingsStackedRowProps> = ({
  itemID,
  title,
  desc,
  children,
  error
}) => (
  <div className="settings-row settings-row--stacked" data-settings-item={itemID}>
    <div className="settings-row-main">
      {title ? <div className="settings-row-title">{title}</div> : null}
      {desc ? <div className="settings-row-desc">{desc}</div> : null}
      {error ? <div className="settings-row-error" role="alert">{error}</div> : null}
      {children ? <div className="settings-row-stacked-control">{children}</div> : null}
    </div>
  </div>
);

interface SettingsSwitchProps {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  label: string;
}

// SettingsSwitch 是行右侧的开关（Web 端此前没有开关组件）。
export const SettingsSwitch: React.FC<SettingsSwitchProps> = ({
  checked,
  onChange,
  disabled,
  label
}) => (
  <button
    type="button"
    role="switch"
    aria-checked={checked}
    aria-label={label}
    disabled={disabled}
    className={`settings-switch${checked ? ' is-on' : ''}`}
    onClick={() => onChange(!checked)}
  >
    <span className="settings-switch-knob" />
  </button>
);
