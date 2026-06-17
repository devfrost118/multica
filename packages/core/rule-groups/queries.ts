import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

// Query keys are workspace-scoped so switching workspace swaps the cache
// automatically (the wsId segment changes). The server resolves the active
// workspace from the X-Workspace-Slug header, so the api calls take no wsId.
export const ruleGroupKeys = {
  all: (wsId: string) => ["rule-groups", wsId] as const,
  list: (wsId: string) => [...ruleGroupKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) =>
    [...ruleGroupKeys.all(wsId), "detail", id] as const,
  rules: (wsId: string, id: string) =>
    [...ruleGroupKeys.detail(wsId, id), "rules"] as const,
  bindings: (wsId: string) => [...ruleGroupKeys.all(wsId), "bindings"] as const,
  bindingList: (wsId: string, scopeType?: string, scopeId?: string) =>
    [...ruleGroupKeys.bindings(wsId), scopeType ?? null, scopeId ?? null] as const,
  effective: (
    wsId: string,
    projectId?: string,
    squadId?: string,
    agentId?: string,
  ) =>
    [
      ...ruleGroupKeys.all(wsId),
      "effective",
      projectId ?? null,
      squadId ?? null,
      agentId ?? null,
    ] as const,
};

export function ruleGroupsListOptions(wsId: string) {
  return queryOptions({
    queryKey: ruleGroupKeys.list(wsId),
    queryFn: () => api.listRuleGroups(),
    enabled: Boolean(wsId),
  });
}

export function ruleGroupDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: ruleGroupKeys.detail(wsId, id),
    queryFn: () => api.getRuleGroup(id),
    enabled: Boolean(wsId) && Boolean(id),
  });
}

export function ruleGroupRulesOptions(wsId: string, groupId: string) {
  return queryOptions({
    queryKey: ruleGroupKeys.rules(wsId, groupId),
    queryFn: () => api.listRuleGroupRules(groupId),
    enabled: Boolean(wsId) && Boolean(groupId),
  });
}

export function ruleGroupBindingsOptions(
  wsId: string,
  scopeType?: string,
  scopeId?: string,
) {
  return queryOptions({
    queryKey: ruleGroupKeys.bindingList(wsId, scopeType, scopeId),
    queryFn: () => api.listRuleGroupBindings(scopeType, scopeId),
    enabled: Boolean(wsId),
  });
}

export function effectiveRulesOptions(
  wsId: string,
  params: { projectId?: string; squadId?: string; agentId?: string },
  enabled = true,
) {
  return queryOptions({
    queryKey: ruleGroupKeys.effective(
      wsId,
      params.projectId,
      params.squadId,
      params.agentId,
    ),
    queryFn: () => api.getEffectiveRules(params),
    enabled: Boolean(wsId) && enabled,
  });
}
