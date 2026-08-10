import { describe, expect, it } from "vitest";
import type { ProviderLimitBucket, ProviderLimitSnapshot } from "../types";
import { selectCanonicalBuckets, withCanonicalBuckets } from "./canonical-buckets";

function bucket(overrides: Partial<ProviderLimitBucket> = {}): ProviderLimitBucket {
  return {
    id: "session",
    label: "Limit session",
    unit: "percent",
    limit_value: 100,
    used_value: 14,
    remaining_value: 86,
    resets_at: "2026-07-28T00:10:00Z",
    status: "ok",
    note: "",
    ...overrides,
  };
}

function snapshot(overrides: Partial<ProviderLimitSnapshot> = {}): ProviderLimitSnapshot {
  return {
    runtime_id: "daemon-1",
    daemon_id: "daemon-1",
    provider: "claude",
    account_key: "account-a",
    account_label: "profile-max",
    checked_at: "2026-07-27T16:00:00Z",
    status: "ok",
    source: { kind: "local_auth_state", freshness_seconds: 900, confidence: "official" },
    buckets: [bucket()],
    error_note: "",
    stale: false,
    ...overrides,
  };
}

// The snapshot an older daemon build wrote: the three real quotas under
// "limit-"-prefixed ids, plus legacy windows duplicating session/weekly.
const legacyClaudeBuckets = [
  bucket({ id: "five_hour", label: "Five hour" }),
  bucket({ id: "seven_day", label: "Seven day", used_value: 3, remaining_value: 97 }),
  bucket({ id: "limit-session", label: "Limit session" }),
  bucket({ id: "limit-weekly_all", label: "Limit weekly all", used_value: 3, remaining_value: 97 }),
  bucket({ id: "limit-weekly_scoped", label: "Limit weekly scoped", used_value: 0, remaining_value: 100 }),
  bucket({ id: "spend", label: "Spend", used_value: 0, remaining_value: 100 }),
];

describe("selectCanonicalBuckets", () => {
  // Antigravity used to report one "session" bucket for what turned out to be
  // two independently metered pools. History keeps that id forever, so without
  // a canonical set the detail dialog grows a third tab plotting a series that
  // no longer means anything.
  it("drops the superseded Antigravity session bucket and keeps both families", () => {
    const stored = [
      bucket({ id: "session", label: "Limit session" }),
      bucket({ id: "session_claude", label: "Limit session Claude", used_value: 10, remaining_value: 90 }),
      bucket({ id: "session_gemini", label: "Limit session Gemini", used_value: 50, remaining_value: 50 }),
    ];

    const selected = selectCanonicalBuckets("antigravity", stored);

    expect(selected.map((entry) => entry.id)).toEqual(["session_claude", "session_gemini"]);
    expect(selected.map((entry) => entry.used_value)).toEqual([10, 50]);
  });

  it("keeps the three Claude quotas in display order and drops legacy windows", () => {
    const selected = selectCanonicalBuckets("claude", legacyClaudeBuckets);

    expect(selected.map((entry) => entry.id)).toEqual(["session", "weekly_all", "weekly_scoped"]);
    expect(selected.map((entry) => entry.label)).toEqual([
      "Limit session",
      "Limit weekly all",
      "Limit weekly scoped",
    ]);
  });

  it("drops Claude bucket kinds the current adapter does not emit", () => {
    const selected = selectCanonicalBuckets("claude", [
      bucket({ id: "unknown_future_kind" }),
      bucket({ id: "weekly_all" }),
    ]);

    expect(selected.map((entry) => entry.id)).toEqual(["weekly_all"]);
  });

  it("reorders Claude buckets reported out of order and keeps the first of a duplicate id", () => {
    const selected = selectCanonicalBuckets("claude", [
      bucket({ id: "weekly_scoped" }),
      bucket({ id: "limit-session", label: "Canonical session" }),
      bucket({ id: "session", label: "Duplicate session" }),
    ]);

    expect(selected.map((entry) => entry.id)).toEqual(["session", "weekly_scoped"]);
    expect(selected[0]?.label).toBe("Canonical session");
  });

  it("returns buckets untouched for providers without a canonical set", () => {
    const codexBuckets = [bucket({ id: "weekly" }), bucket({ id: "five_hour" })];

    expect(selectCanonicalBuckets("codex", codexBuckets)).toEqual(codexBuckets);
  });
});

describe("withCanonicalBuckets", () => {
  it("rewrites Claude snapshots and leaves other providers by reference", () => {
    const claude = snapshot({ buckets: legacyClaudeBuckets });
    const codex = snapshot({ provider: "codex", buckets: [bucket({ id: "five_hour" })] });

    const [canonicalClaude, canonicalCodex] = withCanonicalBuckets([claude, codex]);

    expect(canonicalClaude?.buckets.map((entry) => entry.id)).toEqual([
      "session",
      "weekly_all",
      "weekly_scoped",
    ]);
    expect(canonicalCodex).toBe(codex);
  });

  it("does not mutate the source snapshots", () => {
    const claude = snapshot({ buckets: legacyClaudeBuckets });

    withCanonicalBuckets([claude]);

    expect(claude.buckets).toHaveLength(6);
    expect(claude.buckets[2]?.id).toBe("limit-session");
  });

  it("keeps a Claude snapshot that reports no recognizable bucket empty", () => {
    const claude = snapshot({ buckets: [bucket({ id: "spend" })] });

    expect(withCanonicalBuckets([claude])[0]?.buckets).toEqual([]);
  });
});
