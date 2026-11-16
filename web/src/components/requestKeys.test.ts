import { questionShortcut } from './requestKeys';

const key = (value: string, metaKey = false) => ({ key: value, ctrlKey: false, metaKey, altKey: false });
if (questionShortcut(key('1'), false, false, 2) !== 0) throw new Error('1 应选择第一项');
if (questionShortcut(key('3'), false, false, 2) !== null) throw new Error('超出选项数应忽略');
if (questionShortcut(key('1', true), false, false, 2) !== null) throw new Error('Meta 组合键应忽略');
if (questionShortcut(key('1'), true, false, 2) !== null) throw new Error('输入法组合中应忽略');
if (questionShortcut(key('1'), false, true, 2) !== null) throw new Error('textarea 焦点应忽略');
if (questionShortcut(key('a'), false, false, 2) !== null) throw new Error('非数字键应忽略');
