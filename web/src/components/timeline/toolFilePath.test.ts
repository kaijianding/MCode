import { toolFilePath } from './toolFilePath';

for (const key of ['file_path', 'filePath', 'path', 'notebook_path']) {
  if (toolFilePath('Read', JSON.stringify({ [key]: '/src/file.ts' })) !== '/src/file.ts') throw new Error(`未解析 ${key}`);
}
if (toolFilePath('Read', { file_path: '', path: 'file.ts' }) !== 'file.ts') throw new Error('应跳过空路径');
for (const input of ['not json', '', '{}', { path: '   ' }]) {
  if (toolFilePath('Read', input) !== null) throw new Error('无效路径应返回 null');
}
if (toolFilePath('Grep', { path: 'src' }) !== null) throw new Error('搜索类工具不应返回文件路径');
