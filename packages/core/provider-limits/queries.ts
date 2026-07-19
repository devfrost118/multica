import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const providerLimitKeys = {
  all: (wsId: string) => ["provider-limits", wsId] as const,
  overview: (wsId: string) => [...providerLimitKeys.all(wsId), "overview"] as const,
  history: (wsId: string) => [...providerLimitKeys.all(wsId), "history"] as const,
};

const STALE_TIME = 60 * 1000;

export function providerLimitOverviewOptions(wsId: string) {
  return queryOptions({
    queryKey: providerLimitKeys.overview(wsId),
    queryFn: () => api.getProviderLimits(),
    enabled: !!wsId,
    staleTime: STALE_TIME,
  });
}

export function providerLimitHistoryOptions(wsId: string) {
  return queryOptions({
    queryKey: providerLimitKeys.history(wsId),
    queryFn: () => api.getProviderLimitHistory(),
    enabled: !!wsId,
    staleTime: STALE_TIME,
  });
}
