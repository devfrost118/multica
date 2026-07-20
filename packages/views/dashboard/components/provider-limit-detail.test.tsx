// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen } from "@testing-library/react";
import type { ProviderLimitSnapshot } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ProviderLimitDetail } from "./provider-limit-detail";

// The chart is recharts-heavy; stub it so tests assert on the surrounding
// state (which bucket/points were selected) rather than SVG internals.
vi.mock("./provider-limit-history-chart", () => ({
  ProviderLimitHistoryChart: ({ points, unit }: { points: unknown[]; unit: string }) => (
    <div data-testid="history-chart">
      {points.length} points · {unit}
    </div>
  ),
}));

afterEach(cleanup);

function snapshot(overrides: Partial<ProviderLimitSnapshot> = {}): ProviderLimitSnapshot {
  return {
    runtime_id: "daemon-1",
    daemon_id: "daemon-1",
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

function openDetail(record: ProviderLimitSnapshot, history: ProviderLimitSnapshot[]) {
  renderWithI18n(<ProviderLimitDetail record={record} history={history} />);
  fireEvent.click(screen.getByRole("button", { name: "Details" }));
}

describe("ProviderLimitDetail", () => {
  it("shows a no-history message when the bucket has no recorded snapshots", () => {
    openDetail(snapshot(), []);

    expect(screen.getByText("No history recorded yet for this bucket.")).toBeTruthy();
  });

  it("renders the chart with points once history exists for the active bucket", () => {
    const history = [
      snapshot({ checked_at: "2026-07-19T08:00:00Z", buckets: [{ ...snapshot().buckets[0]!, remaining_value: 90 }] }),
      snapshot({ checked_at: "2026-07-19T10:00:00Z" }),
    ];

    openDetail(snapshot(), history);

    expect(screen.getByTestId("history-chart").textContent).toBe("2 points · percent");
  });

  it("switches bucket tabs and recomputes the chart/pace for the newly selected bucket", () => {
    const record = snapshot({
      buckets: [
        { ...snapshot().buckets[0]!, id: "session", label: "Session" },
        { ...snapshot().buckets[0]!, id: "weekly", label: "Weekly", remaining_value: 40 },
      ],
    });
    const history = [record];

    openDetail(record, history);

    expect(screen.getByTestId("history-chart").textContent).toBe("1 points · percent");
    fireEvent.click(screen.getByRole("tab", { name: "Weekly" }));
    expect(screen.getByTestId("history-chart").textContent).toBe("1 points · percent");
  });

  it("marks pace/runway as estimated and shows the projection when usage is decreasing", () => {
    const history = [
      snapshot({ checked_at: "2026-07-19T08:00:00Z", buckets: [{ ...snapshot().buckets[0]!, remaining_value: 90 }] }),
      snapshot({ checked_at: "2026-07-19T10:00:00Z", buckets: [{ ...snapshot().buckets[0]!, remaining_value: 70 }] }),
    ];

    openDetail(snapshot({ checked_at: "2026-07-19T10:00:00Z" }), history);

    expect(screen.getByText("Estimated")).toBeTruthy();
    expect(screen.getByText(/consumed/)).toBeTruthy();
    expect(screen.getByText(/left at this pace/)).toBeTruthy();
  });

  it("explains why pace is unavailable instead of showing a fabricated number", () => {
    openDetail(snapshot(), [snapshot()]);

    expect(screen.getByText("Not enough comparable snapshots yet.")).toBeTruthy();
    expect(screen.queryByText("Estimated")).toBeNull();
  });

  it("shows the stale-data reason when the latest snapshot is stale", () => {
    const history = [
      snapshot({ checked_at: "2026-07-19T08:00:00Z", buckets: [{ ...snapshot().buckets[0]!, remaining_value: 90 }] }),
      snapshot({ checked_at: "2026-07-19T10:00:00Z", stale: true }),
    ];

    openDetail(snapshot({ checked_at: "2026-07-19T10:00:00Z", stale: true }), history);

    expect(
      screen.getByText("Latest snapshot is stale — refresh before trusting a projection."),
    ).toBeTruthy();
  });
});
