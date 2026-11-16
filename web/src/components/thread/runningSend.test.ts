import { runningSendParams } from './runningSend';

if (Object.keys(runningSendParams(true, false)).length !== 0) throw new Error('运行中的普通会话应省略 whenRunning');
if (runningSendParams(true, true).whenRunning !== 'queue') throw new Error('运行中的总管会话应保持 queue');
if (Object.keys(runningSendParams(false, true)).length !== 0) throw new Error('未运行时应省略 whenRunning');
