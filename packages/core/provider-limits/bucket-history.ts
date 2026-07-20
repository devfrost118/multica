import type { ProviderLimitBucket, ProviderLimitSnapshot } from "../types";

export interface BucketHistoryPoint {
  checkedAt: string;
  remaining: number | null;
  limit: number | null;
  used: number | null;
  unit: string;
  resetsAt: string | null;
  bucketStatus: string;
  snapshotStale: boolean;
}

export interface BucketOption {
  id: string;
  label: string;
  unit: string;
}

function toMillis(iso: string): number {
  const value = Date.parse(iso);
  return Number.isNaN(value) ? 0 : value;
}

// remaining_value is the provider fact when present. Buckets that only
// report limit/used still let us derive remaining, but only when the limit
// is a real cap — a zero or missing limit means "unknown", not "zero left".
export function deriveRemaining(bucket: ProviderLimitBucket): number | null {
  if (bucket.remaining_value !== null) return bucket.remaining_value;
  if (bucket.limit_value !== null && bucket.used_value !== null && bucket.limit_value > 0) {
    return bucket.limit_value - bucket.used_value;
  }
  return null;
}

export function selectBucketHistory(
  snapshots: ProviderLimitSnapshot[],
  provider: string,
  accountKey: string,
  bucketId: string,
): BucketHistoryPoint[] {
  const points: BucketHistoryPoint[] = [];
  for (const snapshot of snapshots) {
    if (snapshot.provider !== provider || snapshot.account_key !== accountKey) continue;
    const bucket = snapshot.buckets.find((candidate) => candidate.id === bucketId);
    if (!bucket) continue;
    points.push({
      checkedAt: snapshot.checked_at,
      remaining: deriveRemaining(bucket),
      limit: bucket.limit_value,
      used: bucket.used_value,
      unit: bucket.unit,
      resetsAt: bucket.resets_at,
      bucketStatus: bucket.status,
      snapshotStale: snapshot.stale,
    });
  }
  return points.sort((a, b) => toMillis(a.checkedAt) - toMillis(b.checkedAt));
}

export function listBucketOptions(
  snapshots: ProviderLimitSnapshot[],
  provider: string,
  accountKey: string,
): BucketOption[] {
  const latestById = new Map<string, { checkedAt: string; option: BucketOption }>();
  for (const snapshot of snapshots) {
    if (snapshot.provider !== provider || snapshot.account_key !== accountKey) continue;
    for (const bucket of snapshot.buckets) {
      const existing = latestById.get(bucket.id);
      if (!existing || toMillis(snapshot.checked_at) > toMillis(existing.checkedAt)) {
        latestById.set(bucket.id, {
          checkedAt: snapshot.checked_at,
          option: { id: bucket.id, label: bucket.label, unit: bucket.unit },
        });
      }
    }
  }
  return [...latestById.values()].map(({ option }) => option);
}
