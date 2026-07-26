package securitytest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	googleuuid "github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/providercredentials"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	workspaceOwnID       = "11111111-1111-1111-1111-111111111111"
	workspaceForeignID   = "22222222-2222-2222-2222-222222222222"
	userID               = "33333333-3333-3333-3333-333333333333"
	memberID             = "44444444-4444-4444-4444-444444444444"
	runtimeID            = "55555555-5555-5555-5555-555555555555"
	credentialOwnID      = "66666666-6666-6666-6666-666666666666"
	credentialForeignID  = "77777777-7777-7777-7777-777777777777"
	providerSnapshotID   = "88888888-8888-8888-8888-888888888888"
	initialFactoryToken  = "factory-initial-secret-token"
	replacedFactoryToken = "factory-replaced-secret-token"
)

func TestFactoryIngestAcceptsOnlyCredentialOwnedByRuntimeWorkspace(t *testing.T) {
	store := newCredentialStore()
	store.credentials = []db.ProviderCredential{
		testCredential(credentialOwnID, workspaceOwnID, []byte("sealed-own")),
		testCredential(credentialForeignID, workspaceForeignID, []byte("sealed-foreign")),
	}
	testHandler := newHandler(t, store)
	checkedAt := time.Date(2026, time.July, 23, 15, 0, 0, 0, time.UTC)
	ownAccountKey := accountKey(credentialOwnID)
	foreignAccountKey := accountKey(credentialForeignID)

	request := daemonRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/provider-limits", map[string]any{
		"snapshots": []any{
			factorySnapshot(ownAccountKey, checkedAt),
			factorySnapshot(foreignAccountKey, checkedAt),
		},
	})
	recorder := httptest.NewRecorder()
	testHandler.ReportProviderLimits(recorder, withURLParam(request, "runtimeId", runtimeID))

	if recorder.Code != http.StatusOK {
		t.Fatalf("ReportProviderLimits status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Accepted int `json:"accepted"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1", response.Accepted)
	}
	if len(store.snapshots) != 1 {
		t.Fatalf("stored snapshots = %d, want 1", len(store.snapshots))
	}
	stored := store.snapshots[0]
	if uuidString(stored.WorkspaceID) != workspaceOwnID || stored.AccountKey != ownAccountKey {
		t.Fatalf("stored cross-workspace snapshot: workspace=%s account=%s", uuidString(stored.WorkspaceID), stored.AccountKey)
	}
	if len(store.validatedCredentialIDs) != 1 || store.validatedCredentialIDs[0] != credentialOwnID {
		t.Fatalf("validated credentials = %v, want only %s", store.validatedCredentialIDs, credentialOwnID)
	}
}

func TestProviderCredentialCRUDNeverEchoesSecrets(t *testing.T) {
	store := newCredentialStore()
	testHandler := newHandler(t, store)

	create := memberRequest(http.MethodPost, "/api/provider-credentials", map[string]any{
		"provider":      "factory",
		"token":         initialFactoryToken,
		"account_label": "Primary Factory",
	})
	createRecorder := httptest.NewRecorder()
	testHandler.CreateProviderCredential(createRecorder, create)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", createRecorder.Code, createRecorder.Body.String())
	}
	assertNoCredentialSecret(t, createRecorder.Body.Bytes(), initialFactoryToken)
	if len(store.credentials) != 1 {
		t.Fatalf("stored credentials = %d, want 1", len(store.credentials))
	}
	if bytes.Contains(store.credentials[0].SealedToken, []byte(initialFactoryToken)) {
		t.Fatal("stored credential contains plaintext token")
	}

	listRecorder := httptest.NewRecorder()
	testHandler.ListProviderCredentials(listRecorder, memberRequest(http.MethodGet, "/api/provider-credentials?provider=factory", nil))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", listRecorder.Code, listRecorder.Body.String())
	}
	assertNoCredentialSecret(t, listRecorder.Body.Bytes(), initialFactoryToken)

	replaceRecorder := httptest.NewRecorder()
	replaceRequest := withURLParam(
		memberRequest(http.MethodPut, "/api/provider-credentials/"+credentialOwnID, map[string]any{"token": replacedFactoryToken}),
		"credentialId",
		credentialOwnID,
	)
	testHandler.ReplaceProviderCredential(replaceRecorder, replaceRequest)
	if replaceRecorder.Code != http.StatusOK {
		t.Fatalf("replace status = %d: %s", replaceRecorder.Code, replaceRecorder.Body.String())
	}
	assertNoCredentialSecret(t, replaceRecorder.Body.Bytes(), initialFactoryToken, replacedFactoryToken)
	opened, err := testHandler.ProviderCredentials.Open(store.credentials[0].SealedToken)
	if err != nil {
		t.Fatalf("open replaced token: %v", err)
	}
	if opened != replacedFactoryToken {
		t.Fatalf("stored replacement token mismatch")
	}

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := withURLParam(
		memberRequest(http.MethodDelete, "/api/provider-credentials/"+credentialOwnID, nil),
		"credentialId",
		credentialOwnID,
	)
	testHandler.DeleteProviderCredential(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
	if len(store.credentials) != 0 {
		t.Fatalf("credentials remain after delete: %d", len(store.credentials))
	}
	if len(store.deletedSnapshotKeys) != 1 || store.deletedSnapshotKeys[0] != accountKey(credentialOwnID) {
		t.Fatalf("deleted snapshot keys = %v", store.deletedSnapshotKeys)
	}
}

