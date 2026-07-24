package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProviderLimitRowsCollapsesUnkeyedFailureIntoOnlyKnownAccount(t *testing.T) {
	lastGood := providerLimitResponseRow(
		"0123456789abcdef",
		"profile-plus",
		time.Date(2026, time.July, 24, 17, 30, 0, 0, time.UTC),
		"ok",
		"",
		`[{"id":"weekly","label":"Limit weekly","unit":"percent","used_value":29,"remaining_value":71,"status":"ok"}]`,
	)
	latestFailure := providerLimitResponseRow(
		"unavailable",
		"",
		time.Date(2026, time.July, 24, 17, 45, 0, 0, time.UTC),
		"unavailable",
		"auth_expired",
		`[]`,
	)

	response := providerLimitRows([]db.ProviderLimitSnapshot{latestFailure, lastGood})

	if len(response) != 1 {
		t.Fatalf("response count = %d, want one logical Codex account: %#v", len(response), response)
	}
	record := response[0]
	if record.AccountKey != lastGood.AccountKey || record.AccountLabel != "profile-plus" {
		t.Fatalf("logical identity = %#v, want keyed Plus account", record)
	}
	if record.Status != "ok" || len(record.Buckets) == 0 {
		t.Fatalf("display snapshot = %#v, want persisted last-good quota data", record)
	}
	if record.ErrorNote != "auth_expired" {
		t.Fatalf("diagnostic reason = %q, want latest failed attempt", record.ErrorNote)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var diagnostics map[string]any
	if err := json.Unmarshal(encoded, &diagnostics); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if diagnostics["last_successful_at"] != "2026-07-24T17:30:00Z" {
		t.Fatalf("last_successful_at = %#v, want persisted good timestamp", diagnostics["last_successful_at"])
	}
	if diagnostics["last_attempted_at"] != "2026-07-24T17:45:00Z" ||
		diagnostics["last_attempt_status"] != "unavailable" {
		t.Fatalf("latest attempt diagnostics = %#v", diagnostics)
	}
}

func TestProviderLimitRowsReconstructsLastGoodAfterDaemonRestart(t *testing.T) {
	accountKey := "0123456789abcdef"
	lastGood := providerLimitResponseRow(
		accountKey,
		"profile-plus",
		time.Date(2026, time.July, 24, 17, 30, 0, 0, time.UTC),
		"ok",
		"",
		`[{"id":"weekly","label":"Limit weekly","unit":"percent","used_value":29,"remaining_value":71,"status":"ok"}]`,
	)
	restartFailure := providerLimitResponseRow(
		accountKey,
		"",
		time.Date(2026, time.July, 24, 17, 45, 0, 0, time.UTC),
		"unavailable",
		"auth_expired",
		`[]`,
	)

	response := providerLimitRows([]db.ProviderLimitSnapshot{restartFailure, lastGood})

	if len(response) != 1 || response[0].AccountLabel != "profile-plus" ||
		response[0].Status != "ok" || len(response[0].Buckets) == 0 {
		t.Fatalf("restart reconstruction = %#v, want persisted last-good data", response)
	}
	if response[0].LastAttemptStatus != "unavailable" ||
		!response[0].LastAttemptedAt.Equal(restartFailure.CheckedAt.Time) {
		t.Fatalf("restart diagnostics = %#v", response[0])
	}
}

func TestProviderLimitRowsKeepsOneUnavailableCardWithoutHistory(t *testing.T) {
	failure := providerLimitResponseRow(
		"unavailable",
		"",
		time.Date(2026, time.July, 24, 17, 45, 0, 0, time.UTC),
		"unavailable",
		"authentication_required",
		`[]`,
	)

	response := providerLimitRows([]db.ProviderLimitSnapshot{failure})

	if len(response) != 1 {
		t.Fatalf("response count = %d, want one provider card", len(response))
	}
	record := response[0]
	if record.AccountLabel != "" || string(record.Buckets) != "[]" || record.LastSuccessfulAt != nil {
		t.Fatalf("unavailable record invented account data: %#v", record)
	}
	if record.LastAttemptStatus != "unavailable" || record.ErrorNote != "authentication_required" {
		t.Fatalf("unavailable diagnostics = %#v", record)
	}
}

func TestProviderLimitRowsDoesNotGuessWhenMultipleAccountsExist(t *testing.T) {
	first := providerLimitResponseRow(
		"0123456789abcdef",
		"profile-plus",
		time.Date(2026, time.July, 24, 17, 30, 0, 0, time.UTC),
		"ok",
		"",
		`[{"id":"weekly","label":"Limit weekly","unit":"percent","status":"ok"}]`,
	)
	second := providerLimitResponseRow(
		"fedcba9876543210",
		"profile-pro",
		time.Date(2026, time.July, 24, 17, 31, 0, 0, time.UTC),
		"ok",
		"",
		`[{"id":"weekly","label":"Limit weekly","unit":"percent","status":"ok"}]`,
	)
	unkeyed := providerLimitResponseRow(
		"unavailable",
		"",
		time.Date(2026, time.July, 24, 17, 45, 0, 0, time.UTC),
		"unavailable",
		"authentication_required",
		`[]`,
	)

	response := providerLimitRows([]db.ProviderLimitSnapshot{unkeyed, first, second})

	if len(response) != 3 {
		t.Fatalf("response count = %d, want two real accounts plus ambiguous failure: %#v", len(response), response)
	}
	keys := map[string]bool{}
	for _, record := range response {
		keys[record.AccountKey] = true
	}
	for _, key := range []string{"0123456789abcdef", "fedcba9876543210", "unavailable"} {
		if !keys[key] {
			t.Fatalf("missing account key %q in %#v", key, response)
		}
	}
}

func TestProviderLimitRowsByDaemonReconcilesWithinEachDaemon(t *testing.T) {
	goodOne := providerLimitResponseRow(
		"0123456789abcdef",
		"profile-plus",
		time.Date(2026, time.July, 24, 17, 30, 0, 0, time.UTC),
		"ok",
		"",
		`[{"id":"weekly","label":"Limit weekly","unit":"percent","status":"ok"}]`,
	)
	failureOne := providerLimitResponseRow(
		"unavailable",
		"",
		time.Date(2026, time.July, 24, 17, 45, 0, 0, time.UTC),
		"unavailable",
		"auth_expired",
		`[]`,
	)
	goodTwo := providerLimitRowOnDaemon(
		providerLimitResponseRow(
			"fedcba9876543210",
			"profile-pro",
			time.Date(2026, time.July, 24, 17, 31, 0, 0, time.UTC),
			"ok",
			"",
			`[{"id":"weekly","label":"Limit weekly","unit":"percent","status":"ok"}]`,
		),
		"daemon-2",
	)
	failureTwo := providerLimitRowOnDaemon(
		providerLimitResponseRow(
			"unavailable",
			"",
			time.Date(2026, time.July, 24, 17, 46, 0, 0, time.UTC),
			"unavailable",
			"authentication_required",
			`[]`,
		),
		"daemon-2",
	)

	response := providerLimitRowsByDaemon([]db.ProviderLimitSnapshot{
		failureOne,
		goodOne,
		failureTwo,
		goodTwo,
	})

	if len(response) != 2 {
		t.Fatalf("response count = %d, want one logical card per daemon: %#v", len(response), response)
	}
	for _, record := range response {
		if record.AccountKey == "unavailable" || len(record.Buckets) == 0 {
			t.Fatalf("daemon record was not reconciled: %#v", record)
		}
	}
}

func TestProviderLimitHistoryRowsPreservesEverySnapshot(t *testing.T) {
	accountKey := "0123456789abcdef"
	lastGood := providerLimitResponseRow(
		accountKey,
		"profile-plus",
		time.Date(2026, time.July, 24, 17, 30, 0, 0, time.UTC),
		"ok",
		"",
		`[{"id":"weekly","label":"Limit weekly","unit":"percent","status":"ok"}]`,
	)
	latestFailure := providerLimitResponseRow(
		accountKey,
		"",
		time.Date(2026, time.July, 24, 17, 45, 0, 0, time.UTC),
		"unavailable",
		"auth_expired",
		`[]`,
	)

	response := providerLimitHistoryRows([]db.ProviderLimitSnapshot{latestFailure, lastGood})

	if len(response) != 2 {
		t.Fatalf("history count = %d, want every persisted point: %#v", len(response), response)
	}
	if response[0].CheckedAt.Equal(response[1].CheckedAt) {
		t.Fatalf("history timestamps collapsed: %#v", response)
	}
}

func providerLimitResponseRow(
	accountKey string,
	accountLabel string,
	checkedAt time.Time,
	status string,
	errorNote string,
	buckets string,
) db.ProviderLimitSnapshot {
	return db.ProviderLimitSnapshot{
		RuntimeID:              parseUUID(testRuntimeID),
		DaemonID:               "daemon-1",
		Provider:               "codex",
		AccountKey:             accountKey,
		AccountLabel:           accountLabel,
		CheckedAt:              pgtype.Timestamptz{Time: checkedAt, Valid: true},
		Status:                 status,
		SourceKind:             "official_api",
		SourceConfidence:       "official",
		SourceFreshnessSeconds: 900,
		Buckets:                json.RawMessage(buckets),
		ErrorNote:              errorNote,
	}
}

func providerLimitRowOnDaemon(row db.ProviderLimitSnapshot, daemonID string) db.ProviderLimitSnapshot {
	row.DaemonID = daemonID
	return row
}
