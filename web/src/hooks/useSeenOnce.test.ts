import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { observeUntilSeen, useSeenOnce } from './useSeenOnce';

function Probe() {
  const ref = React.useRef<Element>(null);
  return React.createElement('span', null, String(useSeenOnce(ref)));
}

if (renderToStaticMarkup(React.createElement(Probe)) !== '<span>true</span>') {
  throw new Error('缺少 IntersectionObserver 时应立即视为可见');
}

let callback: IntersectionObserverCallback | undefined;
let disconnected = false;
let observed: Element | undefined;
class FakeObserver {
  constructor(cb: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    callback = cb;
    if (options?.rootMargin !== '200px') throw new Error('rootMargin 未传递');
  }
  observe(element: Element) { observed = element; }
  disconnect() { disconnected = true; }
}
globalThis.IntersectionObserver = FakeObserver as unknown as typeof IntersectionObserver;

if (renderToStaticMarkup(React.createElement(Probe)) !== '<span>false</span>') {
  throw new Error('支持 IntersectionObserver 时初始应不可见');
}
const element = {} as Element;
let seen = false;
observeUntilSeen(element, '200px', () => { seen = true; });
if (observed !== element || !callback) throw new Error('应观察目标元素');
callback([{ isIntersecting: false } as IntersectionObserverEntry], {} as IntersectionObserver);
if (seen || disconnected) throw new Error('未相交时应继续观察');
callback([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver);
if (!seen || !disconnected) throw new Error('相交后应标记可见并断开观察');
