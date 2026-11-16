import { shouldTakeOverComposer } from './composerTakeover';

if (shouldTakeOverComposer([])) throw new Error('空列表不应接管');
if (!shouldTakeOverComposer([{}])) throw new Error('有请求时应接管');
