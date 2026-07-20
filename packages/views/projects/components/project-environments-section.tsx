"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Pencil, Plus, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import {
  projectEnvironmentsOptions,
  useCreateProjectEnvironment,
  useDeleteProjectEnvironment,
  useRevealProjectEnvironment,
  useUpdateProjectEnvironment,
} from "@multica/core/projects";
import { useWorkspaceId } from "@multica/core/hooks";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import type { AgentRuntime, ProjectEnvironment, ProjectEnvironmentRequest } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { useT } from "../../i18n";

type SecretPair = { key: string; value: string };

interface EnvironmentFormState {
  name: string;
  description: string;
  config: string;
  secrets: SecretPair[];
  allowedRuntimeIds: string[];
}

function initialForm(environment?: ProjectEnvironment): EnvironmentFormState {
  return {
    name: environment?.name ?? "",
    description: environment?.description ?? "",
    config: JSON.stringify(environment?.config ?? {}, null, 2),
    secrets: Object.entries(environment?.secrets ?? {}).map(([key, value]) => ({
      key,
      value,
    })),
    allowedRuntimeIds: environment?.allowed_runtime_ids ?? [],
  };
}

function runtimeLabel(runtime: AgentRuntime): string {
  return runtime.custom_name || runtime.name;
}

export function ProjectEnvironmentsSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const [editing, setEditing] = useState<ProjectEnvironment | null | undefined>(undefined);
  const { data: environments = [], isLoading, isError } = useQuery(
    projectEnvironmentsOptions(wsId, projectId),
  );
  const createEnvironment = useCreateProjectEnvironment(wsId, projectId);
  const updateEnvironment = useUpdateProjectEnvironment(wsId, projectId);
  const deleteEnvironment = useDeleteProjectEnvironment(wsId, projectId);
  const revealEnvironment = useRevealProjectEnvironment(wsId, projectId);

  const handleDelete = async (environment: ProjectEnvironment) => {
    try {
      await deleteEnvironment.mutateAsync(environment.id);
      toast.success(t(($) => $.environments.toast_deleted));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t(($) => $.environments.toast_delete_failed));
    }
  };

  const handleReveal = async (environment: ProjectEnvironment) => {
    try {
      const revealed = await revealEnvironment.mutateAsync(environment.id);
      setEditing({ ...environment, secrets: revealed.secrets });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t(($) => $.environments.toast_reveal_failed));
    }
  };

  return (
    <section className="space-y-2" aria-label={t(($) => $.environments.section_header)}>
      <div className="flex items-center justify-between gap-2">
        <div>
          <h3 className="text-xs font-medium">{t(($) => $.environments.section_header)}</h3>
          <p className="text-[11px] text-muted-foreground">{t(($) => $.environments.section_hint)}</p>
        </div>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-7 px-2 text-xs"
          onClick={() => setEditing(null)}
        >
          <Plus className="size-3" />
          {t(($) => $.environments.add_button)}
        </Button>
      </div>

      {isLoading ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.environments.loading)}</p>
      ) : isError ? (
        <p className="text-xs text-destructive">{t(($) => $.environments.load_failed)}</p>
      ) : environments.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.environments.empty)}</p>
      ) : (
        <div className="space-y-1">
          {environments.map((environment) => (
            <div key={environment.id} className="group flex items-center gap-2 rounded-md px-1 py-1 text-xs hover:bg-accent/50">
              <span className="min-w-0 flex-1 truncate font-medium">{environment.name}</span>
              {environment.allowed_runtime_ids.length > 0 && (
                <span className="text-[10px] text-muted-foreground">{t(($) => $.environments.runtime_count, { count: environment.allowed_runtime_ids.length })}</span>
              )}
              <button
                type="button"
                className="rounded-sm p-0.5 hover:bg-accent"
                aria-label={t(($) => $.environments.edit_aria, { name: environment.name })}
                onClick={() => setEditing(environment)}
              >
                <Pencil className="size-3 text-muted-foreground" />
              </button>
              <button
                type="button"
                className="rounded-sm p-0.5 hover:bg-accent"
                aria-label={t(($) => $.environments.delete_aria, { name: environment.name })}
                onClick={() => void handleDelete(environment)}
                disabled={deleteEnvironment.isPending}
              >
                <Trash2 className="size-3 text-muted-foreground" />
              </button>
            </div>
          ))}
        </div>
      )}

      {editing !== undefined && (
        <EnvironmentDialog
          environment={editing}
          projectId={projectId}
          wsId={wsId}
          saving={createEnvironment.isPending || updateEnvironment.isPending}
          revealing={revealEnvironment.isPending}
          onClose={() => setEditing(undefined)}
          onCreate={async (data) => {
            await createEnvironment.mutateAsync(data);
            toast.success(t(($) => $.environments.toast_created));
            setEditing(undefined);
          }}
          onUpdate={async (environmentId, data) => {
            await updateEnvironment.mutateAsync({ environmentId, data });
            toast.success(t(($) => $.environments.toast_updated));
            setEditing(undefined);
          }}
          onReveal={async () => {
            if (editing) await handleReveal(editing);
          }}
        />
      )}
    </section>
  );
}