func newHandler(t *testing.T, store *credentialStore) *handler.Handler {
	t.Helper()
	box, err := secretbox.New(bytes.Repeat([]byte{9}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	result := &handler.Handler{
		Queries:             db.New(store),
		ProviderCredentials: providercredentials.New(box),
	}
	result.TxStarter = credentialTxStarter{store: store}
	return result
}

func memberRequest(method, path string, body any) *http.Request {
	request := jsonRequest(method, path, body)
	request.Header.Set("X-User-ID", userID)
	request.Header.Set("X-Workspace-ID", workspaceOwnID)
	return request
}

func daemonRequest(method, path string, body any) *http.Request {
	request := jsonRequest(method, path, body)
	ctx := middleware.WithDaemonContext(request.Context(), workspaceOwnID, "factory-security-test-daemon")
	return request.WithContext(ctx)
}

func jsonRequest(method, path string, body any) *http.Request {
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			panic(err)
		}
	}
	request := httptest.NewRequest(method, path, &encoded)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func withURLParam(request *http.Request, key, value string) *http.Request {
	route := chi.NewRouteContext()
	route.URLParams.Add(key, value)
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))
}

func factorySnapshot(key string, checkedAt time.Time) map[string]any {
	return map[string]any{
		"provider":      "factory",
		"account_key":   key,
		"account_label": "Factory account",
		"checked_at":    checkedAt.Format(time.RFC3339),
		"status":        "ok",
		"source": map[string]any{
			"kind":              "official_api",
			"confidence":        "official",
			"freshness_seconds": 900,
		},
		"buckets": []any{
			map[string]any{
				"id":              "standard_weekly",
				"label":           "Standard weekly",
				"unit":            "percent",
				"remaining_value": 75.0,
				"status":          "ok",
			},
		},
	}
}

func assertNoCredentialSecret(t *testing.T, body []byte, secrets ...string) {
	t.Helper()
	text := string(body)
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("credential response echoed secret: %s", text)
		}
	}
	for _, forbiddenField := range []string{`"token"`, `"sealed_token"`} {
		if strings.Contains(text, forbiddenField) {
			t.Fatalf("credential response exposed %s: %s", forbiddenField, text)
		}
	}
}

func accountKey(id string) string {
	hash := sha256.Sum256([]byte(id))
	return hex.EncodeToString(hash[:])[:16]
}

func uuid(value string) pgtype.UUID {
	var parsed pgtype.UUID
	if err := parsed.Scan(value); err != nil {
		panic(err)
	}
	return parsed
}

func uuidString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return googleuuid.UUID(value.Bytes).String()
}

