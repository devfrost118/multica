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
