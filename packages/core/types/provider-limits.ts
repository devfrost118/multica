export interface ProviderLimitSource {
  kind: string;
  freshness_seconds: number;
  confidence: string;
}

export interface ProviderLimitBucket {
  id: string;
  label: string;
  unit: string;
  limit_value: number | null;
  used_value: number | null;
  remaining_value: number | null;
  resets_at: string | null;
  status: string;
  note: string;
}

export interface ProviderLimitSnapshot {
  runtime_id: string;
  provider: string;
  account_key: string;
  account_label: string;
  checked_at: string;
  status: string;
  source: ProviderLimitSource;
  buckets: ProviderLimitBucket[];
  error_note: string;
  stale: boolean;
}

export interface ProviderLimitsOverviewResponse {
  accounts: ProviderLimitSnapshot[];
  daemons: ProviderLimitSnapshot[];
}

export interface ProviderLimitHistoryResponse {
  snapshots: ProviderLimitSnapshot[];
}