func testCredential(id, workspaceID string, sealed []byte) db.ProviderCredential {
	now := pgtype.Timestamptz{Time: time.Date(2026, time.July, 23, 14, 0, 0, 0, time.UTC), Valid: true}
	return db.ProviderCredential{
		ID:                   uuid(id),
		WorkspaceID:          uuid(workspaceID),
		Provider:             "factory",
		AccountLabel:         "Factory account",
		SealedToken:          append([]byte(nil), sealed...),
		Fingerprint:          "abcdef123456",
		LastValidationStatus: "pending",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

type credentialStore struct {
	credentials            []db.ProviderCredential
	snapshots              []db.ProviderLimitSnapshot
	deletedSnapshotKeys    []string
	validatedCredentialIDs []string
}

func newCredentialStore() *credentialStore {
	return &credentialStore{}
}

func (store *credentialStore) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(query, "-- name: DeleteProviderCredential"):
		id := uuidString(args[0].(pgtype.UUID))
		workspaceID := uuidString(args[1].(pgtype.UUID))
		next := make([]db.ProviderCredential, 0, len(store.credentials))
		affected := int64(0)
		for _, credential := range store.credentials {
			if uuidString(credential.ID) == id && uuidString(credential.WorkspaceID) == workspaceID {
				affected++
				continue
			}
			next = append(next, credential)
		}
		store.credentials = next
		return pgconn.NewCommandTag(commandTag("DELETE", affected)), nil
	case strings.Contains(query, "-- name: DeleteProviderLimitSnapshotsForAccount"):
		store.deletedSnapshotKeys = appendCopy(store.deletedSnapshotKeys, args[2].(string))
		return pgconn.NewCommandTag("DELETE 1"), nil
	case strings.Contains(query, "-- name: UpdateProviderCredentialValidation"):
		id := uuidString(args[0].(pgtype.UUID))
		workspaceID := uuidString(args[1].(pgtype.UUID))
		next := append([]db.ProviderCredential(nil), store.credentials...)
		for index, credential := range next {
			if uuidString(credential.ID) == id && uuidString(credential.WorkspaceID) == workspaceID {
				credential.LastValidatedAt = args[2].(pgtype.Timestamptz)
				credential.LastValidationStatus = args[3].(string)
				credential.LastValidationNote = args[4].(string)
				next[index] = credential
			}
		}
		store.credentials = next
		store.validatedCredentialIDs = appendCopy(store.validatedCredentialIDs, id)
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(query, "-- name: DeleteExpiredProviderLimitSnapshots"):
		return pgconn.NewCommandTag("DELETE 0"), nil
	default:
		return pgconn.CommandTag{}, errors.New("unexpected Exec query")
	}
}

func (store *credentialStore) Query(_ context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	if !strings.Contains(query, "-- name: ListProviderCredentials") {
		return nil, errors.New("unexpected Query")
	}
	workspaceID := uuidString(args[0].(pgtype.UUID))
	provider := args[1].(string)
	rows := make([][]any, 0, len(store.credentials))
	for _, credential := range store.credentials {
		if uuidString(credential.WorkspaceID) == workspaceID && credential.Provider == provider {
			rows = append(rows, credentialValues(credential))
		}
	}
	return &credentialRows{values: rows}, nil
}

