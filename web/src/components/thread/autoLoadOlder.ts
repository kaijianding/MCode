export function shouldAutoLoadOlder({ intersecting, hasMore, loading, failed }: {
  intersecting: boolean;
  hasMore: boolean;
  loading: boolean;
  failed: boolean;
}): boolean {
  return intersecting && hasMore && !loading && !failed;
}
