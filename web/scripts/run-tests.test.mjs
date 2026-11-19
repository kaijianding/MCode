// 验证 web/scripts/run-tests.mjs 的批量打包与并发执行行为。
// 运行：node web/scripts/run-tests.test.mjs
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const webDir = path.resolve(scriptsDir, '..');
const repoRoot = path.resolve(webDir, '..');
const srcDir = path.join(webDir, 'src');
const runnerPath = path.join(scriptsDir, 'run-tests.mjs');

function countTests(dir) {
  let count = 0;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'node_modules') continue;
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      count += countTests(fullPath);
    } else if (/\.test\.tsx?$/.test(entry.name)) {
      count += 1;
    }
  }
  return count;
}

const expectedCount = countTests(srcDir);
assert.ok(expectedCount > 0, 'web/src 下应存在至少一个测试文件');

// 从仓库根目录启动（cwd != webDir），同时覆盖 cwd: webDir、import.meta.url CSS 测试与并发上限配置。
const result = spawnSync(process.execPath, [runnerPath], {
  cwd: repoRoot,
  env: {
    ...process.env,
    MCODE_WEB_TEST_CONCURRENCY: '4'
  },
  encoding: 'utf8',
  timeout: 60_000
});

assert.equal(result.error, undefined, `启动 run-tests.mjs 失败：${result.error?.message}`);
assert.equal(
  result.status,
  0,
  `run-tests.mjs 退出码应为 0，实际为 ${result.status}\nstdout:\n${result.stdout}\nstderr:\n${result.stderr}`
);

const normalizedStdout = result.stdout.replaceAll('\\', '/');
const expectedSummary = `web 测试：${expectedCount} 通过，0 失败，共 ${expectedCount} 个`;
assert.ok(
  normalizedStdout.includes(expectedSummary),
  `汇总输出不匹配，期望包含 "${expectedSummary}"\n实际 stdout:\n${result.stdout}`
);

for (const requiredTest of [
  'src/tokenAliases.test.ts',
  'src/components/sheetLayerKeyboardCss.test.ts',
  'src/components/settings/settingsLayoutCss.test.ts',
  'src/components/sheet/sheetLayerGuard.test.ts'
]) {
  assert.ok(
    normalizedStdout.includes(`✓ ${requiredTest}`),
    `输出缺少关键测试通过行：${requiredTest}\n实际 stdout:\n${result.stdout}`
  );
}

console.log(`✓ run-tests.test.mjs 通过（MCODE_WEB_TEST_CONCURRENCY=4，共 ${expectedCount} 个测试）`);
