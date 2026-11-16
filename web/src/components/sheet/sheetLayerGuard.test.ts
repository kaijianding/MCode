// @ts-expect-error 测试运行时由 Node 提供，web 未安装 @types/node
import fs from 'node:fs';
// @ts-expect-error 测试运行时由 Node 提供，web 未安装 @types/node
import path from 'node:path';

const srcDir = path.resolve('src');
function findViolations(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry: { name: string; isDirectory(): boolean }) => {
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) return findViolations(file);
    if (!entry.name.endsWith('.tsx') || entry.name.endsWith('.test.tsx') || file === path.join(srcDir, 'components/sheet/SheetFrame.tsx')) return [];
    return fs.readFileSync(file, 'utf8').includes('sheet-layer') ? [path.relative(srcDir, file)] : [];
  });
}
const violations = findViolations(srcDir);
if (violations.length) throw new Error(`以下文件包含 sheet-layer：\n${violations.join('\n')}`);
