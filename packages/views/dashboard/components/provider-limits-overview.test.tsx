// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import type { ProviderLimitSnapshot } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ProviderLimitsOverview, providerLimitFreshness } from "./provider-limits-overview";

afterEach(cleanup);

const checkedAt = "2026-07-19T10:00:00Z";

function snapshot(overrides: Record<string, unknown> = {}) {
  return {
    runtime_id: "daemon-1",
    daemon_id: "daemon-1",
    provider: "claude",
    account_key: "account-a",
    account_label: "Shared account",
    checked_at: checkedAt,
    status: "ok",
    source: {
      kind: "official_api",
      confidence: "official",
      freshness_seconds: 900,
    },
    buckets: [bucket()],
    error_note: "",
    stale: false,
    ...overrides,
  };
}

function bucket(overrides: Record<string, unknown> = {}) {
  return {
    id: "session",
    label: "Limit session",
    unit: "percent",
    limit_value: 100,
    used_value: 30,
    remaining_value: 70,
    resets_at: "2026-07-19T15:00:00Z",
    status: "ok",
    note: "",
    ...overrides,
  };
}

describe("ProviderLimitsOverview", () => {
  it("renders a loading state without hiding the provider limits section", () => {
    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: [], daemons: [] }} history={[]} isLoading={true} isError={false} />,
    );

    expect(screen.getByText("Provider limits")).toBeTruthy();
    expect(screen.getByText("Loading provider limits…")).toBeTruthy();
  });

  it("shows the empty explanation when no providers have reported", () => {
    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: [], daemons: [] }} history={[]} isLoading={false} isError={false} />,
    );

    expect(screen.getByText("No provider limits reported yet.")).toBeTruthy();
    expect(screen.queryAllByRole("article")).toHaveLength(0);
  });

  it("renders every provider and bucket status with remaining and reset info", () => {
    const statuses = [
      snapshot(),
      snapshot({ provider: "codex", account_key: "account-b", account_label: "Codex", stale: true }),
      snapshot({ provider: "cursor", account_key: "account-c", account_label: "Cursor", status: "partial" }),
      snapshot({ provider: "perplexity", account_key: "account-d", account_label: "Perplexity", status: "unavailable", error_note: "not_configured", buckets: [] }),
      snapshot({ provider: "other", account_key: "account-e", account_label: "Other", status: "error", error_note: "probe_failed", buckets: [] }),
    ];

    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: statuses, daemons: [] }} history={[]} isLoading={false} isError={false} />,
    );

    expect(screen.getAllByRole("article")).toHaveLength(5);
    expect(screen.getAllByText("30% used").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Resets/).length).toBeGreaterThan(0);

    // Metadata and the textual status are now in the Details dialog; the card
    // itself only carries the colour freshness badge.
    fireEvent.click(screen.getAllByRole("button", { name: "Details" })[0]!);
    expect(screen.getByText("Official API · official")).toBeTruthy();
    expect(screen.getByText("Fresh for 15m")).toBeTruthy();
    expect(screen.getByText("Latest attempt: OK")).toBeTruthy();
  });

  it("uses history to surface the last good snapshot after an unavailable report in Details", () => {
    const unavailable = snapshot({ status: "unavailable", buckets: [], error_note: "not_configured" });
    const lastGood = snapshot({ checked_at: "2026-07-19T09:00:00Z" });

    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: [unavailable], daemons: [] }} history={[lastGood]} isLoading={false} isError={false} />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Details" }));
    expect(screen.getByText(/Last successful collection:/)).toBeTruthy();
  });

  it("deduplicates an account reported by two daemons while preserving the diagnostic daemon view", () => {
    const newer = snapshot({ runtime_id: "daemon-2", daemon_id: "daemon-2", checked_at: "2026-07-19T11:00:00Z" });
    const older = snapshot({ runtime_id: "daemon-1", daemon_id: "daemon-1" });

    renderWithI18n(
      <ProviderLimitsOverview
        overview={{ accounts: [older, newer], daemons: [older, newer] }}
        history={[]}
        isLoading={false}
        isError={false}
      />,
    );

    expect(screen.getAllByText("Shared account")).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: "By daemon" }));
    expect(screen.getAllByRole("article")).toHaveLength(2);
  });

  it("shows only the latest state when a legacy unkeyed snapshot matches an identified account", () => {
    const legacy = snapshot({
      account_key: "unavailable",
      account_label: "profile-plus",
      checked_at: "2026-07-19T10:00:00Z",
      buckets: [
        bucket({
          id: "weekly_all",
          label: "Primary 7d",
          used_value: 88,
          remaining_value: 12,
          resets_at: "2026-07-25T11:43:13Z",
        }),
      ],
    });
    const current = snapshot({
      account_key: "0123456789abcdef",
      account_label: "profile-plus",
      checked_at: "2026-07-19T11:00:00Z",
    });

    renderWithI18n(
      <ProviderLimitsOverview
        overview={{ accounts: [legacy, current], daemons: [] }}
        history={[]}
        isLoading={false}
        isError={false}
      />,
    );

    expect(screen.getAllByRole("article")).toHaveLength(1);
    expect(screen.getByText("Limit session")).toBeTruthy();
    expect(screen.queryByText("Primary 7d")).toBeNull();
  });

  it("keeps one Codex card with last-known limits and relative age after an unkeyed auth failure", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T10:15:00Z"));
    const lastGood = snapshot({
      provider: "codex",
      account_key: "0123456789abcdef",
      account_label: "profile-plus",
      checked_at: "2026-07-19T10:00:00Z",
    });
    const failedAttempt = snapshot({
      provider: "codex",
      account_key: "unavailable",
      account_label: "",
      checked_at: "2026-07-19T10:15:00Z",
      status: "unavailable",
      buckets: [],
      error_note: "auth_expired",
    });

    try {
      renderWithI18n(
        <ProviderLimitsOverview
          overview={{ accounts: [lastGood, failedAttempt], daemons: [] }}
          history={[failedAttempt, lastGood]}
          isLoading={false}
          isError={false}
        />,
      );

      expect(screen.getAllByRole("article")).toHaveLength(1);
      expect(screen.getByText("Plus")).toBeTruthy();
      expect(screen.getByText("Limit session")).toBeTruthy();
      expect(screen.getByText("30% used")).toBeTruthy();
      expect(screen.getByText("Updated 15 minutes ago")).toBeTruthy();

      fireEvent.click(screen.getByRole("button", { name: "Details" }));
      expect(screen.getByText(/Last successful collection:/)).toBeTruthy();
      expect(screen.getByText(/Last attempted probe:/)).toBeTruthy();
      expect(screen.getByText(/Authentication expired. Sign in to Codex again./)).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows one checked-age unavailable card when no successful snapshot exists", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-19T10:05:00Z"));
    const unavailable = snapshot({
      provider: "codex",
      account_key: "unavailable",
      account_label: "",
      checked_at: "2026-07-19T10:00:00Z",
      status: "unavailable",
      buckets: [],
      error_note: "authentication_required",
    });

    try {
      renderWithI18n(
        <ProviderLimitsOverview
          overview={{ accounts: [unavailable], daemons: [] }}
          history={[unavailable]}
          isLoading={false}
          isError={false}
        />,
      );

      expect(screen.getAllByRole("article")).toHaveLength(1);
      expect(screen.getByText("Data unavailable · checked 5 minutes ago")).toBeTruthy();
      expect(screen.queryByText("Plus")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  // Factory and Cursor report "not set up yet" as an unkeyed unavailable
  // snapshot with no buckets. That card is the only place the user learns what
  // to do, so it must render with the reason spelled out rather than a bare
  // "no quota buckets" line (FRO-206).
  it("renders an unavailable provider card with an actionable reason instead of a bare empty state", () => {
    const factory = snapshot({
      provider: "factory",
      account_key: "unavailable",
      account_label: "",
      status: "unavailable",
      buckets: [],
      error_note: "credential_missing",
    });
    const cursor = snapshot({
      provider: "cursor",
      account_key: "unavailable",
      account_label: "",
      status: "unavailable",
      buckets: [],
      error_note: "reauth_required",
    });

    renderWithI18n(
      <ProviderLimitsOverview
        overview={{ accounts: [factory, cursor], daemons: [] }}
        history={[]}
        isLoading={false}
        isError={false}
      />,
    );

    const cards = screen.getAllByRole("article");
    expect(cards).toHaveLength(2);
    expect(screen.getByText("Factory")).toBeTruthy();
    expect(screen.getByText("Cursor")).toBeTruthy();
    expect(
      screen.getByText("No token is connected. Open Details and add one to collect usage data."),
    ).toBeTruthy();
    expect(
      screen.getByText("The local session expired. Sign in with the provider's CLI again."),
    ).toBeTruthy();
    expect(screen.queryByText("No quota buckets are available.")).toBeNull();
  });

  it("collapses a legacy snapshot within one daemon without merging different daemons", () => {
    const legacy = snapshot({
      daemon_id: "daemon-1",
      runtime_id: "runtime-1",
      account_key: "unavailable",
      account_label: "profile-plus",
      checked_at: "2026-07-19T10:00:00Z",
    });
    const current = snapshot({
      daemon_id: "daemon-1",
      runtime_id: "runtime-1",
      account_key: "0123456789abcdef",
      account_label: "profile-plus",
      checked_at: "2026-07-19T11:00:00Z",
    });
    const otherDaemon = snapshot({
      daemon_id: "daemon-2",
      runtime_id: "runtime-2",
      account_key: "fedcba9876543210",
      account_label: "profile-plus",
      checked_at: "2026-07-19T10:30:00Z",
    });

    renderWithI18n(
      <ProviderLimitsOverview
        overview={{ accounts: [], daemons: [legacy, current, otherDaemon] }}
        history={[]}
        isLoading={false}
        isError={false}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "By daemon" }));

    expect(screen.getAllByRole("article")).toHaveLength(2);
  });

  // A daemon build older than the app writes six Claude buckets: the three real
  // quotas under "limit-" ids plus five_hour/seven_day/spend windows duplicating
  // them. Both the card and the Details tabs must stay at the canonical three.
  it("shows only the three canonical Claude buckets when a snapshot carries legacy windows", () => {
    const legacyBuild = snapshot({
      buckets: [
        bucket({ id: "five_hour", label: "Five hour", used_value: 14, remaining_value: 86 }),
        bucket({ id: "seven_day", label: "Seven day", used_value: 3, remaining_value: 97 }),
        bucket({ id: "limit-session", label: "Limit session", used_value: 14, remaining_value: 86 }),
        bucket({ id: "limit-weekly_all", label: "Limit weekly all", used_value: 3, remaining_value: 97 }),
        bucket({ id: "limit-weekly_scoped", label: "Limit weekly scoped", used_value: 0, remaining_value: 100 }),
        bucket({ id: "spend", label: "Spend", used_value: 0, remaining_value: 100 }),
      ],
    });

    renderWithI18n(
      <ProviderLimitsOverview
        overview={{ accounts: [legacyBuild], daemons: [legacyBuild] }}
        history={[legacyBuild]}
        isLoading={false}
        isError={false}
      />,
    );

    expect(screen.getByText("Limit session")).toBeTruthy();
    expect(screen.getByText("Limit weekly all")).toBeTruthy();
    expect(screen.getByText("Limit weekly scoped")).toBeTruthy();
    expect(screen.queryByText("Five hour")).toBeNull();
    expect(screen.queryByText("Seven day")).toBeNull();
    expect(screen.queryByText("Spend")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Details" }));
    expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
      "Limit session",
      "Limit weekly all",
      "Limit weekly scoped",
    ]);
  });

  it("shows a query error instead of treating it as an empty response", () => {
    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: [], daemons: [] }} history={[]} isLoading={false} isError={true} />,
    );

    expect(screen.getByText("Provider limits could not be loaded.")).toBeTruthy();
  });
});

