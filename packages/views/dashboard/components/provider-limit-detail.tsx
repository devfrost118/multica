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
} from "@multica/core/provider-limits";
import type { ProviderLimitSnapshot } from "@multica/core/types";
import { useT } from "../../i18n";
import { titleCase } from "./provider-limits-overview";
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
  record,
  history,
}: {
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
  const displayName = record.account_label || titleCase(record.provider);

  return (
    <>
      <Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)}>
        {t(($) => $.provider_limits.detail.trigger)}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{displayName}</DialogTitle>
            <DialogDescription>
              {titleCase(record.provider)}
              {record.account_key ? ` · ${record.account_key}` : ""}
            </DialogDescription>
          </DialogHeader>

          {bucketOptions.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t(($) => $.provider_limits.no_buckets)}</p>
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
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
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