func (store *credentialStore) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(query, "-- name: GetMemberByUserAndWorkspace"):
		now := pgtype.Timestamptz{Time: time.Date(2026, time.July, 23, 14, 0, 0, 0, time.UTC), Valid: true}
		return credentialRow{values: []any{uuid(memberID), args[1].(pgtype.UUID), args[0].(pgtype.UUID), "owner", now}}
	case strings.Contains(query, "-- name: CreateProviderCredential"):
		now := pgtype.Timestamptz{Time: time.Date(2026, time.July, 23, 14, 0, 0, 0, time.UTC), Valid: true}
		credential := db.ProviderCredential{
			ID:                   uuid(credentialOwnID),
			WorkspaceID:          args[0].(pgtype.UUID),
			Provider:             args[1].(string),
			AccountLabel:         args[2].(string),
			SealedToken:          append([]byte(nil), args[3].([]byte)...),
			Fingerprint:          args[4].(string),
			LastValidationStatus: "pending",
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		store.credentials = appendCredential(store.credentials, credential)
		return credentialRow{values: credentialValues(credential)}
	case strings.Contains(query, "-- name: GetProviderCredentialInWorkspace"):
		credential, ok := store.findCredential(args[0].(pgtype.UUID), args[1].(pgtype.UUID))
		if !ok {
			return credentialRow{err: pgx.ErrNoRows}
		}
		return credentialRow{values: credentialValues(credential)}
	case strings.Contains(query, "-- name: ReplaceProviderCredentialToken"):
		id := args[0].(pgtype.UUID)
		workspaceID := args[1].(pgtype.UUID)
		credential, ok := store.findCredential(id, workspaceID)
		if !ok {
			return credentialRow{err: pgx.ErrNoRows}
		}
		credential.SealedToken = append([]byte(nil), args[2].([]byte)...)
		credential.Fingerprint = args[3].(string)
		credential.LastValidatedAt = pgtype.Timestamptz{}
		credential.LastValidationStatus = "pending"
		credential.LastValidationNote = ""
		credential.UpdatedAt = pgtype.Timestamptz{Time: time.Date(2026, time.July, 23, 15, 0, 0, 0, time.UTC), Valid: true}
		store.replaceCredential(credential)
		return credentialRow{values: credentialValues(credential)}
	case strings.Contains(query, "-- name: GetAgentRuntime"):
		now := pgtype.Timestamptz{Time: time.Date(2026, time.July, 23, 14, 0, 0, 0, time.UTC), Valid: true}
		runtime := db.AgentRuntime{
			ID:          uuid(runtimeID),
			WorkspaceID: uuid(workspaceOwnID),
			DaemonID:    pgtype.Text{String: "factory-security-test-daemon", Valid: true},
			Name:        "Factory security test runtime",
			RuntimeMode: "local",
			Provider:    "codex",
			Status:      "online",
			Metadata:    []byte(`{}`),
			LastSeenAt:  now,
			CreatedAt:   now,
			UpdatedAt:   now,
			OwnerID:     uuid(userID),
			Visibility:  "workspace",
		}
		return credentialRow{values: runtimeValues(runtime)}
	case strings.Contains(query, "-- name: UpsertProviderLimitSnapshot"):
		now := pgtype.Timestamptz{Time: time.Date(2026, time.July, 23, 15, 0, 0, 0, time.UTC), Valid: true}
		snapshot := db.ProviderLimitSnapshot{
			ID:                     uuid(providerSnapshotID),
			WorkspaceID:            args[0].(pgtype.UUID),
			RuntimeID:              args[1].(pgtype.UUID),
			DaemonID:               args[2].(string),
			Provider:               args[3].(string),
			AccountKey:             args[4].(string),
			AccountLabel:           args[5].(string),
			CheckedAt:              args[6].(pgtype.Timestamptz),
			Status:                 args[7].(string),
			SourceKind:             args[8].(string),
			SourceConfidence:       args[9].(string),
			SourceFreshnessSeconds: args[10].(int64),
			Buckets:                append([]byte(nil), args[11].([]byte)...),
			ErrorNote:              args[12].(string),
			ContentHash:            args[13].(string),
			CreatedAt:              now,
		}
		store.snapshots = appendSnapshot(store.snapshots, snapshot)
		return credentialRow{values: snapshotValues(snapshot)}
	default:
		return credentialRow{err: errors.New("unexpected QueryRow")}
	}
}

func (store *credentialStore) findCredential(id, workspaceID pgtype.UUID) (db.ProviderCredential, bool) {
	for _, credential := range store.credentials {
		if credential.ID == id && credential.WorkspaceID == workspaceID {
			return credential, true
		}
	}
	return db.ProviderCredential{}, false
}

func (store *credentialStore) replaceCredential(updated db.ProviderCredential) {
	next := append([]db.ProviderCredential(nil), store.credentials...)
	for index, credential := range next {
		if credential.ID == updated.ID && credential.WorkspaceID == updated.WorkspaceID {
			next[index] = updated
		}
	}
	store.credentials = next
}

