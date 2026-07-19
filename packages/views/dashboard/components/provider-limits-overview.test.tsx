// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";
import { ProviderLimitsOverview } from "./provider-limits-overview";

afterEach(cleanup);

const checkedAt = "2026-07-19T10:00:00Z";

function snapshot(overrides: Record<string, unknown> = {}) {
  return {
    runtime_id: "daemon-1",
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
    buckets: [
      {
        id: "five_hour",
        label: "5-hour window",
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

describe("ProviderLimitsOverview", () => {
  it("renders a loading state without hiding the provider limits section", () => {
    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: [], daemons: [] }} history={[]} isLoading={true} isError={false} />,
    );

    expect(screen.getByText("Provider limits")).toBeTruthy();
    expect(screen.getByText("Loading provider limits…")).toBeTruthy();
  });

  it("shows the empty explanation and the required unavailable Antigravity provider", () => {
    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: [], daemons: [] }} history={[]} isLoading={false} isError={false} />,
    );

    expect(screen.getByText("No provider limits reported yet.")).toBeTruthy();
    expect(screen.getAllByText("Antigravity").length).toBeGreaterThan(0);
    expect(screen.getByText("Unavailable")).toBeTruthy();
  });

  it("renders every provider and bucket status with remaining/reset/source metadata", () => {
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
    expect(screen.getAllByText("70% remaining").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Resets/).length).toBeGreaterThan(0);
    expect(screen.getAllByText("Official API · official").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Fresh for 15m").length).toBeGreaterThan(0);
    expect(screen.getByText("Reason: Not Configured")).toBeTruthy();
  });

  it("uses history to surface the last good snapshot after an unavailable report", () => {
    const unavailable = snapshot({ status: "unavailable", buckets: [], error_note: "not_configured" });
    const lastGood = snapshot({ checked_at: "2026-07-19T09:00:00Z" });

    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: [unavailable], daemons: [] }} history={[lastGood]} isLoading={false} isError={false} />,
    );

    expect(screen.getByText(/Last good:/)).toBeTruthy();
  });

  it("deduplicates an account reported by two daemons while preserving the diagnostic daemon view", () => {
    const newer = snapshot({ runtime_id: "daemon-2", checked_at: "2026-07-19T11:00:00Z" });
    const older = snapshot({ runtime_id: "daemon-1" });

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
    expect(screen.getByText(/daemon-1/)).toBeTruthy();
    expect(screen.getByText(/daemon-2/)).toBeTruthy();
  });

  it("shows a query error instead of treating it as an empty response", () => {
    renderWithI18n(
      <ProviderLimitsOverview overview={{ accounts: [], daemons: [] }} history={[]} isLoading={false} isError={true} />,
    );

    expect(screen.getByText("Provider limits could not be loaded.")).toBeTruthy();
  });
});
