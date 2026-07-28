"use client";

import { useEffect, useMemo, useState } from "react";
import { AlertCircle, Loader2, Server, SlidersHorizontal } from "lucide-react";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useProviderLimitSettingsStore, withCanonicalBuckets } from "@multica/core/provider-limits";
import type {
  ProviderLimitHistoryResponse,
  ProviderLimitSnapshot,
  ProviderLimitsOverviewResponse,
} from "@multica/core/types";
import { useT } from "../../i18n";
import { ProviderLimitDetail } from "./provider-limit-detail";

type ViewMode = "accounts" | "daemons";
type AccountCollapseScope = "workspace" | "daemon";

const EMPTY_OVERVIEW: ProviderLimitsOverviewResponse = { accounts: [], daemons: [] };
const EMPTY_HISTORY: ProviderLimitHistoryResponse["snapshots"] = [];

function timestamp(value: string): number {
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

function scopeKey(record: ProviderLimitSnapshot, scope: AccountCollapseScope): string {
  return scope === "daemon"
    ? `${record.daemon_id}:${record.provider}`
    : record.provider;
}

function attemptTimestamp(record: ProviderLimitSnapshot): number {
  return timestamp(record.last_attempted_at || record.checked_at);
}

function hasUsefulQuota(record: ProviderLimitSnapshot): boolean {
  return (record.status === "ok" || record.status === "partial") && record.buckets.length > 0;
}

function buildKnownAccounts(
  candidates: ProviderLimitSnapshot[],
  scope: AccountCollapseScope,
): Map<string, Set<string>> {
  const knownAccounts = new Map<string, Set<string>>();
  for (const candidate of candidates) {
    if (candidate.account_key === "unavailable") continue;
    const key = scopeKey(candidate, scope);
    const accounts = knownAccounts.get(key) ?? new Set<string>();
    knownAccounts.set(key, new Set([...accounts, candidate.account_key]));
  }
  return knownAccounts;
}

function createCanonicalAccountKey(
  knownAccounts: Map<string, Set<string>>,
  scope: AccountCollapseScope,
): (record: ProviderLimitSnapshot) => string {
  return (record) => {
    if (record.account_key !== "unavailable") return record.account_key;
    const accounts = [...(knownAccounts.get(scopeKey(record, scope)) ?? [])];
    return accounts.length === 1 ? (accounts[0] ?? record.account_key) : record.account_key;
  };
}

function groupProviderLimitRecords(
  records: ProviderLimitSnapshot[],
  scope: AccountCollapseScope,
  canonicalAccountKey: (record: ProviderLimitSnapshot) => string,
): Map<string, ProviderLimitSnapshot[]> {
  const recordGroups = new Map<string, ProviderLimitSnapshot[]>();
  for (const record of records) {
    const key = `${scopeKey(record, scope)}:${canonicalAccountKey(record)}`;
    recordGroups.set(key, [...(recordGroups.get(key) ?? []), record]);
  }
  return recordGroups;
}

function reconcileProviderLimitGroup(
  groupKey: string,
  attempts: ProviderLimitSnapshot[],
  candidates: ProviderLimitSnapshot[],
  scope: AccountCollapseScope,
  canonicalAccountKey: (record: ProviderLimitSnapshot) => string,
): ProviderLimitSnapshot {
  const latestAttempt = attempts.toSorted(
    (left, right) => attemptTimestamp(right) - attemptTimestamp(left),
  )[0]!;
  const matchingCandidates = candidates.filter(
    (candidate) =>
      `${scopeKey(candidate, scope)}:${canonicalAccountKey(candidate)}` === groupKey,
  );
  const lastGood = matchingCandidates
    .filter(hasUsefulQuota)
    .toSorted((left, right) => timestamp(right.checked_at) - timestamp(left.checked_at))[0];
  const display = lastGood ?? latestAttempt;
  const attemptStatus = latestAttempt.last_attempt_status || latestAttempt.status;
  const attemptFailed = attemptStatus === "unavailable" || attemptStatus === "error";

  return {
    ...display,
    runtime_id: latestAttempt.runtime_id || display.runtime_id,
    daemon_id: latestAttempt.daemon_id || display.daemon_id,
    account_key: canonicalAccountKey(latestAttempt),
    account_label: display.account_label || latestAttempt.account_label,
    error_note: latestAttempt.error_note,
    stale: display.stale || (attemptFailed && display !== latestAttempt),
    last_successful_at:
      latestAttempt.last_successful_at ??
      display.last_successful_at ??
      (hasUsefulQuota(display) ? display.checked_at : null),
    last_attempted_at: latestAttempt.last_attempted_at || latestAttempt.checked_at,
    last_attempt_status: attemptStatus,
    last_attempt_source: latestAttempt.last_attempt_source ?? latestAttempt.source,
  };
}

function reconcileProviderLimitAccounts(
  records: ProviderLimitSnapshot[],
  history: ProviderLimitSnapshot[],
  scope: AccountCollapseScope,
): ProviderLimitSnapshot[] {
  const candidates = [...records, ...history];
  const canonicalAccountKey = createCanonicalAccountKey(
    buildKnownAccounts(candidates, scope),
    scope,
  );
  const recordGroups = groupProviderLimitRecords(records, scope, canonicalAccountKey);
  const reconciled = [...recordGroups.entries()].map(([groupKey, attempts]) =>
    reconcileProviderLimitGroup(groupKey, attempts, candidates, scope, canonicalAccountKey),
  );

  return reconciled.toSorted((left, right) => {
    const leftKey = `${left.daemon_id}:${left.provider}:${left.account_key}`;
    const rightKey = `${right.daemon_id}:${right.provider}:${right.account_key}`;
    return leftKey.localeCompare(rightKey);
  });
}

// Freshness scale for the card badge: how much can the numbers still be
// trusted? Green is inside the window the source itself declares (15m for the
// official APIs), then one step per age bracket. A failed probe drops straight
// to `expired` — a snapshot collected minutes ago still means nothing once the
// provider login expired, and a green dot there would hide the problem.
export type ProviderLimitFreshness = "fresh" | "recent" | "aging" | "expired";

const DEFAULT_FRESHNESS_WINDOW_SECONDS = 900;
const DAY_SECONDS = 86_400;
const AGING_LIMIT_SECONDS = 3 * DAY_SECONDS;

function isFailedStatus(status: string | undefined): boolean {
  return status === "unavailable" || status === "error";
}

export function providerLimitFreshness(
  record: ProviderLimitSnapshot,
  now = Date.now(),
): ProviderLimitFreshness {
  if (isFailedStatus(record.status) || isFailedStatus(record.last_attempt_status)) return "expired";

  const collectedAt = timestamp(record.last_successful_at || record.checked_at);
  if (collectedAt === 0) return "expired";

  const declaredWindow = record.source?.freshness_seconds ?? 0;
  const windowSeconds = declaredWindow > 0 ? declaredWindow : DEFAULT_FRESHNESS_WINDOW_SECONDS;
  const ageSeconds = Math.max(0, (now - collectedAt) / 1_000);

  if (ageSeconds <= windowSeconds) return "fresh";
  if (ageSeconds <= DAY_SECONDS) return "recent";
  if (ageSeconds <= AGING_LIMIT_SECONDS) return "aging";
  return "expired";
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

export function sourceLabel(kind: string): string {
  const labels: Record<string, string> = {
    official_api: "Official API",
    local_auth_state: "Local auth state",
    local_log: "Local log",
    cli: "CLI",
  };
  return labels[kind] ?? titleCase(kind);
}

export function formatFreshness(seconds: number): string {
  if (seconds <= 0) return "—";
  if (seconds % 3_600 === 0) return `${seconds / 3_600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

export function formatRelativeAge(value: string, locale: string, now = Date.now()): string {
  const parsed = timestamp(value);
  if (parsed === 0) return "вЂ”";
  const deltaSeconds = Math.round((parsed - now) / 1_000);
  const absoluteSeconds = Math.abs(deltaSeconds);
  const [amount, unit] =
    absoluteSeconds < 60
      ? [deltaSeconds, "second" as const]
      : absoluteSeconds < 3_600
        ? [Math.round(deltaSeconds / 60), "minute" as const]
        : absoluteSeconds < 86_400
          ? [Math.round(deltaSeconds / 3_600), "hour" as const]
          : [Math.round(deltaSeconds / 86_400), "day" as const];
  return new Intl.RelativeTimeFormat(locale, { numeric: "always" }).format(amount, unit);
}

export function lastGoodSnapshot(
  history: ProviderLimitSnapshot[],
  record: ProviderLimitSnapshot,
): ProviderLimitSnapshot | undefined {
  return history
    .filter(
      (candidate) =>
        candidate.provider === record.provider &&
        candidate.account_key === record.account_key &&
        (candidate.status === "ok" || candidate.status === "partial") &&
        !candidate.stale,
    )
    .toSorted((left, right) => timestamp(right.checked_at) - timestamp(left.checked_at))[0];
}

export function ProviderLimitsOverview({
  wsId,
  overview = EMPTY_OVERVIEW,
  history = EMPTY_HISTORY,
  isLoading,
  isError,
  onRefresh,
}: {
  wsId?: string;
  overview?: ProviderLimitsOverviewResponse;
  history?: ProviderLimitSnapshot[];
  isLoading: boolean;
  isError: boolean;
  onRefresh?: (runtimeId: string) => Promise<void>;
}) {
  const { t } = useT("usage");
  const [view, setView] = useState<ViewMode>("accounts");
  const warningThreshold = useProviderLimitSettingsStore((state) => state.warningThreshold);
  const criticalThreshold = useProviderLimitSettingsStore((state) => state.criticalThreshold);
  const setWarningThreshold = useProviderLimitSettingsStore((state) => state.setWarningThreshold);
  const setCriticalThreshold = useProviderLimitSettingsStore((state) => state.setCriticalThreshold);
  // Canonicalize before reconciling so a snapshot whose buckets are all legacy
  // no longer counts as useful quota, and so the cards and the detail dialog
  // read one bucket set.
  const canonicalHistory = useMemo(() => withCanonicalBuckets(history), [history]);
  const records = useMemo(() => {
    return view === "accounts"
      ? reconcileProviderLimitAccounts(withCanonicalBuckets(overview.accounts), canonicalHistory, "workspace")
      : reconcileProviderLimitAccounts(withCanonicalBuckets(overview.daemons), canonicalHistory, "daemon");
  }, [canonicalHistory, overview.accounts, overview.daemons, view]);
  const hasReportedRecords = view === "accounts"
    ? overview.accounts.length > 0
    : overview.daemons.length > 0;
  const refreshRuntimeID = records.find((record) => record.runtime_id)?.runtime_id;
  // A refresh is queued per runtime, not per provider, so the daemon re-probes
  // every provider it owns at once. Tracking the runtime instead of a plain
  // boolean lets each card decide whether the running refresh covers it.
  const [refreshingRuntimeID, setRefreshingRuntimeID] = useState<string | null>(null);

  const handleRefresh = async () => {
    if (!refreshRuntimeID || !onRefresh) return;
    setRefreshingRuntimeID(refreshRuntimeID);
    try {
      await onRefresh(refreshRuntimeID);
    } finally {
      setRefreshingRuntimeID(null);
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
        <button type="button" className="rounded-md border px-2.5 py-1 text-xs font-medium disabled:opacity-50" disabled={!refreshRuntimeID || refreshingRuntimeID !== null} onClick={() => void handleRefresh()}>
          {t(($) => $.provider_limits.refresh)}
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
                  wsId={wsId ?? ""}
                  record={record}
                  history={canonicalHistory}
                  warningThreshold={warningThreshold}
                  criticalThreshold={criticalThreshold}
                  isRefreshing={record.runtime_id === refreshingRuntimeID}
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
  wsId,
  record,
  history,
  warningThreshold,
  criticalThreshold,
  isRefreshing,
}: {
  wsId: string;
  record: ProviderLimitSnapshot;
  history: ProviderLimitSnapshot[];
  warningThreshold: number;
  criticalThreshold: number;
  isRefreshing: boolean;
}) {
  const { t, i18n } = useT("usage");
  const locale = i18n.resolvedLanguage ?? i18n.language;
  const lastSuccessfulAt =
    record.last_successful_at || (record.buckets.length > 0 ? record.checked_at : "");
  const lastAttemptedAt = record.last_attempted_at || record.checked_at;
  const reason = useProviderLimitReasonLabel(record.error_note);
  return (
    <article className="rounded-md border p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h3 className="text-sm font-medium">{titleCase(record.provider)}</h3>
          {record.account_label && (
            <p className="text-xs text-muted-foreground">{subscriptionLabel(record.account_label)}</p>
          )}
          <p className="text-xs text-muted-foreground">
            {lastSuccessfulAt
              ? t(($) => $.provider_limits.updated, {
                  value: formatRelativeAge(lastSuccessfulAt, locale),
                })
              : t(($) => $.provider_limits.data_unavailable_checked, {
                  value: formatRelativeAge(lastAttemptedAt, locale),
                })}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <FreshnessBadge record={record} />
          {isRefreshing && <RefreshSpinner />}
          <ProviderLimitDetail wsId={wsId} record={record} history={history} />
        </div>
      </div>
      <div className="mt-3 space-y-2">
        {record.buckets.map((bucket) => (
          <BucketRow key={bucket.id} bucket={bucket} warningThreshold={warningThreshold} criticalThreshold={criticalThreshold} />
        ))}
        {record.buckets.length === 0 && (
          <p className="text-xs text-muted-foreground">
            {reason || t(($) => $.provider_limits.no_buckets)}
          </p>
        )}
      </div>
    </article>
  );
}


// Per-card progress for the running refresh. The icon is decorative, so the
// accessible name lives on the `status` wrapper — screen readers announce the
// card that is being refreshed instead of an unnamed image.
function RefreshSpinner() {
  const { t } = useT("usage");
  const label = t(($) => $.provider_limits.refreshing);
  return (
    <span role="status" aria-label={label} title={label} className="text-muted-foreground">
      <Loader2 className="size-4 animate-spin motion-reduce:animate-none" aria-hidden />
    </span>
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

// A provider that reports no quota is only useful if the card says what to do
// about it. The reason code is the daemon's machine-readable diagnosis
// (`credential_missing` for Factory, `reauth_required` for Cursor), so it maps
// to one actionable sentence shared by the card and the Details dialog.
export function useProviderLimitReasonLabel(errorNote: string | undefined): string {
  const { t } = useT("usage");
  const labels: Record<string, string> = {
    auth_expired: t(($) => $.provider_limits.reasons.auth_expired),
    authentication_required: t(($) => $.provider_limits.reasons.authentication_required),
    reauth_required: t(($) => $.provider_limits.reasons.reauth_required),
    onboarding_required: t(($) => $.provider_limits.reasons.onboarding_required),
    usage_unavailable: t(($) => $.provider_limits.reasons.usage_unavailable),
    rate_limited: t(($) => $.provider_limits.reasons.rate_limited),
    unsupported_platform: t(($) => $.provider_limits.reasons.unsupported_platform),
    credential_missing: t(($) => $.provider_limits.reasons.credential_missing),
    credential_invalid: t(($) => $.provider_limits.reasons.credential_invalid),
  };
  if (!errorNote) return "";
  return labels[errorNote] ?? titleCase(errorNote);
}

export function useProviderLimitStatusLabel(status: string): string {
  const { t } = useT("usage");
  const labels: Record<string, string> = {
    ok: t(($) => $.provider_limits.status.ok),
    stale: t(($) => $.provider_limits.status.stale),
    partial: t(($) => $.provider_limits.status.partial),
    unavailable: t(($) => $.provider_limits.status.unavailable),
    error: t(($) => $.provider_limits.status.error),
  };
  return labels[status] ?? titleCase(status);
}

const FRESHNESS_BADGE_CLASS: Record<ProviderLimitFreshness, string> = {
  fresh: "bg-success/10 text-success",
  recent: "bg-warning/10 text-warning",
  aging: "bg-warning-strong/10 text-warning-strong",
  expired: "bg-destructive/10 text-destructive",
};

const FRESHNESS_DOT_CLASS: Record<ProviderLimitFreshness, string> = {
  fresh: "bg-success",
  recent: "bg-warning",
  aging: "bg-warning-strong",
  expired: "bg-destructive",
};

// The dashboard is a long-lived page and the limits query does not poll, so
// nothing re-renders the badge on its own. Without a clock of its own the
// colour is frozen at whatever the age was when the page happened to render.
// A minute is the finest resolution any of the four steps needs.
const FRESHNESS_TICK_MS = 60_000;

function useNowTick(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

function FreshnessBadge({ record }: { record: ProviderLimitSnapshot }) {
  const { t } = useT("usage");
  const now = useNowTick(FRESHNESS_TICK_MS);
  const level = providerLimitFreshness(record, now);
  const labels: Record<ProviderLimitFreshness, string> = {
    fresh: t(($) => $.provider_limits.freshness_level.fresh),
    recent: t(($) => $.provider_limits.freshness_level.recent),
    aging: t(($) => $.provider_limits.freshness_level.aging),
    expired: t(($) => $.provider_limits.freshness_level.expired),
  };
  const hints: Record<ProviderLimitFreshness, string> = {
    fresh: t(($) => $.provider_limits.freshness_hint.fresh),
    recent: t(($) => $.provider_limits.freshness_hint.recent),
    aging: t(($) => $.provider_limits.freshness_hint.aging),
    expired: t(($) => $.provider_limits.freshness_hint.expired),
  };

  // Colour is never the only channel: the badge keeps a visible label and the
  // accessible name carries the full explanation of what the colour means.
  return (
    <span
      role="img"
      aria-label={`${labels[level]}: ${hints[level]}`}
      title={hints[level]}
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ${FRESHNESS_BADGE_CLASS[level]}`}
    >
      <span className={`size-1.5 shrink-0 rounded-full ${FRESHNESS_DOT_CLASS[level]}`} />
      {labels[level]}
    </span>
  );
}
