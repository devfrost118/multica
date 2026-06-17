"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ShieldCheck, Plus, Trash2, ChevronRight, Eye } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@multica/ui/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { toast } from "sonner";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentMember } from "@multica/core/permissions";
import {
  ruleGroupBindingsOptions,
  ruleGroupsListOptions,
  effectiveRulesOptions,
  useCreateRuleGroupBinding,
  useDeleteRuleGroupBinding,
  useUpdateRuleGroupBinding,
  type RuleGroupBinding,
  type RuleGroupScopeType,
} from "@multica/core/rule-groups";
import { useT } from "../../i18n";

type BindableScope = "project" | "squad" | "agent";

const SCOPE_LABEL_KEY: Record<BindableScope, "scope_project" | "scope_squad" | "scope_agent"> = {
  project: "scope_project",
  squad: "scope_squad",
  agent: "scope_agent",
};

const LAYER_LABEL_KEY: Record<
  RuleGroupScopeType,
  "layer_workspace" | "layer_project" | "layer_squad" | "layer_agent"
> = {
  workspace: "layer_workspace",
  project: "layer_project",
  squad: "layer_squad",
  agent: "layer_agent",
};

/**
 * Attach/detach rule groups to a single project, squad, or agent and preview
 * the effective rules that apply at this scope. Reused across the project,
 * squad, and agent detail views; the backend resolves the workspace from the
 * request header so only the scope id is passed.
 */
