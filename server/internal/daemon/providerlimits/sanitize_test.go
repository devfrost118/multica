package providerlimits

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSanitizeSnapshots_RemovesSecretsAndAuthPathsWithoutMutatingInput(t *testing.T) {
	t.Parallel()

	original := AccountSnapshot{
		Provider:     "claude",
		AccountKey:   "account-1",
		AccountLabel: "Bearer secret-token",
		CheckedAt:    time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC),
		Status:       StatusPartial,
		Source: Source{
			Kind:       SourceKindLocalAuthState,
			Confidence: ConfidenceOfficial,
		},
		Buckets: []Bucket{{
			ID:    "weekly",
			Label: "Weekly",
			Unit:  UnitPercent,
			Note:  `read C:\\Users\\Ada\\.claude\\.credentials.json with sk-super-secret`,
		}},
		ErrorNote: "raw output: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature",
	}

	sanitized := SanitizeSnapshots([]AccountSnapshot{original}, SanitizationCaps{MaxTextLength: 80, MaxBucketsPerSnapshot: 4})
	if len(sanitized) != 1 {
		t.Fatalf("sanitized snapshot count = %d", len(sanitized))
	}
	got := sanitized[0]
	if got.AccountLabel != "" || got.Buckets[0].Note != "" || got.ErrorNote != "" {
		t.Fatalf("unsafe text crossed boundary: %#v", got)
	}
	if original.AccountLabel == "" || original.Buckets[0].Note == "" || original.ErrorNote == "" {
		t.Fatalf("SanitizeSnapshots mutated input: %#v", original)
	}
}

func TestSanitizeSnapshots_DoesNotPreserveTokenLikeEnumOrProviderValues(t *testing.T) {
	t.Parallel()

	secret := "Bearer should-not-cross"
	sanitized := SanitizeSnapshots([]AccountSnapshot{{
		Provider:   secret,
		AccountKey: "account-1",
		CheckedAt:  time.Now().UTC(),
		Status:     Status(secret),
		Source: Source{
			Kind:       SourceKind(secret),
			Confidence: Confidence(secret),
		},
		Buckets: []Bucket{{ID: secret, Label: secret, Unit: Unit(secret), Status: Status(secret)}},
	}}, SanitizationCaps{})
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatalf("marshal sanitized snapshots: %v", err)
	}
	if strings.Contains(string(encoded), "should-not-cross") {
		t.Fatalf("unsafe enum-like text crossed boundary: %s", encoded)
	}
}

func TestSanitizeSnapshots_RejectsRawPayloadsInIdentityAndDisplayFields(t *testing.T) {
	t.Parallel()

	sanitized := SanitizeSnapshots([]AccountSnapshot{{
		Provider:     "password=provider-secret",
		AccountKey:   `{"raw":"account-secret"}`,
		AccountLabel: "password=label-secret",
		CheckedAt:    time.Now().UTC(),
		Status:       StatusOK,
		Source: Source{
			Kind:       SourceKindCLI,
			Confidence: ConfidenceObserved,
		},
		Buckets: []Bucket{{
			ID:     `{"raw":"bucket-secret"}`,
			Label:  "password=bucket-secret",
			Unit:   UnitPercent,
			Status: StatusOK,
		}},
	}}, SanitizationCaps{})
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatalf("marshal sanitized snapshots: %v", err)
	}
	for _, forbidden := range []string{"provider-secret", "account-secret", "label-secret", "bucket-secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("unsafe value %q crossed boundary: %s", forbidden, encoded)
		}
	}
}

func TestSanitizeSnapshots_RejectsUnmaskedEmailAccountLabel(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot("claude")
	snapshot.AccountLabel = "alice@example.com"

	sanitized := SanitizeSnapshots([]AccountSnapshot{snapshot}, SanitizationCaps{})
	if got := sanitized[0].AccountLabel; got != "" {
		t.Fatalf("unmasked account label = %q, want empty", got)
	}
}

func TestSanitizeSnapshots_AppliesSnapshotBucketAndTextCaps(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot("codex")
	snapshot.Buckets = []Bucket{
		{ID: "one", Label: "one", Unit: UnitPercent, Note: strings.Repeat("a", 20)},
		{ID: "two", Label: "two", Unit: UnitPercent},
	}

	sanitized := SanitizeSnapshots([]AccountSnapshot{snapshot, snapshot}, SanitizationCaps{
		MaxSnapshots:          1,
		MaxBucketsPerSnapshot: 1,
		MaxTextLength:         8,
	})
	if len(sanitized) != 1 || len(sanitized[0].Buckets) != 1 {
		t.Fatalf("caps were not applied: %#v", sanitized)
	}
	if got := sanitized[0].AccountLabel; got != "profile-" {
		t.Fatalf("capped account label = %q, want eight characters", got)
	}
}

func TestSanitizeSnapshots_AllowsOnlyReasonCodesForDiagnostics(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot("claude")
	snapshot.ErrorNote = `{"raw":"provider response"}`
	snapshot.Buckets[0].Note = "rate_limited"

	sanitized := SanitizeSnapshots([]AccountSnapshot{snapshot}, SanitizationCaps{})
	if got := sanitized[0].ErrorNote; got != "" {
		t.Fatalf("raw diagnostic = %q, want empty", got)
	}
	if got := sanitized[0].Buckets[0].Note; got != "rate_limited" {
		t.Fatalf("reason code = %q", got)
	}
}
