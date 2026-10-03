// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";

const updateMock = vi.fn().mockResolvedValue({});

const RESOURCE = {
  id: "res-1",
  project_id: "p1",
  workspace_id: "workspace-1",
  resource_type: "local_directory",
  resource_ref: {
    local_path: "/Users/dev/work/game-client",
    daemon_id: "daemon-1",
    label: "Game Client",
    // The setting this test exists to protect.
    execution_mode: "worktree",
  },
  // A row renamed since: the top-level column holds the current name while the
  // ref still carries the one it was created with.
  label: "Renamed Client",
  position: 0,
  created_at: "2026-08-18T00:00:00Z",
  created_by: "u1",
};

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: unknown[] }) => {
    const key = options?.queryKey?.[0];
    if (key === "project-resources") return { data: [RESOURCE] };
    return { data: [] };
  },
  queryOptions: (options: unknown) => options,
}));

vi.mock("@multica/core/projects", () => ({
  projectEnvironmentsOptions: () => ({ queryKey: ["environments"], queryFn: vi.fn() }),
  projectResourcesOptions: () => ({ queryKey: ["project-resources"], queryFn: vi.fn() }),
  useCreateProjectResource: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateProjectResource: () => ({ mutateAsync: updateMock, isPending: false }),
  useDeleteProjectResource: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCreateProjectEnvironment: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateProjectEnvironment: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteProjectEnvironment: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useRevealProjectEnvironment: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

// A backend that predates the capability signal: the client must assume it
// would silently drop execution_mode.
vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (state: { localWorktreeSupported: boolean }) => unknown) =>
    selector({ localWorktreeSupported: false }),
}));

vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"], queryFn: vi.fn() }),
  runtimeAdvertisesLocalWorktree: () => true,
}));
vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeListOptions: () => ({ queryKey: ["runtimes"], queryFn: vi.fn() }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "workspace-1", slug: "ws", repos: [] }),
}));
vi.mock("../../platform/local-directory", () => ({
  isDesktopShell: () => true,
  pickDirectory: vi.fn(),
  validateLocalDirectory: vi.fn(),
}));
vi.mock("../../platform/use-local-daemon-status", () => ({
  useLocalDaemonStatus: () => ({ daemonId: "daemon-1", deviceName: "MacBook", running: true }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

vi.mock("./project-environments-section", () => ({
  ProjectEnvironmentsSection: () => null,
}));

import { ProjectResourcesSection } from "./project-resources-section";

describe("ProjectResourcesSection — local directory label after MUL-7525", () => {
  beforeEach(() => updateMock.mockClear());

  // Upstream v0.6.1 removed the pencil: a folder is identified by its path,
  // and a rename control beside mode/remove read as a broken edit (MUL-7525).
  // The old label-only PATCH path is gone, so it cannot resend a stripped ref
  // on an outdated server (#7113).
  it("does not expose a rename control that could resend a stripped ref", () => {
    renderWithI18n(<ProjectResourcesSection projectId="p1" />);
    expect(screen.queryByTitle(/rename/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(updateMock).not.toHaveBeenCalled();
  });

  // The stored name still has to be what the user sees, which only holds if
  // the top-level label outranks the stale one still sitting inside the ref.
  // Full read-order matrix: local-directory-label.test.ts.
  it("shows the top-level label over the one left behind in the ref", () => {
    renderWithI18n(<ProjectResourcesSection projectId="p1" />);
    expect(screen.getByText("Renamed Client")).toBeInTheDocument();
    expect(screen.queryByText("Game Client")).not.toBeInTheDocument();
  });

  // The remaining write path is mode edit. The server replaces the whole ref,
  // so this update must spread every stored field — including execution_mode
  // and the leftover label copy — rather than sending a partial object.
  it("spreads the stored ref when the remaining edit path writes execution_mode", async () => {
    renderWithI18n(<ProjectResourcesSection projectId="p1" />);

    fireEvent.click(screen.getByTitle(/change how runs use this folder/i));
    fireEvent.click(screen.getByRole("radio", { name: /edit this folder directly/i }));
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(updateMock).toHaveBeenCalledTimes(1));
    const payload = updateMock.mock.calls[0]?.[0] as {
      resourceId: string;
      data: { resource_ref: Record<string, unknown> };
    };
    expect(payload.resourceId).toBe("res-1");
    expect(payload.data.resource_ref).toEqual({
      local_path: "/Users/dev/work/game-client",
      daemon_id: "daemon-1",
      label: "Game Client",
      execution_mode: "in_place",
    });
  });
});
