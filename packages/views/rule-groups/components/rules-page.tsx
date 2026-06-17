"use client";

import { useEffect, useMemo, useState } from "react";
import { useDefaultLayout } from "react-resizable-panels";
import { useQuery } from "@tanstack/react-query";
import {
  ShieldCheck,
  Plus,
  Search,
  Pencil,
  Trash2,
  MoreHorizontal,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Label } from "@multica/ui/components/ui/label";
import { Badge } from "@multica/ui/components/ui/badge";
import { Switch } from "@multica/ui/components/ui/switch";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@multica/ui/components/ui/resizable";
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
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentMember } from "@multica/core/permissions";
import {
  ruleGroupsListOptions,
  ruleGroupDetailOptions,
  useUpdateRuleGroup,
  useDeleteRuleGroup,
  useCreateRuleGroupRule,
  useUpdateRuleGroupRule,
  useDeleteRuleGroupRule,
  RULE_GROUP_SOURCE_BUILTIN,
  type RuleGroupSummary,
  type RuleGroupRule,
} from "@multica/core/rule-groups";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { RuleGroupFormDialog } from "./rule-group-form-dialog";

const isBuiltinGroup = (group: { source_type: string }) =>
  group.source_type === RULE_GROUP_SOURCE_BUILTIN;

/**
 * Workspace-level Rules page. Master-detail layout (groups on the left, the
 * rules of the selected group on the right) mirroring the Runtimes page. This
 * replaces the old Settings → Rule Groups tab and its overflow-prone rules
 * modal: the rules now render inside a resizable, scrollable panel that does
 * not spill past its container regardless of title / file-name length.
 */
