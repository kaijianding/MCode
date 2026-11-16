export function shouldTakeOverComposer(requests: readonly unknown[]): boolean {
  return requests.length > 0;
}
