import { describe, expect, it } from "vitest";
import type { ProviderLimitSnapshot } from "../types";
import { deriveRemaining, listBucketOptions, selectBucketHistory } from "./bucket-history";

function snapshot(overrides: Partial<ProviderLimitSnapshot> = {}): ProviderLimitSnapshot {
  return {
    runtime_id: "daemon-1",
    provider: "claude",
    account_key: "account-a",
    account_label: "Shared account",
    checked_at: "2026-07-19T10:00:00Z",
    status: "ok",
    source: { kind: "official_api", freshness_seconds: 900, confidence: "official" },
    buckets: [
      {
        id: "session",
        label: "Session",
        unit: "percent",
        limit_value: 100,
        used_value: 30,
        remaining_value: 70,
        resets_at: "2026-07-19T15:00:00Z",
        status: "ok",
        note: "",
      },
    ],
    error_note: "",
    stale: false,
    ...overrides,
  };
}

describe("deriveRemaining", () => {
  it("prefers the reported remaining_value", () => {
    expect(deriveRemaining(snapshot().buckets[0]!)).toBe(70);
  });

  it("derives remaining from limit minus used when remaining_value is missing", () => {
    const bucket = { ...snapshot().buckets[0]!, remaining_value: null, limit_value: 100, used_value: 40 };
    expect(deriveRemaining(bucket)).toBe(60);
  });

  it("returns null when the limit is missing or non-positive", () => {
    expect(deriveRemaining({ ...snapshot().buckets[0]!, remaining_value: null, limit_value: null })).toBeNull();
    expect(deriveRemaining({ ...snapshot().buckets[0]!, remaining_value: null, limit_value: 0, used_value: 0 })).toBeNull();
  });
});

describe("selectBucketHistory", () => {
  it("filters to the requested provider/account/bucket and sorts ascending", () => {
    const snapshots = [
      snapshot({ checked_at: "2026-07-19T12:00:00Z" }),
      snapshot({ checked_at: "2026-07-19T10:00:00Z" }),
      snapshot({ provider: "codex", checked_at: "2026-07-19T11:00:00Z" }),
      snapshot({ account_key: "account-b", checked_at: "2026-07-19T11:00:00Z" }),
    ];

    const points = selectBucketHistory(snapshots, "claude", "account-a", "session");

    expect(points.map((p) => p.checkedAt)).toEqual([
      "2026-07-19T10:00:00Z",
      "2026-07-19T12:00:00Z",
    ]);
  });

  it("skips snapshots that do not report the requested bucket id", () => {
    const withoutBucket = snapshot({ buckets: [] });
    const points = selectBucketHistory([withoutBucket], "claude", "account-a", "session");
    expect(points).toEqual([]);
  });
});

describe("listBucketOptions", () => {
  it("returns one option per bucket id using the most recent label", () => {
    const snapshots = [
      snapshot({ checked_at: "2026-07-19T09:00:00Z" }),
      snapshot({
        checked_at: "2026-07-19T10:00:00Z",
        buckets: [{ ...snapshot().buckets[0]!, label: "Renamed session" }],
      }),
    ];

    expect(listBucketOptions(snapshots, "claude", "account-a")).toEqual([
      { id: "session", label: "Renamed session", unit: "percent" },
    ]);
  });
});
