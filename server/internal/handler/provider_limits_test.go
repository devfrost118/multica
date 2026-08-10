package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReportProviderLimitsRejectsUnknownAndOversizePayloads(t *testing.T) {
	t.Parallel()

	unknown := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+testRuntimeID+"/provider-limits", map[string]any{
		"snapshots":   []any{},
		"raw_payload": "must-not-be-accepted",
	}, testWorkspaceID, "provider-limits-test-daemon")
	unknownRecorder := httptest.NewRecorder()
	testHandler.ReportProviderLimits(unknownRecorder, withURLParam(unknown, "runtimeId", testRuntimeID))
	if unknownRecorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown payload status = %d, want %d: %s", unknownRecorder.Code, http.StatusBadRequest, unknownRecorder.Body.String())
	}

	snapshots := make([]map[string]any, 33)
	for index := range snapshots {
		snapshots[index] = providerLimitsTestSnapshot(time.Now().UTC())
	}
	oversize := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+testRuntimeID+"/provider-limits", map[string]any{
		"snapshots": snapshots,
	}, testWorkspaceID, "provider-limits-test-daemon")
	oversizeRecorder := httptest.NewRecorder()
	testHandler.ReportProviderLimits(oversizeRecorder, withURLParam(oversize, "runtimeId", testRuntimeID))
	if oversizeRecorder.Code != http.StatusBadRequest {
		t.Fatalf("oversize payload status = %d, want %d: %s", oversizeRecorder.Code, http.StatusBadRequest, oversizeRecorder.Body.String())
	}
}

func TestReportProviderLimitsPersistsOneSnapshotForDuplicateReports(t *testing.T) {
	checkedAt := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	payload := map[string]any{"snapshots": []any{providerLimitsTestSnapshot(checkedAt)}}

	for attempt := 0; attempt < 2; attempt++ {
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+testRuntimeID+"/provider-limits", payload, testWorkspaceID, "provider-limits-test-daemon")
		recorder := httptest.NewRecorder()
		testHandler.ReportProviderLimits(recorder, withURLParam(req, "runtimeId", testRuntimeID))
		if recorder.Code != http.StatusOK {
			t.Fatalf("report attempt %d status = %d, want %d: %s", attempt+1, recorder.Code, http.StatusOK, recorder.Body.String())
		}
	}

	var count int
	if err := testPool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM provider_limit_snapshots
		WHERE workspace_id = $1 AND runtime_id = $2 AND provider = 'claude' AND account_key = 'a1b2c3d4'
	`, testWorkspaceID, testRuntimeID).Scan(&count); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if count != 1 {
		t.Fatalf("snapshot count = %d, want 1", count)
	}

	t.Cleanup(func() {
		testPool.Exec(t.Context(), `DELETE FROM provider_limit_snapshots WHERE workspace_id = $1 AND runtime_id = $2`, testWorkspaceID, testRuntimeID)
	})
}

// Factory reports "no credential configured" as an unkeyed snapshot. Dropping
// it because no stored credential matches would make the Factory card
// impossible to reach: the card is the only entry point to the Details dialog
// that onboards the credential (FRO-206). A snapshot that claims a specific
// account key is still refused when that credential does not exist.
func TestReportProviderLimitsStoresUnkeyedFactorySnapshotWithoutCredential(t *testing.T) {
	checkedAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	payload := map[string]any{"snapshots": []any{
		providerLimitsFactorySnapshot(checkedAt, "unavailable", "credential_missing"),
		providerLimitsFactorySnapshot(checkedAt, "00112233445566778899aabbccddeeff", "credential_invalid"),
	}}

	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+testRuntimeID+"/provider-limits", payload, testWorkspaceID, "provider-limits-test-daemon")
	recorder := httptest.NewRecorder()
	testHandler.ReportProviderLimits(recorder, withURLParam(req, "runtimeId", testRuntimeID))
	if recorder.Code != http.StatusOK {
		t.Fatalf("report status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var storedKeys []string
	rows, err := testPool.Query(t.Context(), `
		SELECT account_key
		FROM provider_limit_snapshots
		WHERE workspace_id = $1 AND runtime_id = $2 AND provider = 'factory'
	`, testWorkspaceID, testRuntimeID)
	if err != nil {
		t.Fatalf("query factory snapshots: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var accountKey string
		if err := rows.Scan(&accountKey); err != nil {
			t.Fatalf("scan factory snapshot: %v", err)
		}
		storedKeys = append(storedKeys, accountKey)
	}
	if len(storedKeys) != 1 || storedKeys[0] != "unavailable" {
		t.Fatalf("stored factory account keys = %v, want only the unkeyed credential_missing snapshot", storedKeys)
	}

	t.Cleanup(func() {
		testPool.Exec(t.Context(), `DELETE FROM provider_limit_snapshots WHERE workspace_id = $1 AND runtime_id = $2`, testWorkspaceID, testRuntimeID)
	})
}

func TestReportProviderLimitsRejectsRuntimeOutsideDaemonWorkspace(t *testing.T) {
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+testRuntimeID+"/provider-limits", map[string]any{
		"snapshots": []any{providerLimitsTestSnapshot(time.Now().UTC())},
	}, "00000000-0000-0000-0000-000000000000", "provider-limits-test-daemon")
	recorder := httptest.NewRecorder()
	testHandler.ReportProviderLimits(recorder, withURLParam(req, "runtimeId", testRuntimeID))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace status = %d, want %d: %s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func providerLimitsFactorySnapshot(checkedAt time.Time, accountKey, errorNote string) map[string]any {
	return map[string]any{
		"provider":    "factory",
		"account_key": accountKey,
		"checked_at":  checkedAt.Format(time.RFC3339),
		"status":      "unavailable",
		"source": map[string]any{
			"kind":              "official_api",
			"confidence":        "official",
			"freshness_seconds": 900,
		},
		"buckets":    []any{},
		"error_note": errorNote,
	}
}

func providerLimitsTestSnapshot(checkedAt time.Time) map[string]any {
	return map[string]any{
		"provider":      "claude",
		"account_key":   "a1b2c3d4",
		"account_label": "a***@example.com",
		"checked_at":    checkedAt.Format(time.RFC3339),
		"status":        "ok",
		"source": map[string]any{
			"kind":              "official_api",
			"confidence":        "official",
			"freshness_seconds": 0,
		},
		"buckets": []any{
			map[string]any{
				"id":              "weekly",
				"label":           "Weekly",
				"unit":            "percent",
				"remaining_value": 75.0,
				"status":          "ok",
			},
		},
	}
}
