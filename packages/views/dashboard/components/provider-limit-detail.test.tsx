// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import type { ProviderLimitSnapshot } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ProviderLimitDetail } from "./provider-limit-detail";

const credentialMocks = vi.hoisted(() => ({
  useProviderCredentials: vi.fn(),
  useSaveProviderCredential: vi.fn(),
  useDeleteProviderCredential: vi.fn(),
  save: vi.fn(),
  remove: vi.fn(),
}));

vi.mock("@multica/core/provider-limits", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/provider-limits")>();
  return {
    ...actual,
    useProviderCredentials: credentialMocks.useProviderCredentials,
    useSaveProviderCredential: credentialMocks.useSaveProviderCredential,
    useDeleteProviderCredential: credentialMocks.useDeleteProviderCredential,
  };
});

// The chart is recharts-heavy; stub it so tests assert on the surrounding
// state (which bucket/points were selected) rather than SVG internals.
vi.mock("./provider-limit-history-chart", () => ({
  ProviderLimitHistoryChart: ({ points, unit }: { points: unknown[]; unit: string }) => (
    <div data-testid="history-chart">
      {points.length} points · {unit}
    </div>
  ),
}));

beforeEach(() => {
  credentialMocks.save.mockReset();
  credentialMocks.remove.mockReset();
  credentialMocks.useProviderCredentials.mockReturnValue({ data: [], error: null });
  credentialMocks.useSaveProviderCredential.mockReturnValue({
    mutateAsync: credentialMocks.save,
    isPending: false,
    error: null,
  });
  credentialMocks.useDeleteProviderCredential.mockReturnValue({
    mutateAsync: credentialMocks.remove,
    isPending: false,
    error: null,
  });
});

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
  renderWithI18n(<ProviderLimitDetail record={record} history={history} wsId="workspace-1" />);
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

  it("shows source, freshness, checked_at, and error metadata inside the detail dialog", () => {
    openDetail(
      snapshot({
        source: { kind: "official_api", freshness_seconds: 900, confidence: "official" },
        error_note: "rate_limited",
      }),
      [],
    );

    expect(screen.getByText("Official API · official")).toBeTruthy();
    expect(screen.getByText("Fresh for 15m")).toBeTruthy();
    expect(screen.getByText("Reason: Rate Limited")).toBeTruthy();
  });

  it("masks Factory credentials and never renders the token returned by the server", () => {
    const rawToken = "factory-super-secret-token";
    credentialMocks.useProviderCredentials.mockReturnValue({
      data: [
        {
          id: "credential-1",
          provider: "factory",
          account_key: "factory-account",
          account_label: "Factory account",
          fingerprint: "abc123def456",
          token: rawToken,
          last_validation_status: "valid",
          last_validated_at: "2026-07-23T10:00:00Z",
          last_validation_error: "",
          created_at: "2026-07-23T09:00:00Z",
          updated_at: "2026-07-23T10:00:00Z",
        },
      ],
      error: null,
    });

    openDetail(snapshot({ provider: "factory", account_key: "factory-account" }), []);

    const tokenInput = screen.getByLabelText("Factory API token") as HTMLInputElement;
    expect(tokenInput.type).toBe("password");
    expect(tokenInput.value).toBe("");
    expect(screen.getByText("Fingerprint: abc123def456")).toBeTruthy();
    expect(document.body.textContent).not.toContain(rawToken);
    expect(document.body.innerHTML).not.toContain(rawToken);
  });

  it("clears a submitted Factory token and never echoes it into the DOM", async () => {
    const rawToken = "factory-token-to-clear";
    credentialMocks.save.mockResolvedValue({ id: "credential-1" });

    openDetail(snapshot({ provider: "factory", account_key: "factory-account" }), []);

    const labelInput = screen.getByLabelText("Factory account label") as HTMLInputElement;
    const tokenInput = screen.getByLabelText("Factory API token") as HTMLInputElement;
    fireEvent.change(labelInput, { target: { value: "Primary Factory" } });
    fireEvent.change(tokenInput, { target: { value: rawToken } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() => {
      expect(credentialMocks.save).toHaveBeenCalledWith({
        id: undefined,
        request: {
          provider: "factory",
          token: rawToken,
          account_label: "Primary Factory",
        },
      });
      expect(tokenInput.value).toBe("");
      expect(labelInput.value).toBe("");
    });
    expect(document.body.textContent).not.toContain(rawToken);
    expect(document.body.innerHTML).not.toContain(rawToken);
  });
});

