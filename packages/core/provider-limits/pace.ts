import type { BucketHistoryPoint } from "./bucket-history";

export type PaceUnavailableReason =
  | "insufficient_points"
  | "stale_data"
  | "not_decreasing"
  | "zero_elapsed_time";

export interface PaceResult {
  available: boolean;
  reason?: PaceUnavailableReason;
  ratePerHour?: number;
  runwaySeconds?: number;
  windowStartedAt?: string;
  windowEndedAt?: string;
  sampleCount?: number;
  // Pace/runway are derived projections, never a provider-reported fact —
  // every result (available or not) carries this so callers can't present
  // it as authoritative.
  confidence: "estimated";
}

type UsablePoint = BucketHistoryPoint & { remaining: number };

function unavailable(reason: PaceUnavailableReason): PaceResult {
  return { available: false, reason, confidence: "estimated" };
}

function toMillis(iso: string): number {
  const value = Date.parse(iso);
  return Number.isNaN(value) ? 0 : value;
}

// Only "ok" bucket readings with a known remaining value are trustworthy
// enough to extrapolate from; partial/unavailable/error readings are
// skipped rather than treated as a zero-usage gap.
function usablePoints(points: BucketHistoryPoint[]): UsablePoint[] {
  return points
    .filter((point): point is UsablePoint => point.bucketStatus === "ok" && point.remaining !== null)
    .slice()
    .sort((a, b) => toMillis(a.checkedAt) - toMillis(b.checkedAt));
}

// Anchors on the most recent usable point and walks backward while the
// reset boundary (resets_at) and limit stay unchanged. A reset or a limit
// change makes earlier points a different window, so the walk stops there
// instead of blending pre- and post-reset usage into one rate.
function windowFromAnchor(points: UsablePoint[]): UsablePoint[] {
  if (points.length === 0) return [];
  const anchor = points[points.length - 1]!;
  const window = [anchor];
  for (let index = points.length - 2; index >= 0; index -= 1) {
    const point = points[index]!;
    if (point.resetsAt !== anchor.resetsAt || point.limit !== anchor.limit) break;
    window.unshift(point);
  }
  return window;
}

export function computeBucketPace(points: BucketHistoryPoint[]): PaceResult {
  const usable = usablePoints(points);
  if (usable.length === 0) return unavailable("insufficient_points");

  const anchor = usable[usable.length - 1]!;
  if (anchor.snapshotStale) return unavailable("stale_data");

  const window = windowFromAnchor(usable);
  if (window.length < 2) return unavailable("insufficient_points");

  const earliest = window[0]!;
  const elapsedHours = (toMillis(anchor.checkedAt) - toMillis(earliest.checkedAt)) / (60 * 60 * 1000);
  if (elapsedHours <= 0) return unavailable("zero_elapsed_time");

  const consumed = earliest.remaining - anchor.remaining;
  if (consumed <= 0) return unavailable("not_decreasing");

  const ratePerHour = consumed / elapsedHours;

  return {
    available: true,
    ratePerHour,
    runwaySeconds: (anchor.remaining / ratePerHour) * 3600,
    windowStartedAt: earliest.checkedAt,
    windowEndedAt: anchor.checkedAt,
    sampleCount: window.length,
    confidence: "estimated",
  };
}
