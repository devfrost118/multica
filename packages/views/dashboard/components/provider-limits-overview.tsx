"use client";

import { useMemo, useState } from "react";
import { AlertCircle, Clock3, Server, SlidersHorizontal } from "lucide-react";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useProviderLimitSettingsStore } from "@multica/core/provider-limits";
import type {
  ProviderLimitHistoryResponse,
  ProviderLimitSnapshot,
  ProviderLimitsOverviewResponse,
} from "@multica/core/types";
import { useT } from "../../i18n";
import { ProviderLimitDetail } from "./provider-limit-detail";

type ViewMode = "accounts" | "daemons";

const EMPTY_OVERVIEW: ProviderLimitsOverviewResponse = { accounts: [], daemons: [] };
const EMPTY_HISTORY: ProviderLimitHistoryResponse["snapshots"] = [];

function timestamp(value: string): number {
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

function deduplicateAccounts(records: ProviderLimitSnapshot[]): ProviderLimitSnapshot[] {
  const latest = new Map<string, ProviderLimitSnapshot>();
  for (const record of records) {
    const key = `${record.provider}:${record.account_key}`;
    const previous = latest.get(key);
    if (!previous || timestamp(record.checked_at) > timestamp(previous.checked_at)) {
      latest.set(key, record);
    }
  }
  return [...latest.values()];
}

function effectiveStatus(record: ProviderLimitSnapshot): string {
  return record.stale ? "stale" : record.status;
}

export function titleCase(value: string): string {
  return value
    .split("_")
    .filter(Boolean)
    .map((part) => `${part.slice(0, 1).toUpperCase()}${part.slice(1)}`)
    .join(" ");
}

// account_label is stored internally as "profile-<slug>" (e.g. "profile-max")
// so it can pass the daemon-side sanitizer; render it as a clean subscription
// name ("Max") instead of the raw slug.
export function subscriptionLabel(accountLabel: string): string {
  const slug = accountLabel.startsWith("profile-") ? accountLabel.slice("profile-".length) : accountLabel;
  return titleCase(slug.replaceAll("-", "_"));
}

function remainingPercent(bucket: ProviderLimitSnapshot["buckets"][number]): number | null {
  if (bucket.remaining_value !== null) return Math.max(0, Math.min(100, bucket.remaining_value));
  if (bucket.limit_value !== null && bucket.used_value !== null && bucket.limit_value > 0) {
    return Math.max(0, Math.min(100, ((bucket.limit_value - bucket.used_value) / bucket.limit_value) * 100));
  }
  return null;
}

function sourceLabel(kind: string): string {
  const labels: Record<string, string> = {
    official_api: "Official API",
    local_auth_state: "Local auth state",
    local_log: "Local log",
    cli: "CLI",
  };
  return labels[kind] ?? titleCase(kind);
}

function formatFreshness(seconds: number): string {
  if (seconds <= 0) return "—";
  if (seconds % 3_600 === 0) return `${seconds / 3_600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

function lastGoodSnapshot(
  history: ProviderLimitSnapshot[],
  record: ProviderLimitSnapshot,
): ProviderLimitSnapshot | undefined {
  return history
    .filter(
      (candidate) =>
        candidate.provider === record.provider &&
        candidate.account_key === record.account_key &&
        candidate.status === "ok" &&
        !candidate.stale,
    )
    .toSorted((left, right) => timestamp(right.checked_at) - timestamp(left.checked_at))[0];
}

export function ProviderLimitsOverview({
  overview = EMPTY_OVERVIEW,
  history = EMPTY_HISTORY,
  isLoading,
  isError,
  onRefresh,
}: {
  overview?: ProviderLimitsOverviewResponse;
  history?: ProviderLimitSnapshot[];
  isLoading: boolean;
  isError: boolean;
  onRefresh?: (runtimeId: string) => Promise<void>;
}) {
  const { t, i18n } = useT("usage");
  const [view, setView] = useState<ViewMode>("accounts");
  const warningThreshold = useProviderLimitSettingsStore((state) => state.warningThreshold);
  const criticalThreshold = useProviderLimitSettingsStore((state) => state.criticalThreshold);
  const setWarningThreshold = useProviderLimitSettingsStore((state) => state.setWarningThreshold);
  const setCriticalThreshold = useProviderLimitSettingsStore((state) => state.setCriticalThreshold);
  const records = useMemo(() => {
    return view === "accounts" ? deduplicateAccounts(overview.accounts) : overview.daemons;
  }, [overview.accounts, overview.daemons, view]);
  const hasReportedRecords = view === "accounts"
    ? overview.accounts.length > 0
    : overview.daemons.length > 0;
  const locale = i18n.resolvedLanguage ?? i18n.language;
  const refreshRuntimeID = records.find((record) => record.runtime_id)?.runtime_id;
  const [isRefreshing, setIsRefreshing] = useState(false);

  const handleRefresh = async () => {
    if (!refreshRuntimeID || !onRefresh) return;
    setIsRefreshing(true);
    try {
      await onRefresh(refreshRuntimeID);
    } finally {
      setIsRefreshing(false);
    }
  };

  return (
    <section className="rounded-lg border bg-card" aria-labelledby="provider-limits-title">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
        <div className="flex items-center gap-2">
          <Server className="size-4 text-muted-foreground" />
          <div>
            <h2 id="provider-limits-title" className="text-sm font-semibold">
              {t(($) => $.provider_limits.title)}
            </h2>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.provider_limits.subtitle)}
            </p>
          </div>
        </div>
        <div className="inline-flex rounded-md bg-muted p-0.5" aria-label={t(($) => $.provider_limits.view_label)}>
          <button
            type="button"
            onClick={() => setView("accounts")}
            className={`rounded-sm px-2.5 py-1 text-xs font-medium ${view === "accounts" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}
          >
            {t(($) => $.provider_limits.by_accounts)}
          </button>
          <button
            type="button"
            onClick={() => setView("daemons")}
            className={`rounded-sm px-2.5 py-1 text-xs font-medium ${view === "daemons" ? "bg-background text-foreground shadow-sm" : "text-muted-foreground"}`}
          >
            {t(($) => $.provider_limits.by_daemon)}
          </button>
        </div>
        <button type="button" className="rounded-md border px-2.5 py-1 text-xs font-medium disabled:opacity-50" disabled={!refreshRuntimeID || isRefreshing} onClick={() => void handleRefresh()}>
          {isRefreshing ? "Refreshing…" : "Refresh"}
        </button>
      </div>

      <div className="space-y-4 p-4">
        <ThresholdSettings
          warningThreshold={warningThreshold}
          criticalThreshold={criticalThreshold}
          onWarningChange={setWarningThreshold}
          onCriticalChange={setCriticalThreshold}
        />
        {isLoading ? (
          <div className="space-y-2" aria-live="polite">
            <p className="text-xs text-muted-foreground">{t(($) => $.provider_limits.loading)}</p>
            <Skeleton className="h-28 w-full" />
          </div>
        ) : isError ? (
          <div className="flex items-center gap-2 rounded-md border border-dashed p-4 text-sm text-muted-foreground" role="alert">
            <AlertCircle className="size-4 shrink-0" />
            {t(($) => $.provider_limits.error)}
          </div>
        ) : (
          <>
            {!hasReportedRecords && (
              <p className="text-xs text-muted-foreground">{t(($) => $.provider_limits.empty)}</p>
            )}
            <div className="grid gap-3 lg:grid-cols-2">
              {records.map((record) => (
                <ProviderLimitCard
                  key={`${record.daemon_id}:${record.runtime_id}:${record.provider}:${record.account_key}`}
                  record={record}
                  history={history}
                  warningThreshold={warningThreshold}
                  criticalThreshold={criticalThreshold}
                  locale={locale}
                />
              ))}
            </div>
          </>
        )}
      </div>
    </section>
  );
}

