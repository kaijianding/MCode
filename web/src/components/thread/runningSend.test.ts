import { runningSendParams, runningSubmitLabel } from './runningSend';

if (Object.keys(runningSendParams(true, false)).length !== 0) throw new Error('运行中的普通会话应省略 whenRunning');
if (runningSendParams(true, true).whenRunning !== 'queue') throw new Error('运行中的总管会话应保持 queue');
if (Object.keys(runningSendParams(false, true)).length !== 0) throw new Error('未运行时应省略 whenRunning');

if (runningSubmitLabel(false, false, true) !== '发送') throw new Error('未运行时应显示发送');
if (runningSubmitLabel(true, true, true) !== '发送') throw new Error('总管会话应显示发送');
if (runningSubmitLabel(true, false, true) !== '插入') throw new Error('开启插入时应显示插入');
if (runningSubmitLabel(true, false, false) !== '排队') throw new Error('关闭插入时应显示排队');
