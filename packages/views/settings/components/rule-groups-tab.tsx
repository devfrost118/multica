"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ShieldCheck, Plus, MoreHorizontal, Pencil, Trash2, FileText } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Badge } from "@multica/ui/components/ui/badge";
import { Switch } from "@multica/ui/components/ui/switch";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
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
  ruleGroupsListOptions,
  useUpdateRuleGroup,
  useDeleteRuleGroup,
  RULE_GROUP_SOURCE_BUILTIN,
  type RuleGroupSummary,
} from "@multica/core/rule-groups";
import { useT } from "../../i18n";
import { RuleGroupFormDialog } from "./rule-group-form-dialog";
import { RuleGroupRulesDialog } from "./rule-group-rules-dialog";

export function RuleGroupsTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const { role } = useCurrentMember(wsId);
  const canManage = role === "owner" || role === "admin";

  const { data: groups, isLoading } = useQuery(ruleGroupsListOptions(wsId));
  const updateGroup = useUpdateRuleGroup();
  const deleteGroup = useDeleteRuleGroup();

  const [createOpen, setCreateOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<RuleGroupSummary | null>(null);
  const [rulesTarget, setRulesTarget] = useState<RuleGroupSummary | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<RuleGroupSummary | null>(null);

  const handleToggle = async (group: RuleGroupSummary, enabled: boolean) => {
    try {
      await updateGroup.mutateAsync({ id: group.id, enabled });
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.toast_update_failed),
      );
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteGroup.mutateAsync(deleteTarget.id);
      toast.success(t(($) => $.rule_groups.toast_deleted));
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.toast_delete_failed),
      );
    } finally {
      setDeleteTarget(null);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <ShieldCheck className="h-4 w-4 text-muted-foreground" />
            {t(($) => $.rule_groups.title)}
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">
            {t(($) => $.rule_groups.description)}
          </p>
        </div>
        {canManage && (
          <Button size="sm" className="shrink-0" onClick={() => setCreateOpen(true)}>
            <Plus className="mr-1.5 h-4 w-4" />
            {t(($) => $.rule_groups.create)}
          </Button>
        )}
      </div>

      {!canManage && (
        <p className="text-xs text-muted-foreground">{t(($) => $.rule_groups.manage_hint)}</p>
      )}

      {isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : !groups || groups.length === 0 ? (
        <div className="flex h-32 items-center justify-center rounded-lg border border-dashed">
          <p className="text-sm text-muted-foreground">{t(($) => $.rule_groups.empty)}</p>
        </div>
      ) : (
        <div className="space-y-2">
          {groups.map((group) => {
            const isBuiltin = group.source_type === RULE_GROUP_SOURCE_BUILTIN;
            return (
              <Card key={group.id}>
                <CardContent className="flex items-start gap-3 p-4">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <h3 className="truncate text-sm font-medium">{group.name}</h3>
                      {isBuiltin && (
                        <Badge variant="secondary" className="shrink-0">
                          {t(($) => $.rule_groups.builtin_badge)}
                        </Badge>
                      )}
                      {!group.enabled && (
                        <Badge variant="outline" className="shrink-0">
                          {t(($) => $.rule_groups.disabled_badge)}
                        </Badge>
                      )}
                    </div>
                    {group.description && (
                      <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">
                        {group.description}
                      </p>
                    )}
                    <div className="mt-2 flex gap-4 text-xs text-muted-foreground">
                      <span>{t(($) => $.rule_groups.rule_count, { count: group.rule_count })}</span>
                      <span>
                        {t(($) => $.rule_groups.binding_count, { count: group.binding_count })}
                      </span>
                    </div>
                  </div>

                  <div className="flex shrink-0 items-center gap-1.5">
                    {canManage && (
                      <Switch
                        checked={group.enabled}
                        onCheckedChange={(v) => handleToggle(group, v)}
                        aria-label={group.name}
                      />
                    )}
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      onClick={() => setRulesTarget(group)}
                      title={t(($) => $.rule_groups.manage_rules)}
                    >
                      <FileText className="h-4 w-4 text-muted-foreground" />
                    </Button>
                    {canManage && !isBuiltin && (
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button variant="ghost" size="icon-sm">
                              <MoreHorizontal className="h-4 w-4 text-muted-foreground" />
                            </Button>
                          }
                        />
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => setEditTarget(group)}>
                            <Pencil className="mr-2 h-4 w-4" />
                            {t(($) => $.rule_groups.edit_dialog_title)}
                          </DropdownMenuItem>
                          <DropdownMenuItem variant="destructive" onClick={() => setDeleteTarget(group)}>
                            <Trash2 className="mr-2 h-4 w-4" />
                            {t(($) => $.rule_groups.delete)}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <RuleGroupFormDialog open={createOpen} onOpenChange={setCreateOpen} />
      <RuleGroupFormDialog
        open={!!editTarget}
        onOpenChange={(v) => {
          if (!v) setEditTarget(null);
        }}
        group={editTarget ?? undefined}
      />
      {rulesTarget && (
        <RuleGroupRulesDialog
          open={!!rulesTarget}
          onOpenChange={(v) => {
            if (!v) setRulesTarget(null);
          }}
          wsId={wsId}
          groupId={rulesTarget.id}
          groupName={rulesTarget.name}
          readOnly={!canManage || rulesTarget.source_type === RULE_GROUP_SOURCE_BUILTIN}
        />
      )}

      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(v) => {
          if (!v) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.rule_groups.delete_dialog_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.rule_groups.delete_dialog_description, {
                name: deleteTarget?.name ?? "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.rule_groups.cancel)}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={handleDelete}>
              {t(($) => $.rule_groups.delete)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
