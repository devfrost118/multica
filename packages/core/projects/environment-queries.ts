import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  ListProjectEnvironmentsResponse,
  ProjectEnvironment,
  ProjectEnvironmentRequest,
} from "../types";
import { projectKeys } from "./queries";

export const projectEnvironmentKeys = {
  list: (wsId: string, projectId: string) =>
    [...projectKeys.detail(wsId, projectId), "environments"] as const,
};

export function projectEnvironmentsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectEnvironmentKeys.list(wsId, projectId),
    queryFn: () => api.listProjectEnvironments(projectId),
    select: (data) => data.environments,
  });
}

export function useCreateProjectEnvironment(wsId: string, projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: ProjectEnvironmentRequest) =>
      api.createProjectEnvironment(projectId, data),
    onSuccess: (created) => {
      qc.setQueryData<ListProjectEnvironmentsResponse>(
        projectEnvironmentKeys.list(wsId, projectId),
        (old) =>
          old && !old.environments.some((environment) => environment.id === created.id)
            ? {
                ...old,
                environments: [...old.environments, created],
                total: old.total + 1,
              }
            : old,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectEnvironmentKeys.list(wsId, projectId),
      });
    },
  });
}

export function useUpdateProjectEnvironment(wsId: string, projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      environmentId,
      data,
    }: {
      environmentId: string;
      data: ProjectEnvironmentRequest;
    }) => api.updateProjectEnvironment(projectId, environmentId, data),
    onSuccess: (updated) => {
      qc.setQueryData<ListProjectEnvironmentsResponse>(
        projectEnvironmentKeys.list(wsId, projectId),
        (old) =>
          old
            ? {
                ...old,
                environments: old.environments.map((environment) =>
                  environment.id === updated.id ? updated : environment,
                ),
              }
            : old,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectEnvironmentKeys.list(wsId, projectId),
      });
    },
  });
}

export function useDeleteProjectEnvironment(wsId: string, projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (environmentId: string) =>
      api.deleteProjectEnvironment(projectId, environmentId),
    onSuccess: (_data, environmentId) => {
      qc.setQueryData<ListProjectEnvironmentsResponse>(
        projectEnvironmentKeys.list(wsId, projectId),
        (old) =>
          old
            ? {
                ...old,
                environments: old.environments.filter(
                  (environment: ProjectEnvironment) =>
                    environment.id !== environmentId,
                ),
                total: old.total - 1,
              }
            : old,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectEnvironmentKeys.list(wsId, projectId),
      });
    },
  });
}

export function useRevealProjectEnvironment(wsId: string, projectId: string) {
  return useMutation({
    mutationFn: (environmentId: string) =>
      api.revealProjectEnvironment(projectId, environmentId),
    meta: {
      workspaceId: wsId,
    },
  });
}
