import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

type MemberRole = "owner" | "admin" | "member";

const groupsRef = vi.hoisted(() => ({
  current: [] as Array<Record<string, unknown>>,
}));
const roleRef = vi.hoisted(() => ({ current: "owner" as MemberRole }));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("list")) return { data: groupsRef.current, isLoading: false };
    return { data: undefined, isLoading: false };
  },
  useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/permissions", () => ({
  useCurrentMember: () => ({ role: roleRef.current, userId: "user-1", member: null, isLoading: false }),
}));

vi.mock("@multica/core/rule-groups", () => {
  const mutationStub = () => ({ mutateAsync: vi.fn().mockResolvedValue({}), isPending: false });
  return {
    ruleGroupsListOptions: (wsId: string) => ({ queryKey: ["rule-groups", wsId, "list"] }),
    ruleGroupDetailOptions: (wsId: string, id: string) => ({
      queryKey: ["rule-groups", wsId, "detail", id],
    }),
    useUpdateRuleGroup: mutationStub,
    useDeleteRuleGroup: mutationStub,
    useCreateRuleGroup: mutationStub,
    RULE_GROUP_SOURCE_BUILTIN: "builtin",
  };
});

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { RuleGroupsTab } from "./rule-groups-tab";

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function Wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function group(overrides: Record<string, unknown> = {}) {
  return {
    id: "g1",
    workspace_id: "workspace-1",
    name: "Runtime Hygiene",
    description: "Keep agents tidy",
    enabled: true,
    source_type: "manual",
    source_ref: {},
    version: null,
    created_by: null,
    created_at: "",
    updated_at: "",
    rule_count: 3,
    binding_count: 1,
    ...overrides,
  };
}

describe("RuleGroupsTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    groupsRef.current = [];
    roleRef.current = "owner";
  });

  it("shows the empty state when there are no rule groups", () => {
    render(<RuleGroupsTab />, { wrapper: Wrapper });
    expect(screen.getByText("No rule groups yet.")).toBeTruthy();
  });

  it("renders a group with its rule and binding counts and a Create button for admins", () => {
    groupsRef.current = [group()];
    render(<RuleGroupsTab />, { wrapper: Wrapper });
    expect(screen.getByText("Runtime Hygiene")).toBeTruthy();
    expect(screen.getByText("3 rules")).toBeTruthy();
    expect(screen.getByText("1 bindings")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Create group/ })).toBeTruthy();
  });

  it("marks builtin groups and hides their edit/delete menu", () => {
    groupsRef.current = [group({ id: "b1", name: "Multica Core", source_type: "builtin" })];
    render(<RuleGroupsTab />, { wrapper: Wrapper });
    expect(screen.getByText("Built-in")).toBeTruthy();
    // The overflow menu (edit/delete) is the only MoreHorizontal trigger and is
    // omitted for builtin groups.
    expect(screen.queryByRole("button", { name: /more/i })).toBeNull();
  });

  it("hides management controls and shows a hint for non-admins", () => {
    roleRef.current = "member";
    groupsRef.current = [group()];
    render(<RuleGroupsTab />, { wrapper: Wrapper });
    expect(screen.queryByRole("button", { name: /Create group/ })).toBeNull();
    expect(
      screen.getByText("Only admins and owners can manage rule groups."),
    ).toBeTruthy();
    // No enable/disable switch for non-admins.
    expect(screen.queryByRole("switch")).toBeNull();
  });
});
