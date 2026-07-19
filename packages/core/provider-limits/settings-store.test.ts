// @vitest-environment jsdom

import { beforeEach, describe, expect, it } from "vitest";
import { useProviderLimitSettingsStore } from "./settings-store";

describe("provider limit threshold settings", () => {
  beforeEach(() => {
    localStorage.clear();
    useProviderLimitSettingsStore.setState({
      warningThreshold: 40,
      criticalThreshold: 20,
    });
  });

  it("persists bounded warning and critical thresholds", () => {
    const state = useProviderLimitSettingsStore.getState();
    state.setWarningThreshold(99);
    state.setCriticalThreshold(99);

    expect(useProviderLimitSettingsStore.getState()).toMatchObject({
      warningThreshold: 40,
      criticalThreshold: 20,
    });
    expect(localStorage.getItem("multica_provider_limit_thresholds")).toContain(
      '"warningThreshold":40',
    );
  });

  it("never leaves the critical threshold above a lowered warning threshold", () => {
    useProviderLimitSettingsStore.getState().setWarningThreshold(10);
    useProviderLimitSettingsStore.getState().setCriticalThreshold(20);

    expect(useProviderLimitSettingsStore.getState()).toMatchObject({
      warningThreshold: 10,
      criticalThreshold: 10,
    });
  });
});
