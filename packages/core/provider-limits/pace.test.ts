import { describe, expect, it } from "vitest";
import type { BucketHistoryPoint } from "./bucket-history";
import { computeBucketPace } from "./pace";

function point(overrides: Partial<BucketHistoryPoint> = {}): BucketHistoryPoint {
  return {
    checkedAt: "2026-07-19T10:00:00Z",
    remaining: 70,
    limit: 100,
    used: 30,
    unit: "percent",
    resetsAt: "2026-07-19T15:00:00Z",
    bucketStatus: "ok",
    snapshotStale: false,
    ...overrides,
  };
}

describe("computeBucketPace", () => {
  it("is unavailable with a single snapshot", () => {
    const result = computeBucketPace([point()]);
    expect(result).toEqual({ available: false, reason: "insufficient_points", confidence: "estimated" });
  });

  it("is unavailable when there are no usable points at all", () => {
    const result = computeBucketPace([]);
    expect(result).toEqual({ available: false, reason: "insufficient_points", confidence: "estimated" });
  });

  it("computes rate and runway from two decreasing points in the same window", () => {
    const result = computeBucketPace([
      point({ checkedAt: "2026-07-19T10:00:00Z", remaining: 80 }),
      point({ checkedAt: "2026-07-19T12:00:00Z", remaining: 60 }),
    ]);

    expect(result.available).toBe(true);
    expect(result.confidence).toBe("estimated");
    // 20 units consumed over 2 hours.
    expect(result.ratePerHour).toBeCloseTo(10);
    // 60 remaining at 10/hour -> 6 hours -> 21600 seconds.
    expect(result.runwaySeconds).toBeCloseTo(21_600);
    expect(result.sampleCount).toBe(2);
  });

  it("stops the window at a reset boundary instead of blending pre/post-reset usage", () => {
    const points = [
      point({ checkedAt: "2026-07-19T08:00:00Z", remaining: 10, resetsAt: "2026-07-19T09:00:00Z" }),
      point({ checkedAt: "2026-07-19T08:30:00Z", remaining: 5, resetsAt: "2026-07-19T09:00:00Z" }),
      point({ checkedAt: "2026-07-19T09:05:00Z", remaining: 100, resetsAt: "2026-07-19T14:00:00Z" }),
      point({ checkedAt: "2026-07-19T11:05:00Z", remaining: 80, resetsAt: "2026-07-19T14:00:00Z" }),
    ];

    const result = computeBucketPace(points);

    expect(result.available).toBe(true);
    expect(result.windowStartedAt).toBe("2026-07-19T09:05:00Z");
    expect(result.windowEndedAt).toBe("2026-07-19T11:05:00Z");
    expect(result.sampleCount).toBe(2);
    // 20 units consumed over 2 hours in the post-reset window only.
    expect(result.ratePerHour).toBeCloseTo(10);
  });

  it("skips non-ok bucket readings instead of treating the gap as zero usage", () => {
    const points = [
      point({ checkedAt: "2026-07-19T08:00:00Z", remaining: 90 }),
      point({ checkedAt: "2026-07-19T09:00:00Z", remaining: null, bucketStatus: "unavailable" }),
      point({ checkedAt: "2026-07-19T10:00:00Z", remaining: 70 }),
    ];

    const result = computeBucketPace(points);

    expect(result.available).toBe(true);
    expect(result.sampleCount).toBe(2);
    expect(result.windowStartedAt).toBe("2026-07-19T08:00:00Z");
    expect(result.windowEndedAt).toBe("2026-07-19T10:00:00Z");
    expect(result.ratePerHour).toBeCloseTo(10);
  });

  it("is unavailable when the latest reading is stale", () => {
    const result = computeBucketPace([
      point({ checkedAt: "2026-07-19T08:00:00Z", remaining: 80 }),
      point({ checkedAt: "2026-07-19T10:00:00Z", remaining: 60, snapshotStale: true }),
    ]);

    expect(result).toEqual({ available: false, reason: "stale_data", confidence: "estimated" });
  });

  it("is unavailable when remaining is flat or increasing (not net decreasing)", () => {
    const result = computeBucketPace([
      point({ checkedAt: "2026-07-19T08:00:00Z", remaining: 60 }),
      point({ checkedAt: "2026-07-19T10:00:00Z", remaining: 90 }),
    ]);

    expect(result).toEqual({ available: false, reason: "not_decreasing", confidence: "estimated" });
  });

  it("is unavailable when the window's elapsed time is zero (division-by-zero guard)", () => {
    const result = computeBucketPace([
      point({ checkedAt: "2026-07-19T10:00:00Z", remaining: 80 }),
      point({ checkedAt: "2026-07-19T10:00:00Z", remaining: 60 }),
    ]);

    expect(result).toEqual({ available: false, reason: "zero_elapsed_time", confidence: "estimated" });
  });
});
