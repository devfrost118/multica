import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { SaveProviderCredentialRequest } from "../types";
import { providerLimitKeys } from "./queries";

export function useProviderCredentials(wsId: string, enabled = true) {
  return useQuery({
    queryKey: [...providerLimitKeys.all(wsId), "credentials"],
    queryFn: () => api.getProviderCredentials(),
    enabled: enabled && !!wsId,
  });
}

export function useSaveProviderCredential(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, request }: { id?: string; request: SaveProviderCredentialRequest }) =>
      id ? api.replaceProviderCredential(id, request.token) : api.createProviderCredential(request),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: providerLimitKeys.all(wsId) });
    },
  });
}

export function useDeleteProviderCredential(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteProviderCredential(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: providerLimitKeys.all(wsId) });
    },
  });
}