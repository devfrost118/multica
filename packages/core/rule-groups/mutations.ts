import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { ruleGroupKeys } from "./queries";
import { useWorkspaceId } from "../hooks";
import type {
  CreateRuleGroupRequest,
  UpdateRuleGroupRequest,
  CreateRuleGroupRuleRequest,
  UpdateRuleGroupRuleRequest,
  CreateRuleGroupBindingRequest,
  UpdateRuleGroupBindingRequest,
} from "../types";

export function useCreateRuleGroup() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: CreateRuleGroupRequest) => api.createRuleGroup(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.list(wsId) });
    },
  });
}

export function useUpdateRuleGroup() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string } & UpdateRuleGroupRequest) =>
      api.updateRuleGroup(id, data),
    onSuccess: (group) => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: ruleGroupKeys.detail(wsId, group.id) });
    },
  });
}

export function useDeleteRuleGroup() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.deleteRuleGroup(id),
    onSuccess: (_res, id) => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.list(wsId) });
      qc.removeQueries({ queryKey: ruleGroupKeys.detail(wsId, id) });
      // Bindings can reference the deleted group; refresh scope views + previews.
      qc.invalidateQueries({ queryKey: ruleGroupKeys.bindings(wsId) });
    },
  });
}

export function useCreateRuleGroupRule(groupId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: CreateRuleGroupRuleRequest) =>
      api.createRuleGroupRule(groupId, data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.rules(wsId, groupId) });
      qc.invalidateQueries({ queryKey: ruleGroupKeys.detail(wsId, groupId) });
      qc.invalidateQueries({ queryKey: ruleGroupKeys.list(wsId) });
    },
  });
}

export function useUpdateRuleGroupRule(groupId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string } & UpdateRuleGroupRuleRequest) =>
      api.updateRuleGroupRule(groupId, id, data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.rules(wsId, groupId) });
      qc.invalidateQueries({ queryKey: ruleGroupKeys.detail(wsId, groupId) });
      // A structural rule edit adopts a builtin group to manual server-side;
      // refresh the list so the source_type-driven "built-in" badge updates.
      qc.invalidateQueries({ queryKey: ruleGroupKeys.list(wsId) });
    },
  });
}

export function useDeleteRuleGroupRule(groupId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.deleteRuleGroupRule(groupId, id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.rules(wsId, groupId) });
      qc.invalidateQueries({ queryKey: ruleGroupKeys.detail(wsId, groupId) });
      qc.invalidateQueries({ queryKey: ruleGroupKeys.list(wsId) });
    },
  });
}

export function useCreateRuleGroupBinding() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: CreateRuleGroupBindingRequest) =>
      api.createRuleGroupBinding(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.bindings(wsId) });
      qc.invalidateQueries({ queryKey: ruleGroupKeys.list(wsId) });
    },
  });
}

export function useUpdateRuleGroupBinding() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string } & UpdateRuleGroupBindingRequest) =>
      api.updateRuleGroupBinding(id, data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.bindings(wsId) });
    },
  });
}

export function useDeleteRuleGroupBinding() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.deleteRuleGroupBinding(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ruleGroupKeys.bindings(wsId) });
      qc.invalidateQueries({ queryKey: ruleGroupKeys.list(wsId) });
    },
  });
}