// One Refresh click queues a collection for a whole runtime, so progress has to
// show on every card of that runtime — and only that runtime — for as long as
// the caller's refresh promise is pending.
describe("per-card refresh spinner", () => {
  function deferredRefresh() {
    let settle = () => {};
    const promise = new Promise<void>((resolve) => {
      settle = () => resolve();
    });
    return { promise, settle };
  }

  const twoRuntimes = [
    snapshot(),
    snapshot({ provider: "codex", account_key: "account-b", account_label: "Codex" }),
    snapshot({
      runtime_id: "daemon-2",
      daemon_id: "daemon-2",
      provider: "cursor",
      account_key: "account-c",
      account_label: "Cursor",
    }),
  ];

  it("shows a labelled spinner on every card of the refreshing runtime", async () => {
    const refresh = deferredRefresh();
    const onRefresh = vi.fn(() => refresh.promise);

    renderWithI18n(
      <ProviderLimitsOverview
        overview={{ accounts: twoRuntimes, daemons: [] }}
        history={[]}
        isLoading={false}
        isError={false}
        onRefresh={onRefresh}
      />,
    );

    expect(screen.getAllByRole("article")).toHaveLength(3);
    expect(screen.queryAllByRole("status")).toHaveLength(0);

    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

    expect(onRefresh).toHaveBeenCalledWith("daemon-1");
    const spinners = await screen.findAllByRole("status");
    expect(spinners).toHaveLength(2);
    expect(spinners[0]!.getAttribute("aria-label")).toBe("Refreshing usage data…");

    await act(async () => refresh.settle());
  });

  it("hides the spinners once the refresh settles", async () => {
    const refresh = deferredRefresh();

    renderWithI18n(
      <ProviderLimitsOverview
        overview={{ accounts: twoRuntimes, daemons: [] }}
        history={[]}
        isLoading={false}
        isError={false}
        onRefresh={() => refresh.promise}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findAllByRole("status")).toHaveLength(2);

    await act(async () => refresh.settle());

    expect(screen.queryAllByRole("status")).toHaveLength(0);
  });
});