function ThresholdSettings({
  warningThreshold,
  criticalThreshold,
  onWarningChange,
  onCriticalChange,
}: {
  warningThreshold: number;
  criticalThreshold: number;
  onWarningChange: (value: number) => void;
  onCriticalChange: (value: number) => void;
}) {
  const { t } = useT("usage");
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-md bg-muted/50 px-3 py-2 text-xs">
      <span className="flex items-center gap-1 font-medium"><SlidersHorizontal className="size-3" />{t(($) => $.provider_limits.thresholds.title)}</span>
      <label className="flex items-center gap-1 text-muted-foreground">
        {t(($) => $.provider_limits.thresholds.warning)}
        <input aria-label={t(($) => $.provider_limits.thresholds.warning)} className="w-12 rounded border bg-background px-1 py-0.5 text-foreground" type="number" min="0" max="40" value={warningThreshold} onChange={(event) => onWarningChange(Number(event.target.value))} />%
      </label>
      <label className="flex items-center gap-1 text-muted-foreground">
        {t(($) => $.provider_limits.thresholds.critical)}
        <input aria-label={t(($) => $.provider_limits.thresholds.critical)} className="w-12 rounded border bg-background px-1 py-0.5 text-foreground" type="number" min="0" max="20" value={criticalThreshold} onChange={(event) => onCriticalChange(Number(event.target.value))} />%
      </label>
    </div>
  );
}