interface EnvironmentDialogProps {
  environment: ProjectEnvironment | null;
  projectId: string;
  wsId: string;
  saving: boolean;
  revealing: boolean;
  onClose: () => void;
  onCreate: (data: ProjectEnvironmentRequest) => Promise<void>;
  onUpdate: (environmentId: string, data: ProjectEnvironmentRequest) => Promise<void>;
  onReveal: () => Promise<void>;
}

function EnvironmentDialog({
  environment,
  wsId,
  saving,
  revealing,
  onClose,
  onCreate,
  onUpdate,
  onReveal,
}: EnvironmentDialogProps) {
  const { t } = useT("projects");
  const [form, setForm] = useState(() => initialForm(environment ?? undefined));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));
  const existingSecretKeys = useMemo(
    () => new Set(environment ? Object.keys(environment.secrets) : []),
    [environment],
  );

  useEffect(() => {
    if (!environment) return;
    setForm(initialForm(environment));
    setErrors({});
  }, [environment]);

  const setSecret = (index: number, patch: Partial<SecretPair>) => {
    setForm((current) => ({
      ...current,
      secrets: current.secrets.map((secret, secretIndex) =>
        secretIndex === index ? { ...secret, ...patch } : secret,
      ),
    }));
  };

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const nextErrors: Record<string, string> = {};
    const name = form.name.trim();
    if (!name) nextErrors.name = t(($) => $.environments.name_required);

    let config: Record<string, unknown> = {};
    try {
      const parsed = JSON.parse(form.config || "{}");
      if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error("invalid config");
      config = parsed as Record<string, unknown>;
    } catch {
      nextErrors.config = t(($) => $.environments.config_invalid);
    }

    const secrets: Record<string, string> = {};
    form.secrets.forEach(({ key, value }) => {
      const trimmedKey = key.trim();
      if (!trimmedKey && !value) return;
      if (!trimmedKey) {
        nextErrors.secrets = t(($) => $.environments.secret_name_required);
        return;
      }
      if (secrets[trimmedKey] !== undefined) {
        nextErrors.secrets = t(($) => $.environments.secret_name_duplicate);
        return;
      }
      secrets[trimmedKey] = value;
    });

    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    const data: ProjectEnvironmentRequest = {
      name,
      description: form.description.trim() || null,
      config,
      secrets,
      allowed_runtime_ids: form.allowedRuntimeIds,
    };
    try {
      if (environment) await onUpdate(environment.id, data);
      else await onCreate(data);
    } catch (error) {
      setErrors({ submit: error instanceof Error ? error.message : t(($) => $.environments.save_failed) });
    }
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !saving) onClose(); }}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{environment ? t(($) => $.environments.edit_title) : t(($) => $.environments.create_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.environments.dialog_description)}</DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={(event) => void submit(event)}>
          <Field label={t(($) => $.environments.name_label)} error={errors.name}>
            <input
              aria-label={t(($) => $.environments.name_label)}
              value={form.name}
              onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
              className="w-full rounded-md border bg-transparent px-3 py-2 text-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          </Field>
          <Field label={t(($) => $.environments.description_label)}>
            <textarea
              aria-label={t(($) => $.environments.description_label)}
              value={form.description}
              onChange={(event) => setForm((current) => ({ ...current, description: event.target.value }))}
              className="min-h-16 w-full rounded-md border bg-transparent px-3 py-2 text-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          </Field>
          <Field label={t(($) => $.environments.config_label)} error={errors.config}>
            <textarea
              aria-label={t(($) => $.environments.config_label)}
              value={form.config}
              onChange={(event) => setForm((current) => ({ ...current, config: event.target.value }))}
              className="min-h-24 w-full rounded-md border bg-transparent px-3 py-2 font-mono text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          </Field>
          <div className="space-y-2">
            <div className="flex items-center justify-between"><span className="text-sm font-medium">{t(($) => $.environments.secrets_label)}</span><Button type="button" size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={() => setForm((current) => ({ ...current, secrets: [...current.secrets, { key: "", value: "" }] }))}><Plus className="size-3" />{t(($) => $.environments.add_secret)}</Button></div>
            {form.secrets.map((secret, index) => {
              const canRename = !existingSecretKeys.has(secret.key);
              return <div key={`${secret.key}-${index}`} className="flex gap-2"><input aria-label={t(($) => $.environments.secret_name_label)} value={secret.key} disabled={!canRename} onChange={(event) => setSecret(index, { key: event.target.value })} placeholder={t(($) => $.environments.secret_name_label)} className="w-32 rounded-md border bg-transparent px-2 py-1.5 text-xs outline-none disabled:opacity-70" /><input aria-label={t(($) => $.environments.secret_value_label, { name: secret.key || t(($) => $.environments.secret_name_label) })} value={secret.value} onChange={(event) => setSecret(index, { value: event.target.value })} className="min-w-0 flex-1 rounded-md border bg-transparent px-2 py-1.5 text-xs outline-none" /><button type="button" aria-label={t(($) => $.environments.remove_secret_aria, { name: secret.key || t(($) => $.environments.secret_name_label) })} onClick={() => setForm((current) => ({ ...current, secrets: current.secrets.filter((_, secretIndex) => secretIndex !== index) }))} className="rounded-sm p-1 hover:bg-accent"><X className="size-3 text-muted-foreground" /></button></div>;
            })}
            {errors.secrets && <p className="text-xs text-destructive">{errors.secrets}</p>}
          </div>
          <div className="space-y-2"><span className="text-sm font-medium">{t(($) => $.environments.runtimes_label)}</span>{runtimes.length === 0 ? <p className="text-xs text-muted-foreground">{t(($) => $.environments.runtimes_empty)}</p> : runtimes.map((runtime) => <label key={runtime.id} className="flex items-center gap-2 text-xs"><input type="checkbox" checked={form.allowedRuntimeIds.includes(runtime.id)} onChange={() => setForm((current) => ({ ...current, allowedRuntimeIds: current.allowedRuntimeIds.includes(runtime.id) ? current.allowedRuntimeIds.filter((id) => id !== runtime.id) : [...current.allowedRuntimeIds, runtime.id] }))} />{runtimeLabel(runtime)}</label>)}</div>
          {environment && <Button type="button" variant="outline" size="sm" aria-label={t(($) => $.environments.reveal_aria, { name: environment.name })} onClick={() => void onReveal()} disabled={revealing}>{revealing ? t(($) => $.environments.revealing) : t(($) => $.environments.reveal_button)}</Button>}
          {errors.submit && <p className="text-xs text-destructive">{errors.submit}</p>}
          <DialogFooter><Button type="button" variant="outline" onClick={onClose} disabled={saving}>{t(($) => $.environments.cancel)}</Button><Button type="submit" disabled={saving}>{saving ? t(($) => $.environments.saving) : t(($) => $.environments.save_button)}</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Field({ label, error, children }: { label: string; error?: string; children: React.ReactNode }) {
  return <label className="block space-y-1"><span className="text-sm font-medium">{label}</span>{children}{error && <span className="block text-xs text-destructive">{error}</span>}</label>;
}