// The card badge is the only place the freshness scale is visible, so each
// colour step needs a rendered example — a unit test on the helper alone would
// not catch a wrong class landing in the badge.
describe("provider limit freshness badge", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  function renderBadge(now: string, overrides: Record<string, unknown> = {}): HTMLElement {
    vi.setSystemTime(new Date(now));
    renderWithI18n(
      <ProviderLimitsOverview
        overview={{ accounts: [snapshot(overrides)], daemons: [] }}
        history={[]}
        isLoading={false}
        isError={false}
      />,
    );
    return within(screen.getByRole("article")).getByRole("img");
  }

  it("is green while the snapshot is inside the declared freshness window", () => {
    const badge = renderBadge("2026-07-19T10:05:00Z");

    expect(badge.className).toContain("bg-success/10");
    expect(badge.getAttribute("aria-label")).toBe("Fresh: Updated within the freshness window.");
    expect(within(badge).getByText("Fresh")).toBeTruthy();
  });

  it("is yellow once the snapshot is older than the window but under a day", () => {
    const badge = renderBadge("2026-07-19T15:00:00Z");

    expect(badge.className).toContain("bg-warning/10");
    expect(badge.getAttribute("aria-label")).toBe("Aging: Updated less than a day ago.");
  });

  it("is orange for a snapshot between one and three days old", () => {
    const badge = renderBadge("2026-07-21T10:00:00Z");

    expect(badge.className).toContain("bg-warning-strong/10");
    expect(badge.getAttribute("aria-label")).toBe("Old: Updated 1-3 days ago.");
  });

  it("is red for a snapshot older than three days", () => {
    const badge = renderBadge("2026-07-25T10:00:00Z");

    expect(badge.className).toContain("bg-destructive/10");
    expect(badge.getAttribute("aria-label")).toBe(
      "Outdated: Updated more than 3 days ago, or usage data could not be collected.",
    );
  });

  it("is red for an unavailable provider even when the probe just ran", () => {
    const badge = renderBadge("2026-07-19T10:00:00Z", { status: "unavailable", buckets: [] });

    expect(badge.className).toContain("bg-destructive/10");
  });

  it("is red for an errored provider even when the probe just ran", () => {
    const badge = renderBadge("2026-07-19T10:00:00Z", { status: "error", buckets: [] });

    expect(badge.className).toContain("bg-destructive/10");
  });

  it("is red when the latest probe failed behind a still-recent good snapshot", () => {
    const badge = renderBadge("2026-07-19T10:05:00Z", { last_attempt_status: "unavailable" });

    expect(badge.className).toContain("bg-destructive/10");
  });

  // The dashboard is a long-lived page: nothing refetches or re-renders it on
  // its own, so a badge that only reads the clock during render stays green for
  // hours after the data went stale. The same rendered badge must walk the
  // whole scale on its own.
  it("walks the whole scale on a page that is never refetched or remounted", () => {
    const badge = renderBadge("2026-07-19T10:05:00Z");
    const step = (now: string) => {
      vi.setSystemTime(new Date(now));
      act(() => {
        vi.advanceTimersByTime(60_000);
      });
      return within(screen.getByRole("article")).getByRole("img");
    };

    expect(badge.className).toContain("bg-success/10");
    expect(step("2026-07-19T10:20:00Z").className).toContain("bg-warning/10");
    expect(step("2026-07-20T11:00:00Z").className).toContain("bg-warning-strong/10");
    expect(step("2026-07-23T11:00:00Z").className).toContain("bg-destructive/10");
    // Same DOM node throughout: the colour changed by ticking, not by remount.
    expect(within(screen.getByRole("article")).getByRole("img")).toBe(badge);
  });
});

