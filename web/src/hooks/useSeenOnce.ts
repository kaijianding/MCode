import React from 'react';

export function observeUntilSeen(element: Element, rootMargin: string, onSeen: () => void): () => void {
  const observer = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) {
      observer.disconnect();
      onSeen();
    }
  }, { rootMargin });
  observer.observe(element);
  return () => observer.disconnect();
}

export function useSeenOnce(ref: React.RefObject<Element | null>, rootMargin = '200px'): boolean {
  const [seen, setSeen] = React.useState(() => typeof IntersectionObserver === 'undefined');

  React.useEffect(() => {
    if (seen || !ref.current) return;
    return observeUntilSeen(ref.current, rootMargin, () => setSeen(true));
  }, [ref, rootMargin, seen]);

  return seen;
}