export function RuleGroupsBindingSection({
  scopeType,
  scopeId,
}: {
  scopeType: BindableScope;
  scopeId: string;
}) {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const { role } = useCurrentMember(wsId);
  const canManage = role === "owner" || role === "admin";

  const [isOpen, setIsOpen] = useState(true);
  const [attachOpen, setAttachOpen] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [detachTarget, setDetachTarget] = useState<RuleGroupBinding | null>(null);

  const { data: bindings, isLoading } = useQuery(
    ruleGroupBindingsOptions(wsId, scopeType, scopeId),
  );
  const { data: allGroups } = useQuery(ruleGroupsListOptions(wsId));

  const createBinding = useCreateRuleGroupBinding();
  const deleteBinding = useDeleteRuleGroupBinding();
  const updateBinding = useUpdateRuleGroupBinding();

  const boundGroupIds = new Set((bindings ?? []).map((b) => b.rule_group_id));
  const available = (allGroups ?? []).filter((g) => !boundGroupIds.has(g.id));
  const scopeLabel = t(($) => $.rule_groups.bindings[SCOPE_LABEL_KEY[scopeType]]);

  const handleAttach = async (ruleGroupId: string) => {
    try {
      await createBinding.mutateAsync({
        rule_group_id: ruleGroupId,
        scope_type: scopeType,
        scope_id: scopeId,
        sort_order: bindings?.length ?? 0,
      });
      toast.success(t(($) => $.rule_groups.bindings.toast_attached));
      setAttachOpen(false);
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.bindings.toast_attach_failed),
      );
    }
  };

  const handleToggle = async (binding: RuleGroupBinding, enabled: boolean) => {
    try {
      await updateBinding.mutateAsync({ id: binding.id, enabled });
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.bindings.toast_toggle_failed),
      );
    }
  };

  const handleDetach = async () => {
    if (!detachTarget) return;
    try {
      await deleteBinding.mutateAsync(detachTarget.id);
      toast.success(t(($) => $.rule_groups.bindings.toast_detached));
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.bindings.toast_detach_failed),
      );
    } finally {
      setDetachTarget(null);
    }
  };

  return (
    <div>
      <button
        type="button"
        className={`mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70 ${isOpen ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setIsOpen(!isOpen)}
      >
        {t(($) => $.rule_groups.bindings.section_title)}
        <ChevronRight
          className={`!size-3 shrink-0 text-muted-foreground transition-transform ${isOpen ? "rotate-90" : ""}`}
        />
      </button>

      {isOpen && (
        <div className="space-y-2 pl-2">
          {isLoading ? (
            <Skeleton className="h-12 w-full" />
          ) : !bindings || bindings.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.rule_groups.bindings.empty)}
            </p>
          ) : (
            <ul className="space-y-1">
              {bindings.map((b) => (
                <li
                  key={b.id}
                  className="flex items-center justify-between gap-2 rounded-md border bg-card p-2 text-sm"
                >
                  <div className="flex min-w-0 items-center gap-2">
                    <ShieldCheck className="h-4 w-4 shrink-0 text-muted-foreground" />
                    <span className="truncate">{b.rule_group_name}</span>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    {canManage && (
                      <Switch
                        checked={b.enabled}
                        onCheckedChange={(v) => handleToggle(b, v)}
                        aria-label={b.rule_group_name}
                      />
                    )}
                    {canManage && (
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="h-6 w-6"
                        onClick={() => setDetachTarget(b)}
                        title={t(($) => $.rule_groups.bindings.detach_tooltip)}
                      >
                        <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
                      </Button>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}

          <div className="flex flex-wrap gap-2">
            {canManage && (
              <Button
                variant="outline"
                size="sm"
                className="h-7 text-xs"
                onClick={() => setAttachOpen(true)}
              >
                <Plus className="mr-1.5 h-3.5 w-3.5" />
                {t(($) => $.rule_groups.bindings.attach)}
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              className="h-7 text-xs"
              onClick={() => setPreviewOpen(true)}
            >
              <Eye className="mr-1.5 h-3.5 w-3.5" />
              {t(($) => $.rule_groups.bindings.preview)}
            </Button>
          </div>
        </div>
      )}

      {/* Attach picker */}
      <Dialog open={attachOpen} onOpenChange={setAttachOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.rule_groups.bindings.attach_dialog_title)}</DialogTitle>
          </DialogHeader>
          {!allGroups || allGroups.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted-foreground">
              {t(($) => $.rule_groups.bindings.no_groups)}
            </p>
          ) : available.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted-foreground">
              {t(($) => $.rule_groups.bindings.none_available)}
            </p>
          ) : (
            <div className="max-h-[50vh] space-y-1.5 overflow-y-auto">
              {available.map((g) => (
                <button
                  key={g.id}
                  type="button"
                  disabled={createBinding.isPending}
                  onClick={() => handleAttach(g.id)}
                  className="flex w-full items-center gap-2 rounded-md border bg-card p-3 text-left text-sm transition-colors hover:bg-accent/60 disabled:opacity-60"
                >
                  <ShieldCheck className="h-4 w-4 shrink-0 text-muted-foreground" />
                  <div className="min-w-0">
                    <div className="truncate font-medium">{g.name}</div>
                    {g.description && (
                      <div className="truncate text-xs text-muted-foreground">
                        {g.description}
                      </div>
                    )}
                  </div>
                </button>
              ))}
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* Effective rules preview */}
      <EffectivePreviewDialog
        open={previewOpen}
        onOpenChange={setPreviewOpen}
        wsId={wsId}
        scopeType={scopeType}
        scopeId={scopeId}
      />

      {/* Detach confirm */}
      <AlertDialog
        open={!!detachTarget}
        onOpenChange={(v) => {
          if (!v) setDetachTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.rule_groups.bindings.detach_dialog_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.rule_groups.bindings.detach_dialog_description, {
                name: detachTarget?.rule_group_name ?? "",
                scope: scopeLabel,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.rule_groups.cancel)}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={handleDetach}>
              {t(($) => $.rule_groups.bindings.detach_tooltip)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function EffectivePreviewDialog({
  open,
  onOpenChange,
  wsId,
  scopeType,
  scopeId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  wsId: string;
  scopeType: BindableScope;
  scopeId: string;
}) {
  const { t } = useT("settings");
  const params =
    scopeType === "project"
      ? { projectId: scopeId }
      : scopeType === "squad"
        ? { squadId: scopeId }
        : { agentId: scopeId };

  const { data, isLoading } = useQuery(effectiveRulesOptions(wsId, params, open));
  const layers = data?.layers ?? [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(($) => $.rule_groups.bindings.preview_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.rule_groups.description)}</DialogDescription>
        </DialogHeader>
        {isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : layers.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            {t(($) => $.rule_groups.bindings.preview_empty)}
          </p>
        ) : (
          <div className="max-h-[60vh] space-y-4 overflow-y-auto">
            {layers.map((layer) => {
              // Server-driven scope_type: fall back to the raw value for an
              // unknown layer rather than rendering an undefined i18n key.
              const labelKey = LAYER_LABEL_KEY[layer.scope_type as RuleGroupScopeType];
              return (
              <div key={`${layer.scope_type}:${layer.scope_id ?? "ws"}`}>
                <div className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {labelKey ? t(($) => $.rule_groups.bindings[labelKey]) : layer.scope_type}
                </div>
                <ul className="space-y-1">
                  {layer.groups.map((g) => (
                    <li
                      key={g.binding_id}
                      className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-sm"
                    >
                      <span className="flex items-center gap-2">
                        <ShieldCheck className="h-4 w-4 text-muted-foreground" />
                        {g.name}
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {t(($) => $.rule_groups.rule_count, { count: g.rule_count })}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
              );
            })}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