describe("providerLimitFreshness", () => {
  const record = (overrides: Record<string, unknown> = {}) =>
    snapshot(overrides) as ProviderLimitSnapshot;
  const now = Date.parse("2026-07-19T12:00:00Z");

  it("keeps a snapshot fresh exactly at the window boundary", () => {
    expect(providerLimitFreshness(record({ checked_at: "2026-07-19T11:45:00Z" }), now)).toBe("fresh");
  });

  it("honours a provider that declares a longer freshness window", () => {
    const longWindow = record({
      checked_at: "2026-07-19T06:00:00Z",
      source: { kind: "local_log", confidence: "inferred", freshness_seconds: 43_200 },
    });

    expect(providerLimitFreshness(longWindow, now)).toBe("fresh");
  });

  it("falls back to the default window when the source declares none", () => {
    const noWindow = record({
      checked_at: "2026-07-19T11:00:00Z",
      source: { kind: "local_log", confidence: "inferred", freshness_seconds: 0 },
    });

    expect(providerLimitFreshness(noWindow, now)).toBe("recent");
  });

  it("prefers the last successful collection over the latest probe time", () => {
    const stale = record({
      checked_at: "2026-07-19T11:59:00Z",
      last_successful_at: "2026-07-17T12:00:00Z",
    });

    expect(providerLimitFreshness(stale, now)).toBe("aging");
  });

  it("treats an unparseable timestamp as expired", () => {
    expect(providerLimitFreshness(record({ checked_at: "not-a-date" }), now)).toBe("expired");
  });
});
