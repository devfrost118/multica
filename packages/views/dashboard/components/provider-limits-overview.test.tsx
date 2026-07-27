// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";
import { ProviderLimitsOverview } from "./provider-limits-overview";

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

    expect(screen.getByText("OK")).toBeTruthy();
    expect(screen.getByText("Stale")).toBeTruthy();
    expect(screen.getByText("Partial")).toBeTruthy();
    expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(0);
    expect(screen.getByText("Error")).toBeTruthy();
    expect(screen.getAllByText("30% used").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Resets/).length).toBeGreaterThan(0);

    // Metadata is now in Details dialog
    fireEvent.click(screen.getAllByRole("button", { name: "Details" })[0]!);
    expect(screen.getByText("Official API · official")).toBeTruthy();
    expect(screen.getByText("Fresh for 15m")).toBeTruthy();
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
