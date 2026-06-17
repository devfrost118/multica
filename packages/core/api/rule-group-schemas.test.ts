import { describe, expect, it } from "vitest";
import {
  RuleGroupSummaryListSchema,
  RuleGroupWithRulesSchema,
  RuleGroupBindingListSchema,
  EffectiveRulesResponseSchema,
  EMPTY_RULE_GROUP_SUMMARY_LIST,
  EMPTY_RULE_GROUP_WITH_RULES,
  EMPTY_RULE_GROUP_BINDING_LIST,
  EMPTY_EFFECTIVE_RULES,
} from "./schemas";
import { parseWithFallback } from "./schema";

const ENDPOINT = { endpoint: "test" };

describe("RuleGroupSummaryListSchema", () => {
  it("parses a well-formed summary list and tolerates extra fields", () => {
    const payload = [
      {
        id: "g1",
        workspace_id: "ws-1",
        name: "Runtime Hygiene",
        description: "",
        enabled: true,
        source_type: "manual",
        source_ref: {},
        version: null,
        created_by: null,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
        rule_count: 3,
        binding_count: 1,
        future_field: "ignored",
      },
    ];
    const parsed = parseWithFallback(
      payload,
      RuleGroupSummaryListSchema,
      EMPTY_RULE_GROUP_SUMMARY_LIST,
      ENDPOINT,
    );
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.name).toBe("Runtime Hygiene");
    expect(parsed[0]?.rule_count).toBe(3);
  });

  it("falls back when the body is not an array", () => {
    const parsed = parseWithFallback(
      { not: "an array" },
      RuleGroupSummaryListSchema,
      EMPTY_RULE_GROUP_SUMMARY_LIST,
      ENDPOINT,
    );
    expect(parsed).toBe(EMPTY_RULE_GROUP_SUMMARY_LIST);
  });

  it("defaults missing optional fields instead of throwing", () => {
    const parsed = RuleGroupSummaryListSchema.parse([
      { id: "g1", workspace_id: "ws-1" },
    ]);
    expect(parsed[0]?.enabled).toBe(true);
    expect(parsed[0]?.rule_count).toBe(0);
    expect(parsed[0]?.source_ref).toEqual({});
  });
});

describe("RuleGroupWithRulesSchema", () => {
  it("falls back when rules is null", () => {
    const parsed = parseWithFallback(
      {
        id: "g1",
        workspace_id: "ws-1",
        name: "G",
        rules: null,
      },
      RuleGroupWithRulesSchema,
      EMPTY_RULE_GROUP_WITH_RULES,
      ENDPOINT,
    );
    // `rules: null` violates the array schema → fallback (never undefined).
    expect(parsed.rules).toEqual([]);
  });

  it("parses nested rules", () => {
    const parsed = RuleGroupWithRulesSchema.parse({
      id: "g1",
      workspace_id: "ws-1",
      name: "G",
      rules: [
        {
          id: "r1",
          workspace_id: "ws-1",
          rule_group_id: "g1",
          name: "Rule",
          content: "do the thing",
        },
      ],
    });
    expect(parsed.rules[0]?.name).toBe("Rule");
    expect(parsed.rules[0]?.tags).toEqual([]);
  });
});

describe("RuleGroupBindingListSchema", () => {
  it("keeps an unknown scope_type instead of crashing (enum drift downgrades)", () => {
    const parsed = parseWithFallback(
      [
        {
          id: "b1",
          workspace_id: "ws-1",
          rule_group_id: "g1",
          scope_type: "future_scope",
          scope_id: "x",
          enabled: true,
          sort_order: 0,
          created_by: null,
          created_at: "",
          updated_at: "",
        },
      ],
      RuleGroupBindingListSchema,
      EMPTY_RULE_GROUP_BINDING_LIST,
      ENDPOINT,
    );
    expect(parsed[0]?.scope_type).toBe("future_scope");
  });
});

describe("EffectiveRulesResponseSchema", () => {
  it("falls back on a malformed body", () => {
    const parsed = parseWithFallback(
      "not json object",
      EffectiveRulesResponseSchema,
      EMPTY_EFFECTIVE_RULES,
      ENDPOINT,
    );
    expect(parsed).toBe(EMPTY_EFFECTIVE_RULES);
  });

  it("defaults layers and rules to empty arrays when absent", () => {
    const parsed = EffectiveRulesResponseSchema.parse({ workspace_id: "ws-1" });
    expect(parsed.layers).toEqual([]);
    expect(parsed.rules).toEqual([]);
    expect(parsed.inputs).toEqual({ project_id: null, squad_id: null, agent_id: null });
  });
});
