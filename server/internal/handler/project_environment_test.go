package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type projectEnvironmentTestResponse struct {
	ID                string            `json:"id"`
	ProjectID         string            `json:"project_id"`
	WorkspaceID       string            `json:"workspace_id"`
	Name              string            `json:"name"`
	Description       *string           `json:"description"`
	Config            map[string]any    `json:"config"`
	Secrets           map[string]string `json:"secrets"`
	AllowedRuntimeIDs []string          `json:"allowed_runtime_ids"`
	CreatedBy         *string           `json:"created_by"`
	CreatedAt         string            `json:"created_at"`
	UpdatedAt         string            `json:"updated_at"`
}

func TestProjectEnvironmentLifecycleMasksSecretsAndPreservesSentinel(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectEnvironmentTestProject(t, "Environment lifecycle")
	description := "staging services"

	w := httptest.NewRecorder()
	req := newProjectEnvironmentRequest(http.MethodPost, project.ID, "", map[string]any{
		"name":                "staging",
		"description":         description,
		"config":              map[string]any{"url": "https://staging.example.test", "port": 443},
		"secrets":             map[string]string{"LOGIN": "admin", "PASSWORD": "initial-secret"},
		"allowed_runtime_ids": []string{testRuntimeID},
	})
	testHandler.CreateProjectEnvironment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProjectEnvironment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	created := decodeProjectEnvironmentResponse(t, w)
	if created.Name != "staging" || created.ProjectID != project.ID || created.WorkspaceID != testWorkspaceID {
		t.Fatalf("created environment mismatch: %+v", created)
	}
	if created.Description == nil || *created.Description != description {
		t.Fatalf("description = %v, want %q", created.Description, description)
	}
	if got := created.Secrets; !reflect.DeepEqual(got, map[string]string{"LOGIN": envSentinel, "PASSWORD": envSentinel}) {
		t.Fatalf("created secrets must be masked, got %+v", got)
	}
	if !reflect.DeepEqual(created.AllowedRuntimeIDs, []string{testRuntimeID}) {
		t.Fatalf("allowed_runtime_ids = %+v, want [%s]", created.AllowedRuntimeIDs, testRuntimeID)
	}

	w = httptest.NewRecorder()
	req = newProjectEnvironmentRequest(http.MethodGet, project.ID, "", nil)
	testHandler.ListProjectEnvironments(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListProjectEnvironments: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Environments []projectEnvironmentTestResponse `json:"environments"`
		Total        int                              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Total != 1 || len(listResp.Environments) != 1 {
		t.Fatalf("list returned %+v, want one environment", listResp)
	}
	if got := listResp.Environments[0].Secrets["PASSWORD"]; got != envSentinel {
		t.Fatalf("list must mask PASSWORD, got %q", got)
	}

	w = httptest.NewRecorder()
	req = newProjectEnvironmentRequest(http.MethodPut, project.ID, created.ID, map[string]any{
		"name":                "staging",
		"description":         "rotated services",
		"config":              map[string]any{"url": "https://staging.example.test", "port": 8443},
		"secrets":             map[string]string{"LOGIN": envSentinel, "PASSWORD": "rotated-secret", "PHANTOM": envSentinel},
		"allowed_runtime_ids": []string{},
	})
	testHandler.UpdateProjectEnvironment(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProjectEnvironment: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	updated := decodeProjectEnvironmentResponse(t, w)
	if got := updated.Secrets; !reflect.DeepEqual(got, map[string]string{"LOGIN": envSentinel, "PASSWORD": envSentinel}) {
		t.Fatalf("updated secrets must stay masked and omit PHANTOM, got %+v", got)
	}
	if len(updated.AllowedRuntimeIDs) != 0 {
		t.Fatalf("allowed runtime ids should be cleared, got %+v", updated.AllowedRuntimeIDs)
	}

	var stored string
	if err := testPool.QueryRow(context.Background(), `SELECT secrets::text FROM project_environment WHERE id = $1`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored secrets: %v", err)
	}
	var storedSecrets map[string]string
	if err := json.Unmarshal([]byte(stored), &storedSecrets); err != nil {
		t.Fatalf("decode stored secrets: %v", err)
	}
	wantStored := map[string]string{"LOGIN": "admin", "PASSWORD": "rotated-secret"}
	if !reflect.DeepEqual(storedSecrets, wantStored) {
		t.Fatalf("stored secrets = %+v, want %+v", storedSecrets, wantStored)
	}

	w = httptest.NewRecorder()
	req = newProjectEnvironmentRequest(http.MethodDelete, project.ID, created.ID, nil)
	testHandler.DeleteProjectEnvironment(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteProjectEnvironment: expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProjectEnvironmentRevealSucceedsOnlyAfterAuditAndNeverAuditsValues(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectEnvironmentTestProject(t, "Environment reveal")
	env := createProjectEnvironmentViaHandler(t, project.ID, "revealable", map[string]string{
		"TOKEN":    "plain-token",
		"PASSWORD": "plain-password",
	})

	w := httptest.NewRecorder()
	req := newProjectEnvironmentRequest(http.MethodGet, project.ID, env.ID, nil)
	testHandler.GetProjectEnvironmentReveal(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetProjectEnvironmentReveal: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var revealed struct {
		ID      string            `json:"id"`
		Secrets map[string]string `json:"secrets"`
	}
	if err := json.NewDecoder(w.Body).Decode(&revealed); err != nil {
		t.Fatalf("decode reveal: %v", err)
	}
	if !reflect.DeepEqual(revealed.Secrets, map[string]string{"TOKEN": "plain-token", "PASSWORD": "plain-password"}) {
		t.Fatalf("revealed secrets mismatch: %+v", revealed.Secrets)
	}

	var details string
	if err := testPool.QueryRow(context.Background(), `
		SELECT details::text FROM activity_log
		WHERE workspace_id = $1 AND action = 'project_environment_revealed'
		  AND details->>'environment_id' = $2
		ORDER BY created_at DESC LIMIT 1
	`, testWorkspaceID, env.ID).Scan(&details); err != nil {
		t.Fatalf("expected project_environment_revealed audit row: %v", err)
	}
	for _, want := range []string{"TOKEN", "PASSWORD"} {
		if !strings.Contains(details, want) {
			t.Fatalf("audit details missing key %q: %s", want, details)
		}
	}
	for _, leak := range []string{"plain-token", "plain-password"} {
		if strings.Contains(details, leak) {
			t.Fatalf("audit details leaked secret value %q: %s", leak, details)
		}
	}
}

func TestProjectEnvironmentRevealFailsClosedWhenAuditWriteFails(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectEnvironmentTestProject(t, "Environment reveal audit failure")
	env := createProjectEnvironmentViaHandler(t, project.ID, "audit-failure", map[string]string{"TOKEN": "do-not-return"})
	installProjectEnvironmentAuditFailureTrigger(t)

	w := httptest.NewRecorder()
	req := newProjectEnvironmentRequest(http.MethodGet, project.ID, env.ID, nil)
	testHandler.GetProjectEnvironmentReveal(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GetProjectEnvironmentReveal with broken audit: expected 500, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "do-not-return") {
		t.Fatalf("failed reveal response leaked secret: %s", w.Body.String())
	}
}

func TestProjectEnvironmentRejectsCrossWorkspaceAndAgentActors(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectEnvironmentTestProject(t, "Environment security")
	env := createProjectEnvironmentViaHandler(t, project.ID, "security", map[string]string{"TOKEN": "hidden"})

	w := httptest.NewRecorder()
	req := newProjectEnvironmentRequest(http.MethodGet, project.ID, "", nil)
	req.Header.Set("X-Workspace-ID", "00000000-0000-0000-0000-000000000000")
	testHandler.ListProjectEnvironments(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace list: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	hostAgentID := createHandlerTestAgent(t, "environment-host-agent", nil)
	hostTaskID := createHandlerTestTaskForAgent(t, hostAgentID)
	cases := []struct {
		name   string
		method string
		envID  string
		body   any
		fn     func(http.ResponseWriter, *http.Request)
	}{
		{"list", http.MethodGet, "", nil, testHandler.ListProjectEnvironments},
		{"create", http.MethodPost, "", map[string]any{"name": "agent-write", "config": map[string]any{}, "secrets": map[string]string{}}, testHandler.CreateProjectEnvironment},
		{"update", http.MethodPut, env.ID, map[string]any{"name": "agent-update", "config": map[string]any{}, "secrets": map[string]string{}}, testHandler.UpdateProjectEnvironment},
		{"delete", http.MethodDelete, env.ID, nil, testHandler.DeleteProjectEnvironment},
		{"reveal", http.MethodGet, env.ID, nil, testHandler.GetProjectEnvironmentReveal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newProjectEnvironmentRequest(tc.method, project.ID, tc.envID, tc.body)
			req.Header.Set("X-Agent-ID", hostAgentID)
			req.Header.Set("X-Task-ID", hostTaskID)
			tc.fn(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 for agent actor, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestProjectEnvironmentPlainMemberCannotManage(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	memberID := createProjectPermissionTestMember(t, "member")
	project := createProjectEnvironmentTestProject(t, "Environment member forbidden")

	w := httptest.NewRecorder()
	req := newProjectEnvironmentRequest(http.MethodPost, project.ID, "", map[string]any{
		"name":    "member-write",
		"config":  map[string]any{},
		"secrets": map[string]string{},
	})
	req.Header.Set("X-User-ID", memberID)
	testHandler.CreateProjectEnvironment(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("plain member create: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func newProjectEnvironmentRequest(method, projectID, envID string, body any) *http.Request {
	path := "/api/projects/" + projectID + "/environments"
	if envID != "" {
		path += "/" + envID
	}
	req := newRequest(method, path, body)
	if envID == "" {
		return withURLParam(req, "id", projectID)
	}
	return withURLParams(req, "id", projectID, "envId", envID)
}

func createProjectEnvironmentTestProject(t *testing.T, title string) ProjectResponse {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": title})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, project.ID)
	})
	return project
}

func createProjectEnvironmentViaHandler(t *testing.T, projectID, name string, secrets map[string]string) projectEnvironmentTestResponse {
	t.Helper()

	w := httptest.NewRecorder()
	req := newProjectEnvironmentRequest(http.MethodPost, projectID, "", map[string]any{
		"name":    name,
		"config":  map[string]any{"url": "https://" + name + ".example.test"},
		"secrets": secrets,
	})
	testHandler.CreateProjectEnvironment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProjectEnvironment(%s): expected 201, got %d: %s", name, w.Code, w.Body.String())
	}
	return decodeProjectEnvironmentResponse(t, w)
}

func decodeProjectEnvironmentResponse(t *testing.T, w *httptest.ResponseRecorder) projectEnvironmentTestResponse {
	t.Helper()

	var resp projectEnvironmentTestResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode project environment response: %v", err)
	}
	return resp
}

func installProjectEnvironmentAuditFailureTrigger(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
CREATE OR REPLACE FUNCTION test_fail_project_environment_reveal_audit()
RETURNS trigger AS $$
BEGIN
  IF NEW.action = 'project_environment_revealed' THEN
    RAISE EXCEPTION 'forced project environment reveal audit failure';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS test_project_environment_reveal_audit_failure ON activity_log;
CREATE TRIGGER test_project_environment_reveal_audit_failure
BEFORE INSERT ON activity_log
FOR EACH ROW EXECUTE FUNCTION test_fail_project_environment_reveal_audit();
`); err != nil {
		t.Fatalf("install audit failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
DROP TRIGGER IF EXISTS test_project_environment_reveal_audit_failure ON activity_log;
DROP FUNCTION IF EXISTS test_fail_project_environment_reveal_audit();
`)
	})
}
