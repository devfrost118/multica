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
const detailRef = vi.hoisted(() => ({
  current: { rules: [] as Array<Record<string, unknown>> },
}));
const roleRef = vi.hoisted(() => ({ current: "owner" as MemberRole }));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("detail")) return { data: detailRef.current, isLoading: false };
    if (key.includes("list")) return { data: groupsRef.current, isLoading: false };
    return { data: undefined, isLoading: false };
  },
  useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  queryOptions: <T,>(opts: T) => opts,
}));

// react-resizable-panels touches layout APIs jsdom lacks; the page only needs
// the hook's shape, so stub it. We render the mobile (stacked) layout below to
// avoid mounting the panel group at all.
vi.mock("react-resizable-panels", () => ({
  useDefaultLayout: () => ({ defaultLayout: undefined, onLayoutChanged: vi.fn() }),
}));
vi.mock("@multica/ui/components/ui/resizable", () => ({
  ResizablePanelGroup: ({ children }: { children: ReactNode }) => <>{children}</>,
  ResizablePanel: ({ children }: { children: ReactNode }) => <>{children}</>,
  ResizableHandle: () => null,
}));
vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => true }));

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
    useCreateRuleGroupRule: mutationStub,
    useUpdateRuleGroupRule: mutationStub,
    useDeleteRuleGroupRule: mutationStub,
    RULE_GROUP_SOURCE_BUILTIN: "builtin",
  };
});

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { RulesPage } from "./rules-page";

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

describe("RulesPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    groupsRef.current = [];
    detailRef.current = { rules: [] };
    roleRef.current = "owner";
  });

  it("shows the empty state and a select-a-group prompt when there are no groups", () => {
    render(<RulesPage />, { wrapper: Wrapper });
    expect(screen.getByText("No rule groups yet.")).toBeTruthy();
    expect(screen.getByText("Select a rule group to see its rules.")).toBeTruthy();
  });

  it("auto-selects the first group and renders its counts in the detail panel", () => {
    groupsRef.current = [group()];
    render(<RulesPage />, { wrapper: Wrapper });
    // Name appears in both the list row and the detail header.
    expect(screen.getAllByText("Runtime Hygiene").length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("3 rules").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("1 bindings").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByRole("button", { name: /Create group/ })).toBeTruthy();
  });

  it("renders the rules of the selected group in the detail panel", () => {
    groupsRef.current = [group()];
    detailRef.current = {
      rules: [
        {
          id: "r1",
          name: "Always run tests",
          content: "Run the suite before claiming done.",
          enabled: true,
          file_name: "completion.md",
        },
      ],
    };
    render(<RulesPage />, { wrapper: Wrapper });
    expect(screen.getByText("Always run tests")).toBeTruthy();
    expect(screen.getByText("completion.md")).toBeTruthy();
  });

  it("marks builtin groups read-only: shows the hint and hides the Add rule action", () => {
    groupsRef.current = [group({ id: "b1", name: "Multica Core", source_type: "builtin" })];
    render(<RulesPage />, { wrapper: Wrapper });
    expect(screen.getAllByText("Built-in").length).toBeGreaterThanOrEqual(1);
    expect(
      screen.getByText(
        "Built-in groups are managed by the platform. You can enable or disable them, but not edit their content.",
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Add rule/ })).toBeNull();
  });

  it("hides management controls and shows a hint for non-admins", () => {
    roleRef.current = "member";
    groupsRef.current = [group()];
    render(<RulesPage />, { wrapper: Wrapper });
    expect(screen.queryByRole("button", { name: /Create group/ })).toBeNull();
    expect(
      screen.getByText("Only admins and owners can manage rule groups."),
    ).toBeTruthy();
  });
});
