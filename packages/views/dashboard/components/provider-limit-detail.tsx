"use client";

import { useMemo, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import {
  computeBucketPace,
  listBucketOptions,
  selectBucketHistory,
  type PaceResult,
  type PaceUnavailableReason,
  useDeleteProviderCredential,
  useProviderCredentials,
  useSaveProviderCredential,
} from "@multica/core/provider-limits";
import type { ProviderLimitSnapshot } from "@multica/core/types";
import { useT } from "../../i18n";
import { Clock3 } from "lucide-react";
import {
  formatFreshness,
  lastGoodSnapshot,
  sourceLabel,
  subscriptionLabel,
  titleCase,
} from "./provider-limits-overview";
import { ProviderLimitHistoryChart } from "./provider-limit-history-chart";

// Compact number formatting so a token/credit rate like 1234.5/hour reads
// as "1,235" rather than a long decimal, while percent rates keep one
// decimal place since they're usually single-digit.
function formatRate(ratePerHour: number, unit: string): string {
  const rounded = unit === "percent" ? ratePerHour.toFixed(1) : Math.round(ratePerHour).toLocaleString();
  return unit === "percent" ? `${rounded}%/h` : `${rounded} ${unit}/h`;
}

function formatRunway(seconds: number): string {
  if (seconds < 3_600) return `${Math.max(1, Math.round(seconds / 60))}m`;
  if (seconds < 86_400) return `${(seconds / 3_600).toFixed(1)}h`;
  return `${(seconds / 86_400).toFixed(1)}d`;
}

export function ProviderLimitDetail({
  wsId,
  record,
  history,
}: {
  wsId?: string;
  record: ProviderLimitSnapshot;
  history: ProviderLimitSnapshot[];
}) {
  const { t } = useT("usage");
  const [open, setOpen] = useState(false);
  const [selectedBucketId, setSelectedBucketId] = useState<string | null>(null);

  const bucketOptions = useMemo(() => {
    const fromHistory = listBucketOptions(history, record.provider, record.account_key);
    if (fromHistory.length > 0) return fromHistory;
    return record.buckets.map((bucket) => ({ id: bucket.id, label: bucket.label, unit: bucket.unit }));
  }, [history, record.provider, record.account_key, record.buckets]);

  const activeBucketId = selectedBucketId ?? bucketOptions[0]?.id ?? null;
  const activeBucket = bucketOptions.find((bucket) => bucket.id === activeBucketId) ?? null;

  const points = useMemo(
    () =>
      activeBucketId
        ? selectBucketHistory(history, record.provider, record.account_key, activeBucketId)
        : [],
    [history, record.provider, record.account_key, activeBucketId],
  );

  const pace = useMemo(() => computeBucketPace(points), [points]);

  return (
    <>
      <Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)}>
        {t(($) => $.provider_limits.detail.trigger)}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{titleCase(record.provider)}</DialogTitle>
            {record.account_label && <DialogDescription>{subscriptionLabel(record.account_label)}</DialogDescription>}
          </DialogHeader>

          {bucketOptions.length === 0 ? (
            <div className="space-y-4">
              <p className="text-sm text-muted-foreground">{t(($) => $.provider_limits.no_buckets)}</p>
              <ProviderLimitMetadata record={record} history={history} />
            </div>
          ) : (
            <div className="space-y-4">
              {bucketOptions.length > 1 && activeBucketId && (
                <Tabs value={activeBucketId} onValueChange={(value) => setSelectedBucketId(String(value))}>
                  <TabsList>
                    {bucketOptions.map((bucket) => (
                      <TabsTrigger key={bucket.id} value={bucket.id}>
                        {bucket.label}
                      </TabsTrigger>
                    ))}
                  </TabsList>
                </Tabs>
              )}

              {points.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t(($) => $.provider_limits.detail.no_history)}</p>
              ) : (
                <ProviderLimitHistoryChart points={points} unit={activeBucket?.unit ?? ""} />
              )}

              <PaceSummary pace={pace} unit={activeBucket?.unit ?? ""} />

              <ProviderLimitMetadata record={record} history={history} />
            </div>
          )}

          {record.provider === "factory" && (
            <FactoryCredentialSection wsId={wsId ?? ""} record={record} />
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

function FactoryCredentialSection({ wsId, record }: { wsId: string; record: ProviderLimitSnapshot }) {
  const { t } = useT("usage");
  const credentialsQuery = useProviderCredentials(wsId, record.provider === "factory");
  const saveCredential = useSaveProviderCredential(wsId);
  const deleteCredential = useDeleteProviderCredential(wsId);
  const [token, setToken] = useState("");
  const [accountLabel, setAccountLabel] = useState("");
  const credential = credentialsQuery.data?.find((item) => item.account_key === record.account_key);
  const pending = saveCredential.isPending || deleteCredential.isPending;
  const error = saveCredential.error ?? deleteCredential.error ?? credentialsQuery.error;

  const submit = async () => {
    if (!token.trim()) return;
    try {
      await saveCredential.mutateAsync({
        id: credential?.id,
        request: { provider: "factory", token: token.trim(), account_label: accountLabel.trim() || undefined },
      });
      setToken("");
      setAccountLabel("");
    } catch {
      // The mutation exposes a sanitized user-facing error below.
    }
  };

  const remove = async () => {
    if (!credential) return;
    try {
      await deleteCredential.mutateAsync(credential.id);
      setToken("");
    } catch {
      // The mutation exposes a sanitized user-facing error below.
    }
  };

  return (
    <section className="space-y-2 border-t pt-3" aria-label={t(($) => $.provider_limits.credentials.title)}>
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm font-medium">{t(($) => $.provider_limits.credentials.title)}</p>
        <Badge variant={credential ? "secondary" : "outline"}>{credential ? t(($) => $.provider_limits.credentials.connected) : t(($) => $.provider_limits.credentials.not_connected)}</Badge>
      </div>
      {credential && (
        <div className="text-xs text-muted-foreground">
          <p>{t(($) => $.provider_limits.credentials.fingerprint, { value: credential.fingerprint })}</p>
          <p>{t(($) => $.provider_limits.credentials.validation, { value: credential.last_validation_status })}</p>
          {credential.last_validation_note && <p>{credential.last_validation_note}</p>}
        </div>
      )}
      {!credential && (
        <input aria-label={t(($) => $.provider_limits.credentials.account_label)} className="w-full rounded-md border bg-background px-3 py-2 text-sm" value={accountLabel} maxLength={80} placeholder={t(($) => $.provider_limits.credentials.account_label_placeholder)} onChange={(event) => setAccountLabel(event.target.value)} />
      )}
      <input aria-label={t(($) => $.provider_limits.credentials.token)} className="w-full rounded-md border bg-background px-3 py-2 text-sm" type="password" autoComplete="off" value={token} placeholder={credential ? t(($) => $.provider_limits.credentials.replacement_token) : t(($) => $.provider_limits.credentials.token)} onChange={(event) => setToken(event.target.value)} />
      <div className="flex gap-2">
        <Button type="button" size="sm" disabled={pending || !token.trim()} onClick={() => void submit()}>{credential ? t(($) => $.provider_limits.credentials.replace) : t(($) => $.provider_limits.credentials.connect)}</Button>
        {credential && <Button type="button" size="sm" variant="destructive" disabled={pending} onClick={() => void remove()}>{t(($) => $.provider_limits.credentials.remove)}</Button>}
      </div>
      {error && <p role="alert" className="text-xs text-destructive">{t(($) => $.provider_limits.credentials.action_failed)}</p>}
    </section>
  );
}
function ProviderLimitMetadata({
  record,
  history,
}: {
  wsId?: string;
  record: ProviderLimitSnapshot;
  history: ProviderLimitSnapshot[];
}) {
  const { t, i18n } = useT("usage");
  const locale = i18n.resolvedLanguage ?? i18n.language;
  const lastGood = lastGoodSnapshot(history, record);
  const checkedAt = record.checked_at ? new Date(record.checked_at).toLocaleString(locale) : t(($) => $.provider_limits.unknown);

  return (
    <div className="space-y-1 border-t pt-3 text-xs text-muted-foreground">
      <p>{sourceLabel(record.source.kind)} · {record.source.confidence || t(($) => $.provider_limits.unknown)}</p>
      <p>{t(($) => $.provider_limits.freshness, { value: formatFreshness(record.source.freshness_seconds) })}</p>
      <p className="flex items-center gap-1"><Clock3 className="size-3" />{t(($) => $.provider_limits.checked_at, { value: checkedAt })}</p>
      {lastGood && lastGood.checked_at !== record.checked_at && (
        <p>{t(($) => $.provider_limits.last_good, { value: new Date(lastGood.checked_at).toLocaleString(locale) })}</p>
      )}
      {record.error_note && <p>{t(($) => $.provider_limits.reason, { value: titleCase(record.error_note) })}</p>}
    </div>
  );
}

function PaceSummary({ pace, unit }: { pace: PaceResult; unit: string }) {
  const { t } = useT("usage");
  const unavailableReasons: Record<PaceUnavailableReason, string> = {
    insufficient_points: t(($) => $.provider_limits.detail.unavailable.insufficient_points),
    stale_data: t(($) => $.provider_limits.detail.unavailable.stale_data),
    not_decreasing: t(($) => $.provider_limits.detail.unavailable.not_decreasing),
    zero_elapsed_time: t(($) => $.provider_limits.detail.unavailable.zero_elapsed_time),
  };

  if (!pace.available) {
    const reason = pace.reason ?? "insufficient_points";
    return (
      <div className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">
        <p className="font-medium text-foreground">{t(($) => $.provider_limits.detail.pace_title)}</p>
        <p className="mt-1">{unavailableReasons[reason]}</p>
      </div>
    );
  }

  const exhausted = pace.runwaySeconds !== undefined && pace.runwaySeconds <= 0;

  return (
    <div className="rounded-md border p-3 text-xs">
      <div className="flex items-center justify-between gap-2">
        <p className="font-medium">{t(($) => $.provider_limits.detail.pace_title)}</p>
        <Badge variant="secondary">{t(($) => $.provider_limits.detail.estimated)}</Badge>
      </div>
      <p className="mt-1 text-muted-foreground">
        {t(($) => $.provider_limits.detail.pace_value, { value: formatRate(pace.ratePerHour ?? 0, unit) })}
      </p>
      <p className="text-muted-foreground">
        {exhausted
          ? t(($) => $.provider_limits.detail.runway_exhausted)
          : t(($) => $.provider_limits.detail.runway_value, {
              value: formatRunway(pace.runwaySeconds ?? 0),
            })}
      </p>
    </div>
  );
}

