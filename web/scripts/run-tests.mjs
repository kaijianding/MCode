// web 测试运行器：web/src 下的测试不使用测试框架，这里用 esbuild 批量打包后并发执行。
// 用法：node scripts/run-tests.mjs（或 npm test）。
import { build } from 'esbuild';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const srcDir = path.join(webDir, 'src');

function collectTests(dir) {
  const tests = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'node_modules') continue;
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      tests.push(...collectTests(fullPath));
    } else if (/\.test\.tsx?$/.test(entry.name)) {
      tests.push(fullPath);
    }
  }
  return tests.sort();
}

// 只支持 Vite import.meta.glob 的常见形式：目录固定，文件名用 * / ? 通配。
function expandGlob(pattern) {
  const dir = path.dirname(pattern);
  const base = path.basename(pattern);
  if (/[*?]/.test(dir)) {
    throw new Error(`暂不支持带通配符的目录：${pattern}`);
  }
  const source = base.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '[^/]*').replace(/\?/g, '[^/]');
  const matcher = new RegExp(`^${source}$`);
  return fs
    .readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isFile() && matcher.test(entry.name))
    .map((entry) => path.join(dir, entry.name))
    .sort();
}

// Vite 的 import.meta.glob 由 Vite 编译，esbuild 不认；这里把它展开成静态对象。
// 多入口打包不能全局 define import.meta.url，这里在同一个 onLoad 里按 args.path 各自替换，避免多个 onLoad 短路。
const importMetaGlobPlugin = {
  name: 'import-meta-glob',
  setup(buildContext) {
    buildContext.onLoad({ filter: /\.(ts|tsx)$/ }, async (args) => {
      let contents = await fs.promises.readFile(args.path, 'utf8');
      const hasGlob = contents.includes('import.meta.glob');
      const hasUrl = contents.includes('import.meta.url');
      if (!hasGlob && !hasUrl) return undefined;

      let replaced = false;
      if (hasGlob) {
        const sourceDir = path.dirname(args.path);
        contents = contents.replace(
          /import\.meta\.glob(?:<[^>]*>)?\(\s*(['"])([^'"]+)\1\s*(?:,\s*\{[^}]*\})?\s*\)/g,
          (_match, _quote, pattern) => {
            replaced = true;
            const entries = expandGlob(path.resolve(sourceDir, pattern)).map((file) => {
              let key = path.relative(sourceDir, file).split(path.sep).join('/');
              if (!key.startsWith('.')) key = `./${key}`;
              // query: '?url' 的返回值；测试只关心图标名与来源，不解析真实资源。
              const url = `/${path.relative(webDir, file).split(path.sep).join('/')}`;
              return `${JSON.stringify(key)}: ${JSON.stringify(url)}`;
            });
            return `{ ${entries.join(', ')} }`;
          }
        );
      }

      if (hasUrl) {
        const fileUrlLiteral = JSON.stringify(pathToFileURL(args.path).href);
        contents = contents.replace(/\bimport\.meta\.url\b/g, () => {
          replaced = true;
          return fileUrlLiteral;
        });
      }

      // 只有注释或其它形式里未实际匹配到的文件不需要改写。
      if (!replaced) return undefined;
      return { contents, loader: args.path.endsWith('.tsx') ? 'tsx' : 'ts' };
    });
  }
};

// 测试实际用到的浏览器桩：window 与动画帧。不引入 jsdom。
// react-dom/server 内部会 require('stream')，打包成 ESM 后需要补一个 createRequire。
const browserStubBanner = `
import { createRequire as __mcodeCreateRequire } from 'node:module';
const require = __mcodeCreateRequire(import.meta.url);
if (typeof globalThis.window === 'undefined') {
  globalThis.window = globalThis;
}
if (typeof globalThis.window.location === 'undefined') {
  globalThis.window.location = { origin: 'http://localhost', href: 'http://localhost/' };
}
if (typeof globalThis.requestAnimationFrame === 'undefined') {
  globalThis.requestAnimationFrame = (callback) => setTimeout(() => callback(Date.now()), 0);
  globalThis.cancelAnimationFrame = (handle) => clearTimeout(handle);
}
`;

const TEST_TIMEOUT_MS = 30_000;

function resolveConcurrency() {
  const raw = process.env.MCODE_WEB_TEST_CONCURRENCY;
  if (raw !== undefined && raw !== '') {
    const parsed = Number.parseInt(raw, 10);
    if (Number.isFinite(parsed) && parsed >= 1) {
      return parsed;
    }
  }
  return Math.max(1, Math.min(os.cpus().length, 8));
}

async function bundleTests(tests, tmpDir) {
  const entryPoints = {};
  const entries = tests.map((testFile) => {
    const label = path.relative(webDir, testFile);
    const relFromSrc = path.relative(srcDir, testFile).split(path.sep).join('/');
    entryPoints[relFromSrc] = testFile;
    return {
      label,
      outfile: path.join(tmpDir, `${relFromSrc}.mjs`)
    };
  });

  await build({
    entryPoints,
    outdir: tmpDir,
    outExtension: { '.js': '.mjs' },
    bundle: true,
    platform: 'node',
    format: 'esm',
    jsx: 'automatic',
    target: 'node20',
    logLevel: 'silent',
    banner: { js: browserStubBanner },
    define: {
      'import.meta.env.BASE_URL': JSON.stringify('/')
    },
    // CSS 与图片只被打包进依赖图，测试本身不读取它们。
    loader: {
      '.css': 'empty',
      '.svg': 'empty',
      '.png': 'empty',
      '.jpg': 'empty',
      '.jpeg': 'empty',
      '.webp': 'empty',
      '.gif': 'empty',
      '.woff': 'empty',
      '.woff2': 'empty',
      '.ttf': 'empty'
    },
    plugins: [importMetaGlobPlugin]
  });

  return entries;
}

function runTestProcess(outfile) {
  return new Promise((resolve) => {
    const startedAt = Date.now();
    const child = spawn(process.execPath, [outfile], {
      cwd: webDir,
      stdio: ['ignore', 'pipe', 'pipe']
    });
    const stdoutChunks = [];
    const stderrChunks = [];
    let timedOut = false;
    let spawnError = null;
    let settled = false;
    let killEscalationTimer = null;

    child.stdout?.on('data', (chunk) => stdoutChunks.push(chunk));
    child.stderr?.on('data', (chunk) => stderrChunks.push(chunk));

    const timeoutTimer = setTimeout(() => {
      timedOut = true;
      child.kill('SIGTERM');
      killEscalationTimer = setTimeout(() => {
        child.kill('SIGKILL');
      }, 1000);
      killEscalationTimer.unref?.();
    }, TEST_TIMEOUT_MS);
    timeoutTimer.unref?.();

    const finish = (code, signal) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeoutTimer);
      if (killEscalationTimer) clearTimeout(killEscalationTimer);
      resolve({
        elapsed: Date.now() - startedAt,
        code,
        signal,
        timedOut,
        error: spawnError,
        stdout: Buffer.concat(stdoutChunks),
        stderr: Buffer.concat(stderrChunks)
      });
    };

    child.on('error', (error) => {
      spawnError = error;
      finish(null, null);
    });
    child.on('close', (code, signal) => {
      finish(code, signal);
    });
  });
}

async function run() {
  const tests = collectTests(srcDir);
  if (tests.length === 0) {
    console.error('未找到 web/src 下的 *.test.ts / *.test.tsx');
    process.exit(1);
  }

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mcode-web-tests-'));
  const failuresByIndex = new Array(tests.length);

  try {
    let entries;
    try {
      entries = await bundleTests(tests, tmpDir);
    } catch (error) {
      console.error(`web 测试打包失败：${error instanceof Error ? error.message : String(error)}`);
      process.exitCode = 1;
      return;
    }

    const concurrency = Math.min(resolveConcurrency(), entries.length);
    let nextIndex = 0;

    async function worker() {
      while (nextIndex < entries.length) {
        const currentIndex = nextIndex;
        nextIndex += 1;
        const { label, outfile } = entries[currentIndex];
        const result = await runTestProcess(outfile);

        if (result.stdout.length > 0) process.stdout.write(result.stdout);
        if (result.stderr.length > 0) process.stderr.write(result.stderr);

        if (result.timedOut) {
          const reason = '执行超时';
          failuresByIndex[currentIndex] = { label, reason };
          console.log(`✗ ${label}（${reason}，${result.elapsed}ms）`);
        } else if (result.error) {
          const reason = `执行失败：${result.error.message}`;
          failuresByIndex[currentIndex] = { label, reason };
          console.log(`✗ ${label}（${reason}，${result.elapsed}ms）`);
        } else if (result.code !== 0) {
          const reason = `退出码 ${result.code ?? result.signal}`;
          failuresByIndex[currentIndex] = { label, reason };
          console.log(`✗ ${label}（${result.elapsed}ms）`);
        } else {
          console.log(`✓ ${label}（${result.elapsed}ms）`);
        }
      }
    }

    await Promise.all(Array.from({ length: concurrency }, () => worker()));
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  const failed = failuresByIndex.filter(Boolean);
  const passed = tests.length - failed.length;
  console.log(`\nweb 测试：${passed} 通过，${failed.length} 失败，共 ${tests.length} 个`);
  if (failed.length > 0) {
    for (const item of failed) {
      console.log(`  ${item.label}：${item.reason}`);
    }
    process.exit(1);
  }
}

run().catch((error) => {
  console.error(error);
  process.exit(1);
});
