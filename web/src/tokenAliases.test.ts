// @ts-expect-error web/ 未安装 @types/node，测试运行时由 node 提供 node:fs
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('./index.css', import.meta.url), 'utf8');
const tokens = readFileSync(new URL('./tokens.css', import.meta.url), 'utf8');
const aliases = [
  ['screen', 'surface-main'],
  ['card', 'surface-card'],
  ['card-alt', 'surface-main'],
  ['subtle-strong', 'surface-main'],
  ['quota-mid', 'caution'],
  ['row-hover', 'surface-canvas'],
  ['quota-low', 'success'],
  ['quota-high', 'warning'],
];

for (const [name, token] of aliases) {
  if (!css.includes(`--${name}: var(--mc-${token});`)) {
    throw new Error(`缺少 token 别名 --${name}: --mc-${token}`);
  }
  if (!new RegExp(`--mc-${token}\\s*:`).test(tokens)) {
    throw new Error(`缺少 token --mc-${token}`);
  }
}
