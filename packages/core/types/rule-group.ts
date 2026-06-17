// Rule Groups — workspace-scoped collections of markdown rules that can be
// bound to a workspace, project, squad, or agent. The backend assembles the
// "effective rules" for a (project, squad, agent) combination by layering
// bindings in a fixed order: workspace -> project -> squad -> agent.
// Mirrors server/internal/handler/rule_group.go response/request structs.

export type RuleGroupScopeType = "workspace" | "project" | "squad" | "agent";

export interface RuleGroup {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  enabled: boolean;
  source_type: string;
  source_ref: Record<string, unknown>;
  version: string | null;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface RuleGroupSummary extends RuleGroup {
  rule_count: number;
  binding_count: number;
}

export interface RuleGroupRule {
  id: string;
  workspace_id: string;
  rule_group_id: string;
  name: string;
  description: string;
  content: string;
  sort_order: number;
  enabled: boolean;
  file_name: string | null;
  tags: string[];
  runtime_hints: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface RuleGroupWithRules extends RuleGroup {
  rules: RuleGroupRule[];
}

export interface RuleGroupBinding {
  id: string;
  workspace_id: string;
  rule_group_id: string;
  rule_group_name?: string;
  scope_type: RuleGroupScopeType;
  scope_id: string | null;
  enabled: boolean;
  sort_order: number;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface EffectiveRuleLayerGroup {
  binding_id: string;
  rule_group_id: string;
  name: string;
  rule_count: number;
}

export interface EffectiveRuleLayer {
  scope_type: RuleGroupScopeType;
  scope_id: string | null;
  groups: EffectiveRuleLayerGroup[];
}

export interface EffectiveRule {
  id: string;
  rule_group_id: string;
  rule_group_name: string;
  scope_type: RuleGroupScopeType;
  name: string;
  description: string;
  content: string;
  sort_order: number;
  file_name: string | null;
  runtime_hints: Record<string, unknown>;
}

export interface EffectiveRulesResponse {
  workspace_id: string;
  inputs: {
    project_id: string | null;
    squad_id: string | null;
    agent_id: string | null;
  };
  layers: EffectiveRuleLayer[];
  rules: EffectiveRule[];
}

export interface CreateRuleGroupRequest {
  name: string;
  description: string;
  enabled?: boolean;
  version?: string;
}

export interface UpdateRuleGroupRequest {
  name?: string;
  description?: string;
  enabled?: boolean;
  version?: string;
}

export interface CreateRuleGroupRuleRequest {
  name: string;
  description: string;
  content: string;
  sort_order: number;
  enabled?: boolean;
  file_name?: string;
  tags?: string[];
  runtime_hints?: Record<string, unknown>;
}

export interface UpdateRuleGroupRuleRequest {
  name?: string;
  description?: string;
  content?: string;
  sort_order?: number;
  enabled?: boolean;
  file_name?: string;
  tags?: string[];
  runtime_hints?: Record<string, unknown>;
}

export interface CreateRuleGroupBindingRequest {
  rule_group_id: string;
  scope_type: RuleGroupScopeType;
  scope_id?: string | null;
  enabled?: boolean;
  sort_order: number;
}

export interface UpdateRuleGroupBindingRequest {
  enabled?: boolean;
  sort_order?: number;
}

/** source_type of platform-seeded rule groups; structurally read-only via API. */
export const RULE_GROUP_SOURCE_BUILTIN = "builtin";
