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
const CLAUDE_CANONICAL_IDS = ["session", "weekly_all", "weekly_scoped"] as const;

const ANTIGRAVITY_FAMILY_IDS = ["session_claude", "session_gemini"] as const;
const ANTIGRAVITY_LEGACY_SESSION = "session";

const LEGACY_LIMIT_PREFIX = "limit-";

function canonicalBucketId(id: string): string {
  return id.startsWith(LEGACY_LIMIT_PREFIX) ? id.slice(LEGACY_LIMIT_PREFIX.length) : id;
}

function indexCanonicalBuckets(
  buckets: readonly ProviderLimitBucket[],
  allowedIds: readonly string[],
): Map<string, ProviderLimitBucket> {
  const byId = new Map<string, ProviderLimitBucket>();
  for (const bucket of buckets) {
    const id = canonicalBucketId(bucket.id);
    if (!allowedIds.includes(id) || byId.has(id)) continue;
    byId.set(id, bucket.id === id ? bucket : { ...bucket, id });
  }
  return byId;
}

function selectFixedCanonicalBuckets(
  buckets: readonly ProviderLimitBucket[],
  canonicalIds: readonly string[],
): ProviderLimitBucket[] {
  const byId = indexCanonicalBuckets(buckets, canonicalIds);
  return canonicalIds.flatMap((id) => {
    const bucket = byId.get(id);
    return bucket ? [bucket] : [];
  });
}

function hasAntigravityFamilyBucket(buckets: readonly ProviderLimitBucket[]): boolean {
  return buckets.some((bucket) =>
    (ANTIGRAVITY_FAMILY_IDS as readonly string[]).includes(canonicalBucketId(bucket.id)),
  );
}

function antigravityAccountKey(snapshot: ProviderLimitSnapshot): string {
  return `${snapshot.provider}:${snapshot.account_key}`;
}

// Family ids are the stable contract. Legacy "session" is only a fallback for
// old-daemon rows that never saw a family reading; once any family bucket is
// present for the same snapshot, session is dropped so it cannot become a
// third series.
function selectAntigravityBuckets(
  buckets: readonly ProviderLimitBucket[],
  hideLegacySession: boolean,
): ProviderLimitBucket[] {
  const allowedIds = hideLegacySession
    ? ANTIGRAVITY_FAMILY_IDS
    : ([...ANTIGRAVITY_FAMILY_IDS, ANTIGRAVITY_LEGACY_SESSION] as const);
  const byId = indexCanonicalBuckets(buckets, allowedIds);
  const families = ANTIGRAVITY_FAMILY_IDS.flatMap((id) => {
    const bucket = byId.get(id);
    return bucket ? [bucket] : [];
  });
  if (families.length > 0) {
    return families;
  }
  if (hideLegacySession) {
    return [];
  }
  const legacy = byId.get(ANTIGRAVITY_LEGACY_SESSION);
  return legacy ? [legacy] : [];
}

// Returns the buckets a provider is allowed to display, in canonical display
// order. Unknown ids are dropped and the first bucket claiming an id wins.
export function selectCanonicalBuckets(
  provider: string,
  buckets: readonly ProviderLimitBucket[],
): ProviderLimitBucket[] {
  if (provider === "claude") {
    return selectFixedCanonicalBuckets(buckets, CLAUDE_CANONICAL_IDS);
  }
  if (provider === "antigravity") {
    return selectAntigravityBuckets(buckets, false);
  }
  return [...buckets];
}

// Applies selectCanonicalBuckets across a snapshot list so the overview, its
// cards, and the detail dialog all read the same bucket set. Snapshots of
// providers without a canonical set are returned by reference. For Antigravity,
// legacy "session" is suppressed for an account once any snapshot in the batch
// already carries family buckets — otherwise history/detail grows a third tab.
export function withCanonicalBuckets(
  snapshots: readonly ProviderLimitSnapshot[],
): ProviderLimitSnapshot[] {
  const antigravityAccountsWithFamilies = new Set<string>();
  for (const snapshot of snapshots) {
    if (snapshot.provider !== "antigravity") continue;
    if (hasAntigravityFamilyBucket(snapshot.buckets)) {
      antigravityAccountsWithFamilies.add(antigravityAccountKey(snapshot));
    }
  }

  return snapshots.map((snapshot) => {
    if (snapshot.provider === "claude") {
      return { ...snapshot, buckets: selectCanonicalBuckets(snapshot.provider, snapshot.buckets) };
    }
    if (snapshot.provider === "antigravity") {
      const hideLegacy = antigravityAccountsWithFamilies.has(antigravityAccountKey(snapshot));
      return {
        ...snapshot,
        buckets: selectAntigravityBuckets(snapshot.buckets, hideLegacy),
      };
    }
    return snapshot;
  });
}
