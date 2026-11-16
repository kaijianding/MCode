import { toolFilePath } from './toolFilePath';

for (const key of ['file_path', 'filePath', 'path', 'notebook_path']) {
  if (toolFilePath('read', JSON.stringify({ [key]: '/src/file.ts' })) !== '/src/file.ts') throw new Error(`未解析 ${key}`);
}
if (toolFilePath('read', { file_path: '', path: 'file.ts' }) !== 'file.ts') throw new Error('应跳过空路径');
for (const input of ['not json', '', '{}', { path: '   ' }]) {
  if (toolFilePath('read', input) !== null) throw new Error('无效路径应返回 null');
}
