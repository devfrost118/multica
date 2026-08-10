import type { ProviderLimitBucket, ProviderLimitSnapshot } from "../types";

// Stored snapshots are a version boundary: a daemon installed on a machine can
// be older than the app reading its rows, and history keeps whatever every past
// build wrote. Claude is the case that bites — earlier builds emitted the three
// real quotas under "limit-"-prefixed ids alongside five_hour / seven_day /
// spend windows that duplicate or dilute them, so a Claude card fed straight
// from a row can show six bars. Antigravity has the same shape for a different
// reason: it reported one "session" bucket until a controlled quota check
// showed the Claude/GPT and Gemini pools drain independently. Providers absent
// from this map are passed through untouched.
const CANONICAL_BUCKET_IDS: Record<string, readonly string[]> = {
  claude: ["session", "weekly_all", "weekly_scoped"],
  antigravity: ["session_claude", "session_gemini"],
};

const LEGACY_LIMIT_PREFIX = "limit-";

function canonicalBucketId(id: string): string {
  return id.startsWith(LEGACY_LIMIT_PREFIX) ? id.slice(LEGACY_LIMIT_PREFIX.length) : id;
}

// Returns the buckets a provider is allowed to display, in canonical display
// order. Unknown ids are dropped and the first bucket claiming an id wins.
export function selectCanonicalBuckets(
  provider: string,
  buckets: readonly ProviderLimitBucket[],
): ProviderLimitBucket[] {
  const canonicalIds = CANONICAL_BUCKET_IDS[provider];
  if (!canonicalIds) return [...buckets];

  const byId = new Map<string, ProviderLimitBucket>();
  for (const bucket of buckets) {
    const id = canonicalBucketId(bucket.id);
    if (!canonicalIds.includes(id) || byId.has(id)) continue;
    byId.set(id, bucket.id === id ? bucket : { ...bucket, id });
  }
  return canonicalIds.flatMap((id) => {
    const bucket = byId.get(id);
    return bucket ? [bucket] : [];
  });
}

// Applies selectCanonicalBuckets across a snapshot list so the overview, its
// cards, and the detail dialog all read the same bucket set. Snapshots of
// providers without a canonical set are returned by reference.
export function withCanonicalBuckets(
  snapshots: readonly ProviderLimitSnapshot[],
): ProviderLimitSnapshot[] {
  return snapshots.map((snapshot) =>
    CANONICAL_BUCKET_IDS[snapshot.provider]
      ? { ...snapshot, buckets: selectCanonicalBuckets(snapshot.provider, snapshot.buckets) }
      : snapshot,
  );
}
