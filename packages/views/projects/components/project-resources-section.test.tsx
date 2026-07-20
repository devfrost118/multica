import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithI18n } from "../../test/i18n";
import { ProjectResourcesSection } from "./project-resources-section";

const mocks = vi.hoisted(() => ({
  environments: [] as Array<{
    id: string;
    name: string;
    description: string | null;
    config: Record<string, unknown>;
    secrets: Record<string, string>;
    allowed_runtime_ids: string[];
    project_id: string;
    workspace_id: string;
    created_by: string | null;
    created_at: string;
    updated_at: string;
  }>,
  environmentsLoading: false,
  environmentsError: false,
  createEnvironment: vi.fn(),
  updateEnvironment: vi.fn(),
  deleteEnvironment: vi.fn(),
  revealEnvironment: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: readonly unknown[] }) => {
    const key = options.queryKey?.at(-1);
    if (key === "environments") {
      return {
        data: mocks.environments,
        isLoading: mocks.environmentsLoading,
        isError: mocks.environmentsError,
      };
    }
    return { data: [], isLoading: false, isError: false };
  },
}));

vi.mock("@multica/core/projects", () => ({
  projectResourcesOptions: () => ({ queryKey: ["resources"] }),
  projectEnvironmentsOptions: () => ({ queryKey: ["environments"] }),
  useCreateProjectResource: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateProjectResource: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteProjectResource: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCreateProjectEnvironment: () => ({
    mutateAsync: mocks.createEnvironment,
    isPending: false,
  }),
  useUpdateProjectEnvironment: () => ({
    mutateAsync: mocks.updateEnvironment,
    isPending: false,
  }),
  useDeleteProjectEnvironment: () => ({
    mutateAsync: mocks.deleteEnvironment,
    isPending: false,
  }),
  useRevealProjectEnvironment: () => ({
    mutateAsync: mocks.revealEnvironment,
    isPending: false,
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ repos: [] }),
}));

vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"] }),
}));

vi.mock("../../platform", () => ({
  isDesktopShell: () => false,
  pickDirectory: vi.fn(),
  useLocalDaemonStatus: () => ({ daemonId: null, running: false }),
  validateLocalDirectory: vi.fn(),
}));

function renderSection() {
  return renderWithI18n(<ProjectResourcesSection projectId="project-1" />);
}

describe("ProjectResourcesSection environments", () => {
  beforeEach(() => {
    mocks.environments = [];
    mocks.environmentsLoading = false;
    mocks.environmentsError = false;
    mocks.createEnvironment.mockReset();
    mocks.updateEnvironment.mockReset();
    mocks.deleteEnvironment.mockReset();
    mocks.revealEnvironment.mockReset();
  });

  it("creates an environment only after a name is supplied", async () => {
    const user = userEvent.setup();
    renderSection();

    expect(screen.getByText("Technical environments")).toBeInTheDocument();
    expect(screen.getByText("No environments configured.")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add environment" }));
    await user.click(screen.getByRole("button", { name: "Save environment" }));
    expect(screen.getByText("Name is required")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Environment name"), "Staging");
    await user.clear(screen.getByLabelText("Configuration (JSON)"));
    await user.type(screen.getByLabelText("Configuration (JSON)"), "not JSON");
    await user.click(screen.getByRole("button", { name: "Save environment" }));
    expect(screen.getByText("Configuration must be valid JSON")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Configuration (JSON)"), {
      target: { value: '{"region":"eu"}' },
    });
    await user.click(screen.getByRole("button", { name: "Add secret" }));
    fireEvent.change(screen.getByLabelText("Secret name"), {
      target: { value: "API_TOKEN" },
    });
    await user.type(screen.getAllByRole("textbox")[4]!, "secret-value");
    await user.click(screen.getByRole("button", { name: "Save environment" }));

    expect(mocks.createEnvironment).toHaveBeenCalledWith({
      name: "Staging",
      description: null,
      config: { region: "eu" },
      secrets: { API_TOKEN: "secret-value" },
      allowed_runtime_ids: [],
    });
  });

  it("preserves masked secrets until the user explicitly reveals them", async () => {
    const user = userEvent.setup();
    mocks.environments = [
      {
        id: "environment-1",
        name: "Staging",
        description: "Pre-production",
        config: {},
        secrets: { API_TOKEN: "****" },
        allowed_runtime_ids: [],
        project_id: "project-1",
        workspace_id: "workspace-1",
        created_by: "member-1",
        created_at: "2026-07-16T00:00:00Z",
        updated_at: "2026-07-16T00:00:00Z",
      },
    ];
    mocks.revealEnvironment.mockResolvedValue({
      id: "environment-1",
      project_id: "project-1",
      workspace_id: "workspace-1",
      name: "Staging",
      secrets: { API_TOKEN: "actual-token" },
    });

    renderSection();
    await user.click(
      screen.getByRole("button", { name: "Edit Staging environment" }),
    );

    expect(screen.getByLabelText("Secret API_TOKEN")).toHaveValue("****");
    await user.click(screen.getByRole("button", { name: "Save environment" }));
    expect(mocks.updateEnvironment).toHaveBeenCalledWith({
      environmentId: "environment-1",
      data: expect.objectContaining({ secrets: { API_TOKEN: "****" } }),
    });

    await user.click(
      screen.getByRole("button", { name: "Edit Staging environment" }),
    );
    await user.click(
      screen.getByRole("button", { name: "Reveal secrets for Staging" }),
    );
    expect(mocks.revealEnvironment).toHaveBeenCalledWith("environment-1");
    expect(screen.getByLabelText("Secret API_TOKEN")).toHaveValue(
      "actual-token",
    );
  });

  it("renders loading and error states without exposing environment values", () => {
    mocks.environmentsLoading = true;
    const { rerender } = renderSection();
    expect(screen.getByText("Loading environments...")).toBeInTheDocument();

    mocks.environmentsLoading = false;
    mocks.environmentsError = true;
    rerender(<ProjectResourcesSection projectId="project-1" />);
    expect(screen.getByText("Failed to load environments.")).toBeInTheDocument();
  });
});
