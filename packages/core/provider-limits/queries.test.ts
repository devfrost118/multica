import { describe, expect, it } from "vitest";
import { providerLimitHistoryOptions, providerLimitOverviewOptions } from "./queries";

describe("provider limit queries", () => {
  it("scopes overview and history query keys by workspace id", () => {
    expect(providerLimitOverviewOptions("ws-a").queryKey).toEqual([
      "provider-limits",
      "ws-a",
      "overview",
    ]);
    expect(providerLimitHistoryOptions("ws-b").queryKey).toEqual([
      "provider-limits",
      "ws-b",
      "history",
    ]);
  });
});
