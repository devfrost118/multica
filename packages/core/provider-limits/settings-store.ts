"use client";

import { create } from "zustand";
import { createJSONStorage, persist, type StateStorage } from "zustand/middleware";
import { defaultStorage } from "../platform/storage";

const DEFAULT_WARNING_THRESHOLD = 40;
const DEFAULT_CRITICAL_THRESHOLD = 20;

export interface ProviderLimitSettingsState {
  warningThreshold: number;
  criticalThreshold: number;
  setWarningThreshold: (value: number) => void;
  setCriticalThreshold: (value: number) => void;
}

function percentageAtMost(value: number, maximum: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.max(0, Math.min(maximum, Math.round(value)));
}

const stateStorage = defaultStorage as unknown as StateStorage;

export const useProviderLimitSettingsStore = create<ProviderLimitSettingsState>()(
  persist(
    (set) => ({
      warningThreshold: DEFAULT_WARNING_THRESHOLD,
      criticalThreshold: DEFAULT_CRITICAL_THRESHOLD,
      setWarningThreshold: (value) =>
        set((state) => ({
          warningThreshold: percentageAtMost(value, DEFAULT_WARNING_THRESHOLD),
          criticalThreshold: Math.min(
            state.criticalThreshold,
            percentageAtMost(value, DEFAULT_WARNING_THRESHOLD),
          ),
        })),
      setCriticalThreshold: (value) =>
        set((state) => ({
          criticalThreshold: Math.min(
            state.warningThreshold,
            percentageAtMost(value, DEFAULT_CRITICAL_THRESHOLD),
          ),
        })),
    }),
    {
      name: "multica_provider_limit_thresholds",
      storage: createJSONStorage(() => stateStorage),
    },
  ),
);