export function RulesPage() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const { role } = useCurrentMember(wsId);
  const canManage = role === "owner" || role === "admin";
  const isMobile = useIsMobile();

  const { data: groups, isLoading } = useQuery(ruleGroupsListOptions(wsId));

  const [search, setSearch] = useState("");
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<RuleGroupSummary | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<RuleGroupSummary | null>(null);

  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_rules_layout",
  });

  const filteredGroups = useMemo(() => {
    const list = groups ?? [];
    const q = search.trim().toLowerCase();
    if (!q) return list;
    return list.filter(
      (g) =>
        g.name.toLowerCase().includes(q) ||
        g.description.toLowerCase().includes(q),
    );
  }, [groups, search]);

  // Keep a valid selection: default to the first visible group and re-pick when
  // the current selection drops out of the filtered list (search, deletion).
  useEffect(() => {
    if (filteredGroups.length === 0) {
      if (selectedGroupId !== null) setSelectedGroupId(null);
      return;
    }
    const stillValid =
      !!selectedGroupId && filteredGroups.some((g) => g.id === selectedGroupId);
    if (!stillValid) setSelectedGroupId(filteredGroups[0]!.id);
  }, [filteredGroups, selectedGroupId]);

  const updateGroup = useUpdateRuleGroup();
  const deleteGroup = useDeleteRuleGroup();

  const selectedGroup =
    (groups ?? []).find((g) => g.id === selectedGroupId) ?? null;

  const handleToggleGroup = async (group: RuleGroupSummary, enabled: boolean) => {
    try {
      await updateGroup.mutateAsync({ id: group.id, enabled });
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.toast_update_failed),
      );
    }
  };

  const handleDeleteGroup = async () => {
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

  const detail = (
    <GroupDetail
      wsId={wsId}
      group={selectedGroup}
      canManage={canManage}
      onToggle={handleToggleGroup}
      onEdit={(g) => setEditTarget(g)}
      onDelete={(g) => setDeleteTarget(g)}
    />
  );

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <ShieldCheck className="h-4 w-4 text-muted-foreground" />
          <h1 className="text-sm font-medium">{t(($) => $.rule_groups.page.title)}</h1>
          {(groups?.length ?? 0) > 0 && (
            <span className="font-mono text-xs tabular-nums text-muted-foreground/70">
              {groups!.length}
            </span>
          )}
        </div>
        {canManage && (
          <Button type="button" size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="h-3 w-3" />
            {t(($) => $.rule_groups.create)}
          </Button>
        )}
      </PageHeader>

      {isMobile ? (
        <div className="flex min-h-0 flex-1 flex-col border-t bg-background">
          <GroupSidebar
            groups={filteredGroups}
            totalGroups={groups?.length ?? 0}
            isLoading={isLoading}
            canManage={canManage}
            selectedGroupId={selectedGroup?.id ?? null}
            search={search}
            setSearch={setSearch}
            onSelect={setSelectedGroupId}
            onCreate={() => setCreateOpen(true)}
          />
          {detail}
        </div>
      ) : (
        <div className="min-h-0 flex-1 border-t bg-background">
          <ResizablePanelGroup
            orientation="horizontal"
            className="min-h-0 flex-1"
            defaultLayout={defaultLayout}
            onLayoutChanged={onLayoutChanged}
          >
            <ResizablePanel
              id="groups"
              defaultSize={320}
              minSize={260}
              maxSize={440}
              groupResizeBehavior="preserve-pixel-size"
            >
              <GroupSidebar
                groups={filteredGroups}
                totalGroups={groups?.length ?? 0}
                isLoading={isLoading}
                canManage={canManage}
                selectedGroupId={selectedGroup?.id ?? null}
                search={search}
                setSearch={setSearch}
                onSelect={setSelectedGroupId}
                onCreate={() => setCreateOpen(true)}
                className="h-full border-b-0 border-r"
              />
            </ResizablePanel>
            <ResizableHandle />
            <ResizablePanel id="detail" minSize="45%">
              {detail}
            </ResizablePanel>
          </ResizablePanelGroup>
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
            <AlertDialogAction variant="destructive" onClick={handleDeleteGroup}>
              {t(($) => $.rule_groups.delete)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Left panel — searchable list of rule groups.
// ---------------------------------------------------------------------------

function GroupSidebar({
  groups,
  totalGroups,
  isLoading,
  canManage,
  selectedGroupId,
  search,
  setSearch,
  onSelect,
  onCreate,
  className,
}: {
  groups: RuleGroupSummary[];
  totalGroups: number;
  isLoading: boolean;
  canManage: boolean;
  selectedGroupId: string | null;
  search: string;
  setSearch: (value: string) => void;
  onSelect: (id: string) => void;
  onCreate: () => void;
  className?: string;
}) {
  const { t } = useT("settings");

  return (
    <aside
      className={cn(
        "flex min-h-0 shrink-0 flex-col border-b bg-muted/20",
        className,
      )}
    >
      <div className="shrink-0 border-b bg-background p-3">
        <div className="mb-2 flex items-center justify-between gap-2">
          <span className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
            {t(($) => $.rule_groups.page.groups_label)}
          </span>
          {!canManage && (
            <span className="text-[11px] text-muted-foreground">
              {t(($) => $.rule_groups.manage_hint)}
            </span>
          )}
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder={t(($) => $.rule_groups.page.search_placeholder)}
            className="h-9 pl-8 text-sm"
          />
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto py-2">
        {isLoading ? (
          <div className="space-y-2 px-3">
            <Skeleton className="h-16 w-full rounded-md" />
            <Skeleton className="h-16 w-full rounded-md" />
            <Skeleton className="h-16 w-full rounded-md" />
          </div>
        ) : groups.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center px-6 text-center">
            <ShieldCheck className="h-8 w-8 text-muted-foreground/40" />
            <p className="mt-3 text-sm text-muted-foreground">
              {totalGroups === 0
                ? t(($) => $.rule_groups.empty)
                : t(($) => $.rule_groups.page.no_results)}
            </p>
            {totalGroups === 0 && canManage && (
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="mt-4"
                onClick={onCreate}
              >
                <Plus className="h-3.5 w-3.5" />
                {t(($) => $.rule_groups.create)}
              </Button>
            )}
          </div>
        ) : (
          <ul>
            {groups.map((group) => (
              <li key={group.id}>
                <GroupRow
                  group={group}
                  active={group.id === selectedGroupId}
                  onClick={() => onSelect(group.id)}
                />
              </li>
            ))}
          </ul>
        )}
      </div>
    </aside>
  );
}

function GroupRow({
  group,
  active,
  onClick,
}: {
  group: RuleGroupSummary;
  active: boolean;
  onClick: () => void;
}) {
  const { t } = useT("settings");
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex w-full min-w-0 flex-col gap-1 px-4 py-2.5 text-left transition-colors",
        active ? "bg-accent" : "hover:bg-accent/50",
      )}
    >
      <span className="flex min-w-0 items-center gap-2">
        <ShieldCheck className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate text-sm font-medium">{group.name}</span>
        {isBuiltinGroup(group) && (
          <Badge variant="secondary" className="shrink-0">
            {t(($) => $.rule_groups.builtin_badge)}
          </Badge>
        )}
        {!group.enabled && (
          <Badge variant="outline" className="shrink-0">
            {t(($) => $.rule_groups.disabled_badge)}
          </Badge>
        )}
      </span>
      <span className="flex items-center gap-3 pl-5 text-xs text-muted-foreground">
        <span>{t(($) => $.rule_groups.rule_count, { count: group.rule_count })}</span>
        <span>{t(($) => $.rule_groups.binding_count, { count: group.binding_count })}</span>
      </span>
    </button>
  );
}

// ---------------------------------------------------------------------------
// Right panel — header + rules of the selected group, with inline editor.
// ---------------------------------------------------------------------------