function ProviderLimitCard({
  record,
  history,
  warningThreshold,
  criticalThreshold,
  locale,
}: {
  record: ProviderLimitSnapshot;
  history: ProviderLimitSnapshot[];
  warningThreshold: number;
  criticalThreshold: number;
  locale: string;
}) {
  const { t } = useT("usage");
  const status = effectiveStatus(record);
  const lastGood = lastGoodSnapshot(history, record);
  const checkedAt = record.checked_at ? new Date(record.checked_at).toLocaleString(locale) : t(($) => $.provider_limits.unknown);
  return (
    <article className="rounded-md border p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h3 className="text-sm font-medium">{titleCase(record.provider)}</h3>
          {record.account_label && (
            <p className="text-xs text-muted-foreground">{subscriptionLabel(record.account_label)}</p>
          )}
        </div>
        <div className="flex items-center gap-2">
          <StatusBadge status={status} />
          <ProviderLimitDetail record={record} history={history} />
        </div>
      </div>
      <div className="mt-3 space-y-2">
        {record.buckets.map((bucket) => (
          <BucketRow key={bucket.id} bucket={bucket} warningThreshold={warningThreshold} criticalThreshold={criticalThreshold} />
        ))}
        {record.buckets.length === 0 && (
          <p className="text-xs text-muted-foreground">{t(($) => $.provider_limits.no_buckets)}</p>
        )}
      </div>
      <div className="mt-3 space-y-1 border-t pt-2 text-xs text-muted-foreground">
        <p>{sourceLabel(record.source.kind)} · {record.source.confidence || t(($) => $.provider_limits.unknown)}</p>
        <p>{t(($) => $.provider_limits.freshness, { value: formatFreshness(record.source.freshness_seconds) })}</p>
        <p className="flex items-center gap-1"><Clock3 className="size-3" />{t(($) => $.provider_limits.checked_at, { value: checkedAt })}</p>
        {lastGood && lastGood.checked_at !== record.checked_at && (
          <p>{t(($) => $.provider_limits.last_good, { value: new Date(lastGood.checked_at).toLocaleString(locale) })}</p>
        )}
        {record.error_note && <p>{t(($) => $.provider_limits.reason, { value: titleCase(record.error_note) })}</p>}
      </div>
    </article>
  );
}

function BucketRow({
  bucket,
  warningThreshold,
  criticalThreshold,
}: {
  bucket: ProviderLimitSnapshot["buckets"][number];
  warningThreshold: number;
  criticalThreshold: number;
}) {
  const { t } = useT("usage");
  const remaining = remainingPercent(bucket);
  const used = remaining === null ? null : 100 - remaining;
  const severity = remaining === null ? "unknown" : remaining <= criticalThreshold ? "critical" : remaining <= warningThreshold ? "warning" : "normal";
  return (
    <div className="rounded bg-muted/40 p-2">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="truncate font-medium">{bucket.label}</span>
        <span className={severity === "critical" ? "text-destructive" : "text-muted-foreground"}>
          {used === null ? t(($) => $.provider_limits.unknown) : t(($) => $.provider_limits.used, { value: Math.round(used) })}
        </span>
      </div>
      {used !== null && <div className="mt-1 h-1.5 overflow-hidden rounded bg-background"><div className={severity === "critical" ? "h-full bg-destructive" : "h-full bg-primary"} style={{ width: `${used}%` }} /></div>}
      {bucket.resets_at && <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.provider_limits.resets_at, { value: new Date(bucket.resets_at).toLocaleString() })}</p>}
      {bucket.note && <p className="mt-1 text-xs text-muted-foreground">{titleCase(bucket.note)}</p>}
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const { t } = useT("usage");
  const labels: Record<string, string> = {
    ok: t(($) => $.provider_limits.status.ok),
    stale: t(($) => $.provider_limits.status.stale),
    partial: t(($) => $.provider_limits.status.partial),
    unavailable: t(($) => $.provider_limits.status.unavailable),
    error: t(($) => $.provider_limits.status.error),
  };
  return <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium">{labels[status] ?? titleCase(status)}</span>;
}
