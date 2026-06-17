"use client";

import { useEffect, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Button } from "@multica/ui/components/ui/button";
import { Label } from "@multica/ui/components/ui/label";
import { toast } from "sonner";
import {
  useCreateRuleGroup,
  useUpdateRuleGroup,
  type RuleGroupSummary,
} from "@multica/core/rule-groups";
import { useT } from "../../i18n";

/**
 * Create / edit a rule group's metadata (name, description). When `group` is
 * provided the dialog edits it; otherwise it creates a new group. Rule content
 * is managed separately in the rules detail panel.
 */
export function RuleGroupFormDialog({
  open,
  onOpenChange,
  group,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  group?: RuleGroupSummary;
}) {
  const { t } = useT("settings");
  const isEdit = Boolean(group);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  const createGroup = useCreateRuleGroup();
  const updateGroup = useUpdateRuleGroup();
  const saving = createGroup.isPending || updateGroup.isPending;

  // Seed the form whenever the dialog opens (or the target group changes).
  useEffect(() => {
    if (open) {
      setName(group?.name ?? "");
      setDescription(group?.description ?? "");
    }
  }, [open, group]);

  const handleSave = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    try {
      if (group) {
        await updateGroup.mutateAsync({ id: group.id, name: trimmed, description });
        toast.success(t(($) => $.rule_groups.toast_updated));
      } else {
        await createGroup.mutateAsync({ name: trimmed, description });
        toast.success(t(($) => $.rule_groups.toast_created));
      }
      onOpenChange(false);
    } catch (e) {
      toast.error(
        e instanceof Error
          ? e.message
          : t(($) =>
              isEdit
                ? $.rule_groups.toast_update_failed
                : $.rule_groups.toast_create_failed,
            ),
      );
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {isEdit
              ? t(($) => $.rule_groups.edit_dialog_title)
              : t(($) => $.rule_groups.create_dialog_title)}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="rule-group-name">{t(($) => $.rule_groups.name_label)}</Label>
            <Input
              id="rule-group-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t(($) => $.rule_groups.name_placeholder)}
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="rule-group-description">
              {t(($) => $.rule_groups.description_label)}
            </Label>
            <Textarea
              id="rule-group-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t(($) => $.rule_groups.description_placeholder)}
              rows={3}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            {t(($) => $.rule_groups.cancel)}
          </Button>
          <Button onClick={handleSave} disabled={saving || !name.trim()}>
            {saving ? t(($) => $.rule_groups.saving) : t(($) => $.rule_groups.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