function GroupDetail({
  wsId,
  group,
  canManage,
  onToggle,
  onEdit,
  onDelete,
}: {
  wsId: string;
  group: RuleGroupSummary | null;
  canManage: boolean;
  onToggle: (group: RuleGroupSummary, enabled: boolean) => void;
  onEdit: (group: RuleGroupSummary) => void;
  onDelete: (group: RuleGroupSummary) => void;
}) {
  const { t } = useT("settings");
  const isBuiltin = group ? isBuiltinGroup(group) : false;
  const readOnly = !canManage || isBuiltin;

  const { data: detail, isLoading } = useQuery({
    ...ruleGroupDetailOptions(wsId, group?.id ?? ""),
    enabled: Boolean(group?.id),
  });

  const [editor, setEditor] = useState<null | "new" | RuleGroupRule>(null);
  const [deleteTarget, setDeleteTarget] = useState<RuleGroupRule | null>(null);

  // Reset transient editor state whenever the selected group changes.
  useEffect(() => {
    setEditor(null);
    setDeleteTarget(null);
  }, [group?.id]);

  const createRule = useCreateRuleGroupRule(group?.id ?? "");
  const updateRule = useUpdateRuleGroupRule(group?.id ?? "");
  const deleteRule = useDeleteRuleGroupRule(group?.id ?? "");

  if (!group) {
    return (
      <main className="flex min-h-0 flex-1 flex-col items-center justify-center px-6 text-center">
        <ShieldCheck className="h-8 w-8 text-muted-foreground/40" />
        <p className="mt-3 text-sm text-muted-foreground">
          {t(($) => $.rule_groups.page.select_group)}
        </p>
      </main>
    );
  }

  const rules = detail?.rules ?? [];

  const handleToggleRule = async (rule: RuleGroupRule, enabled: boolean) => {
    try {
      await updateRule.mutateAsync({ id: rule.id, enabled });
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.rule_groups.rules.toast_save_failed),
      );
    }
  };

  const handleDeleteRule = async () => {
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
    <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
      {/* Group header */}
      <div className="shrink-0 border-b bg-background px-5 py-4">
        <div className="flex min-w-0 items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <h2 className="truncate text-base font-semibold tracking-tight">
                {group.name}
              </h2>
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
              <p className="mt-1 text-xs text-muted-foreground break-words">
                {group.description}
              </p>
            )}
            <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <span>{t(($) => $.rule_groups.rule_count, { count: group.rule_count })}</span>
              <span>{t(($) => $.rule_groups.binding_count, { count: group.binding_count })}</span>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-1.5">
            {canManage && (
              <Switch
                checked={group.enabled}
                onCheckedChange={(v) => onToggle(group, v)}
                aria-label={group.name}
              />
            )}
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
                  <DropdownMenuItem onClick={() => onEdit(group)}>
                    <Pencil className="mr-2 h-4 w-4" />
                    {t(($) => $.rule_groups.edit_dialog_title)}
                  </DropdownMenuItem>
                  <DropdownMenuItem variant="destructive" onClick={() => onDelete(group)}>
                    <Trash2 className="mr-2 h-4 w-4" />
                    {t(($) => $.rule_groups.delete)}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </div>
        </div>
        {isBuiltin && (
          <p className="mt-3 text-xs text-muted-foreground">
            {t(($) => $.rule_groups.builtin_readonly_hint)}
          </p>
        )}
      </div>

      {/* Rules list / inline editor */}
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {editor ? (
          <RuleEditor
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
          <div className="space-y-2">
            {isLoading ? (
              <>
                <Skeleton className="h-16 w-full" />
                <Skeleton className="h-16 w-full" />
              </>
            ) : rules.length === 0 ? (
              <p className="py-10 text-center text-sm text-muted-foreground">
                {t(($) => $.rule_groups.rules.empty)}
              </p>
            ) : (
              rules.map((rule) => (
                <div
                  key={rule.id}
                  className="flex min-w-0 items-start gap-3 rounded-md border bg-card p-3"
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="truncate text-sm font-medium">{rule.name}</span>
                      {rule.file_name && (
                        <code className="inline-block max-w-full truncate rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                          {rule.file_name}
                        </code>
                      )}
                    </div>
                    <p className="mt-1 line-clamp-2 whitespace-pre-wrap break-words text-xs text-muted-foreground">
                      {rule.content}
                    </p>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    <Switch
                      checked={rule.enabled}
                      onCheckedChange={(v) => handleToggleRule(rule, v)}
                      aria-label={rule.name}
                      disabled={readOnly}
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

            {!readOnly && (
              <Button variant="outline" size="sm" onClick={() => setEditor("new")}>
                <Plus className="mr-1.5 h-3.5 w-3.5" />
                {t(($) => $.rule_groups.rules.add)}
              </Button>
            )}
          </div>
        )}
      </div>

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
            <AlertDialogAction variant="destructive" onClick={handleDeleteRule}>
              {t(($) => $.rule_groups.delete)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </main>
  );
}

function RuleEditor({
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
          rows={10}
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

export default RulesPage;
