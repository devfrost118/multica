"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus, Pencil, Trash2 } from "lucide-react";
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
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Button } from "@multica/ui/components/ui/button";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { toast } from "sonner";
import {
  ruleGroupDetailOptions,
  useCreateRuleGroupRule,
  useUpdateRuleGroupRule,
  useDeleteRuleGroupRule,
  type RuleGroupRule,
} from "@multica/core/rule-groups";
import { useT } from "../../i18n";

type EditorState = null | "new" | RuleGroupRule;

export function RuleGroupRulesDialog({
  open,
  onOpenChange,
  wsId,
  groupId,
  groupName,
  readOnly,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  wsId: string;
  groupId: string;
  groupName: string;
  readOnly: boolean;
}) {
  const { t } = useT("settings");
  const { data: group, isLoading } = useQuery({
    ...ruleGroupDetailOptions(wsId, groupId),
    enabled: open && Boolean(groupId),
  });

  const [editor, setEditor] = useState<EditorState>(null);
  const [deleteTarget, setDeleteTarget] = useState<RuleGroupRule | null>(null);

  const createRule = useCreateRuleGroupRule(groupId);
  const updateRule = useUpdateRuleGroupRule(groupId);
  const deleteRule = useDeleteRuleGroupRule(groupId);

  // Reset transient editor state when the dialog closes.
  useEffect(() => {
    if (!open) {
      setEditor(null);
      setDeleteTarget(null);
    }
  }, [open]);

  const rules = group?.rules ?? [];

  const handleToggle = async (rule: RuleGroupRule, enabled: boolean) => {
    try {
      await updateRule.mutateAsync({ id: rule.id, enabled });
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.rules.toast_save_failed),
      );
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteRule.mutateAsync(deleteTarget.id);
      toast.success(t(($) => $.rule_groups.rules.toast_deleted));
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.rules.toast_delete_failed),
      );
    } finally {
      setDeleteTarget(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{groupName}</DialogTitle>
          <DialogDescription>{t(($) => $.rule_groups.rules.title)}</DialogDescription>
        </DialogHeader>

        {editor ? (
          <RuleForm
            initial={editor === "new" ? null : editor}
            saving={createRule.isPending || updateRule.isPending}
            onCancel={() => setEditor(null)}
            onSave={async (data) => {
              try {
                if (editor === "new") {
                  await createRule.mutateAsync({
                    name: data.name,
                    description: "",
                    content: data.content,
                    sort_order: rules.length,
                    file_name: data.file_name || undefined,
                  });
                } else {
                  await updateRule.mutateAsync({
                    id: editor.id,
                    name: data.name,
                    content: data.content,
                    file_name: data.file_name,
                  });
                }
                toast.success(t(($) => $.rule_groups.rules.toast_saved));
                setEditor(null);
              } catch (e) {
                toast.error(
                  e instanceof Error
                    ? e.message
                    : t(($) => $.rule_groups.rules.toast_save_failed),
                );
              }
            }}
          />
        ) : (
          <div className="space-y-3">
            <div className="max-h-[50vh] space-y-2 overflow-y-auto">
              {isLoading ? (
                <>
                  <Skeleton className="h-14 w-full" />
                  <Skeleton className="h-14 w-full" />
                </>
              ) : rules.length === 0 ? (
                <p className="py-6 text-center text-sm text-muted-foreground">
                  {t(($) => $.rule_groups.rules.empty)}
                </p>
              ) : (
                rules.map((rule) => (
                  <div
                    key={rule.id}
                    className="flex items-start gap-3 rounded-md border bg-card p-3"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate text-sm font-medium">{rule.name}</span>
                        {rule.file_name && (
                          <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                            {rule.file_name}
                          </code>
                        )}
                      </div>
                      <p className="mt-1 line-clamp-2 text-xs text-muted-foreground whitespace-pre-wrap">
                        {rule.content}
                      </p>
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <Switch
                        checked={rule.enabled}
                        onCheckedChange={(v) => handleToggle(rule, v)}
                        aria-label={rule.name}
                      />
                      {!readOnly && (
                        <>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            onClick={() => setEditor(rule)}
                          >
                            <Pencil className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            onClick={() => setDeleteTarget(rule)}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </>
                      )}
                    </div>
                  </div>
                ))
              )}
            </div>

            {!readOnly && (
              <Button variant="outline" size="sm" onClick={() => setEditor("new")}>
                <Plus className="mr-1.5 h-3.5 w-3.5" />
                {t(($) => $.rule_groups.rules.add)}
              </Button>
            )}
          </div>
        )}
      </DialogContent>

      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(v) => {
          if (!v) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.rule_groups.rules.delete_dialog_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.rule_groups.rules.delete_dialog_description, {
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
    </Dialog>
  );
}

function RuleForm({
  initial,
  saving,
  onSave,
  onCancel,
}: {
  initial: RuleGroupRule | null;
  saving: boolean;
  onSave: (data: { name: string; content: string; file_name: string }) => void;
  onCancel: () => void;
}) {
  const { t } = useT("settings");
  const [name, setName] = useState(initial?.name ?? "");
  const [content, setContent] = useState(initial?.content ?? "");
  const [fileName, setFileName] = useState(initial?.file_name ?? "");

  const canSave = name.trim().length > 0 && content.trim().length > 0;

  return (
    <div className="space-y-4">
      <h3 className="text-sm font-semibold">
        {initial
          ? t(($) => $.rule_groups.rules.edit_dialog_title)
          : t(($) => $.rule_groups.rules.create_dialog_title)}
      </h3>
      <div className="space-y-1.5">
        <Label htmlFor="rule-name">{t(($) => $.rule_groups.rules.name_label)}</Label>
        <Input
          id="rule-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t(($) => $.rule_groups.rules.name_placeholder)}
          autoFocus
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="rule-content">{t(($) => $.rule_groups.rules.content_label)}</Label>
        <Textarea
          id="rule-content"
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder={t(($) => $.rule_groups.rules.content_placeholder)}
          rows={8}
          className="font-mono text-xs"
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="rule-file-name">
          {t(($) => $.rule_groups.rules.file_name_label)}
        </Label>
        <Input
          id="rule-file-name"
          value={fileName}
          onChange={(e) => setFileName(e.target.value)}
          placeholder={t(($) => $.rule_groups.rules.file_name_placeholder)}
        />
      </div>
      <div className="flex justify-end gap-2">
        <Button variant="outline" onClick={onCancel} disabled={saving}>
          {t(($) => $.rule_groups.cancel)}
        </Button>
        <Button
          onClick={() => onSave({ name: name.trim(), content, file_name: fileName.trim() })}
          disabled={saving || !canSave}
        >
          {saving ? t(($) => $.rule_groups.saving) : t(($) => $.rule_groups.save)}
        </Button>
      </div>
    </div>
  );
}