type credentialTxStarter struct {
	store *credentialStore
}

func (starter credentialTxStarter) Begin(context.Context) (pgx.Tx, error) {
	return &credentialTx{store: starter.store}, nil
}

type credentialTx struct {
	pgx.Tx
	store *credentialStore
}

func (tx *credentialTx) Exec(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	return tx.store.Exec(ctx, query, args...)
}

func (tx *credentialTx) Query(ctx context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	return tx.store.Query(ctx, query, args...)
}

func (tx *credentialTx) QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row {
	return tx.store.QueryRow(ctx, query, args...)
}

func (*credentialTx) Commit(context.Context) error   { return nil }
func (*credentialTx) Rollback(context.Context) error { return nil }

type credentialRow struct {
	values []any
	err    error
}

func (row credentialRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	return scanValues(destinations, row.values)
}

type credentialRows struct {
	pgx.Rows
	values [][]any
	index  int
}

func (rows *credentialRows) Close() {}

func (rows *credentialRows) Err() error { return nil }

func (rows *credentialRows) Next() bool {
	if rows.index >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

func (rows *credentialRows) Scan(destinations ...any) error {
	if rows.index == 0 || rows.index > len(rows.values) {
		return errors.New("Scan called without current row")
	}
	return scanValues(destinations, rows.values[rows.index-1])
}

func scanValues(destinations, values []any) error {
	if len(destinations) != len(values) {
		return errors.New("scan destination count mismatch")
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return errors.New("scan destination must be a pointer")
		}
		value := reflect.ValueOf(values[index])
		if !value.IsValid() {
			target.Elem().SetZero()
			continue
		}
		if !value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("scan value type mismatch")
		}
		target.Elem().Set(value)
	}
	return nil
}

func credentialValues(credential db.ProviderCredential) []any {
	return []any{
		credential.ID,
		credential.WorkspaceID,
		credential.Provider,
		credential.AccountLabel,
		append([]byte(nil), credential.SealedToken...),
		credential.Fingerprint,
		credential.LastValidatedAt,
		credential.LastValidationStatus,
		credential.LastValidationNote,
		credential.CreatedAt,
		credential.UpdatedAt,
	}
}

func runtimeValues(runtime db.AgentRuntime) []any {
	return []any{
		runtime.ID,
		runtime.WorkspaceID,
		runtime.DaemonID,
		runtime.Name,
		runtime.RuntimeMode,
		runtime.Provider,
		runtime.Status,
		runtime.DeviceInfo,
		append([]byte(nil), runtime.Metadata...),
		runtime.LastSeenAt,
		runtime.CreatedAt,
		runtime.UpdatedAt,
		runtime.OwnerID,
		runtime.LegacyDaemonID,
		runtime.Visibility,
		runtime.ProfileID,
		runtime.CustomName,
	}
}

func snapshotValues(snapshot db.ProviderLimitSnapshot) []any {
	return []any{
		snapshot.ID,
		snapshot.WorkspaceID,
		snapshot.RuntimeID,
		snapshot.DaemonID,
		snapshot.Provider,
		snapshot.AccountKey,
		snapshot.AccountLabel,
		snapshot.CheckedAt,
		snapshot.Status,
		snapshot.SourceKind,
		snapshot.SourceConfidence,
		snapshot.SourceFreshnessSeconds,
		append([]byte(nil), snapshot.Buckets...),
		snapshot.ErrorNote,
		snapshot.ContentHash,
		snapshot.CreatedAt,
	}
}

func appendCredential(input []db.ProviderCredential, value db.ProviderCredential) []db.ProviderCredential {
	return append(append([]db.ProviderCredential(nil), input...), value)
}

func appendSnapshot(input []db.ProviderLimitSnapshot, value db.ProviderLimitSnapshot) []db.ProviderLimitSnapshot {
	return append(append([]db.ProviderLimitSnapshot(nil), input...), value)
}

func appendCopy(input []string, value string) []string {
	return append(append([]string(nil), input...), value)
}

func commandTag(operation string, affected int64) string {
	return operation + " " + string(rune('0'+affected))
}
