import React, { useEffect, useRef } from 'react';

export const SHEET_MENU_CARD_CLASS = 'thread-action-sheet';
export const SHEET_PANEL_CARD_CLASS =
  'settings-sheet flex flex-col max-h-[92dvh] outline-none w-full max-w-full min-w-0 overflow-hidden';

export function pushSheet<T>(stack: readonly T[], item: T): T[] { return [...stack, item]; }
export function removeSheet<T>(stack: readonly T[], item: T): T[] { return stack.filter((entry) => entry !== item); }
export function isTopSheet<T>(stack: readonly T[], item: T): boolean { return stack[stack.length - 1] === item; }
export function nextFocusIndex(index: number, count: number, backwards: boolean): number {
  if (count === 0) return -1;
  if (index < 0 || index >= count) return backwards ? count - 1 : 0;
  return (index + (backwards ? -1 : 1) + count) % count;
}

let sheetStack: object[] = [];
const focusableSelector = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export interface SheetFrameProps {
  onRequestClose: () => void;
  labelledBy?: string;
  size?: 'menu' | 'panel';
  cardClassName?: string;
  layerClassName?: string;
  role?: 'dialog' | 'alertdialog';
  closeOnEscape?: boolean;
  children: React.ReactNode;
}

export function SheetFrame({ onRequestClose, labelledBy, size = 'panel', cardClassName, layerClassName, role = 'dialog', closeOnEscape = true, children }: SheetFrameProps) {
  const cardRef = useRef<HTMLElement>(null);
  const identity = useRef({});
  const closeRef = useRef(onRequestClose);
  const escapeRef = useRef(closeOnEscape);
  closeRef.current = onRequestClose;
  escapeRef.current = closeOnEscape;

  useEffect(() => {
    const card = cardRef.current;
    if (!card) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const item = identity.current;
    sheetStack = pushSheet(sheetStack, item);
    const focusable = () => Array.from(card.querySelectorAll<HTMLElement>(focusableSelector)).filter((element) => element.getClientRects().length > 0);
    if (!card.contains(document.activeElement)) (focusable()[0] ?? card).focus();
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isTopSheet(sheetStack, item)) return;
      if (event.key === 'Escape' && escapeRef.current) {
        event.stopPropagation();
        closeRef.current();
      } else if (event.key === 'Tab') {
        const elements = focusable();
        const next = nextFocusIndex(elements.indexOf(document.activeElement as HTMLElement), elements.length, event.shiftKey);
        event.preventDefault();
        (elements[next] ?? card).focus();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      sheetStack = removeSheet(sheetStack, item);
      if (previous?.isConnected) previous.focus();
    };
  }, []);

  const cardClass = [size === 'menu' ? SHEET_MENU_CARD_CLASS : SHEET_PANEL_CARD_CLASS, cardClassName].filter(Boolean).join(' ');
  return (
    <div className={['sheet-layer', layerClassName].filter(Boolean).join(' ')} role="presentation" onMouseDown={onRequestClose}>
      <section ref={cardRef} className={cardClass} role={role} aria-modal="true" aria-labelledby={labelledBy} tabIndex={-1} onMouseDown={(event) => event.stopPropagation()}>
        {children}
      </section>
    </div>
  );
}
