package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/instanceadmin"
)

// withInstanceAdmins swaps the handler's configured administrator set for one
// test and restores it afterwards. The set is compiled once at boot in
// production (handler.New), so this is the same field a deployment would carry.
func withInstanceAdmins(t *testing.T, emails ...string) {
	t.Helper()
	previous := testHandler.InstanceAdmins
	testHandler.InstanceAdmins = instanceadmin.NewAdmins(emails)
	t.Cleanup(func() { testHandler.InstanceAdmins = previous })
}

func instanceRequest(t *testing.T, method, target string, actorSource string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("X-User-ID", testUserID)
	if actorSource != "" {
		req.Header.Set("X-Actor-Source", actorSource)
	}
	return req, httptest.NewRecorder()
}

func decodeInstanceOverview(t *testing.T, rec *httptest.ResponseRecorder) instanceadmin.Overview {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var overview instanceadmin.Overview
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview: %v; body = %s", err, rec.Body.String())
	}
	return overview
}

// assertListFieldsNotNull guards the JSON contract, not the Go value: a nil
// slice and an empty slice both have length 0, so decoding into Overview cannot
// tell `null` from `[]`. A consumer iterating these lists must never have to
// null-check them.
func assertListFieldsNotNull(t *testing.T, rec *httptest.ResponseRecorder, keys ...string) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw overview: %v", err)
	}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			t.Errorf("%s missing from the response", key)
			continue
		}
		if string(value) == "null" {
			t.Errorf("%s is null, want an array (empty at minimum)", key)
		}
	}
}

func TestInstanceAccessReportsConfiguredAdmin(t *testing.T) {
	t.Run("unconfigured", func(t *testing.T) {
		withInstanceAdmins(t)
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/access", "")
		testHandler.GetInstanceAccess(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body instanceAccessResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.IsInstanceAdmin {
			t.Fatal("is_instance_admin = true without INSTANCE_ADMIN_EMAILS")
		}
	})

	t.Run("listed", func(t *testing.T) {
		withInstanceAdmins(t, handlerTestEmail)
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/access", "")
		testHandler.GetInstanceAccess(rec, req)
		var body instanceAccessResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !body.IsInstanceAdmin {
			t.Fatalf("is_instance_admin = false for %q, want true", handlerTestEmail)
		}
	})

	t.Run("someone else", func(t *testing.T) {
		withInstanceAdmins(t, "ops@example.com")
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/access", "")
		testHandler.GetInstanceAccess(rec, req)
		var body instanceAccessResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.IsInstanceAdmin {
			t.Fatalf("is_instance_admin = true for %q, which is not listed", handlerTestEmail)
		}
	})
}

func TestInstanceOverviewRefusesNonAdmins(t *testing.T) {
	t.Run("unconfigured", func(t *testing.T) {
		withInstanceAdmins(t)
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/overview", "")
		testHandler.GetInstanceOverview(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("not listed", func(t *testing.T) {
		withInstanceAdmins(t, "ops@example.com")
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/overview", "")
		testHandler.GetInstanceOverview(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("machine credential cannot borrow the owner's grant", func(t *testing.T) {
		withInstanceAdmins(t, handlerTestEmail)
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/overview", "task_token")
		testHandler.GetInstanceOverview(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 for a task token; body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestInstanceOverviewDefaultsAndWindow(t *testing.T) {
	withInstanceAdmins(t, handlerTestEmail)

	t.Run("default window", func(t *testing.T) {
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/overview", "")
		testHandler.GetInstanceOverview(rec, req)
		overview := decodeInstanceOverview(t, rec)
		if overview.WindowDays != instanceOverviewDefaultDays {
			t.Fatalf("window_days = %d, want %d", overview.WindowDays, instanceOverviewDefaultDays)
		}
	})

	t.Run("explicit window", func(t *testing.T) {
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/overview?days=7&tz=Asia/Shanghai", "")
		testHandler.GetInstanceOverview(rec, req)
		overview := decodeInstanceOverview(t, rec)
		if overview.WindowDays != 7 {
			t.Fatalf("window_days = %d, want 7", overview.WindowDays)
		}
		if overview.Timezone != "Asia/Shanghai" {
			t.Fatalf("timezone = %q, want Asia/Shanghai", overview.Timezone)
		}
	})

	t.Run("out-of-range falls back", func(t *testing.T) {
		for _, raw := range []string{"0", "-1", "366", "abc"} {
			req, rec := instanceRequest(t, http.MethodGet, "/api/instance/overview?days="+raw, "")
			testHandler.GetInstanceOverview(rec, req)
			overview := decodeInstanceOverview(t, rec)
			if overview.WindowDays != instanceOverviewDefaultDays {
				t.Fatalf("days=%s produced window_days=%d, want %d", raw, overview.WindowDays, instanceOverviewDefaultDays)
			}
		}
	})

	t.Run("invalid timezone falls back to UTC", func(t *testing.T) {
		req, rec := instanceRequest(t, http.MethodGet, "/api/instance/overview?tz=Not/AZone", "")
		testHandler.GetInstanceOverview(rec, req)
		overview := decodeInstanceOverview(t, rec)
		if overview.Timezone != "UTC" {
			t.Fatalf("timezone = %q, want UTC", overview.Timezone)
		}
	})
}

func TestInstanceOverviewContainsInstanceWideRowsAndUsage(t *testing.T) {
	withInstanceAdmins(t, handlerTestEmail)
	ctx := context.Background()

	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_usage_hourly (
			bucket_hour, workspace_id, runtime_id, agent_id, project_id,
			provider, model, input_tokens, output_tokens,
			cache_read_tokens, cache_write_tokens, task_count, event_count,
			cost_usd_ticks
		)
		VALUES (date_trunc('hour', now()), $1, $2,
			(SELECT id FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1),
			NULL, 'Anthropic', 'instance-admin-test-model', 1200, 300, 10, 20, 3, 3, 5000)
	`, testWorkspaceID, testRuntimeID); err != nil {
		t.Fatalf("seed task_usage_hourly: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM task_usage_hourly WHERE workspace_id = $1 AND model = 'instance-admin-test-model'`, testWorkspaceID)
	})

	var clientUsageInstall string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO client_usage_daily (
			user_id, client_type, install_id, activity_date, workspace_id,
			client_version, os, first_active_at, last_active_at
		)
		VALUES ($1, 'web', gen_random_uuid(), CURRENT_DATE, $2, 'test', 'linux', now(), now())
		RETURNING install_id::text
	`, testUserID, testWorkspaceID).Scan(&clientUsageInstall); err != nil {
		t.Fatalf("seed client_usage_daily: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM client_usage_daily WHERE install_id = $1`, clientUsageInstall)
	})

	req, rec := instanceRequest(t, http.MethodGet, "/api/instance/overview?days=1&tz=UTC", "")
	testHandler.GetInstanceOverview(rec, req)
	overview := decodeInstanceOverview(t, rec)
	assertListFieldsNotNull(t, rec, "workspaces", "users", "agents", "usage")

	if overview.GeneratedAt == "" {
		t.Error("generated_at is empty")
	}
	if overview.Summary.WorkspaceCount != len(overview.Workspaces) {
		t.Errorf("summary.workspace_count = %d, len(workspaces) = %d", overview.Summary.WorkspaceCount, len(overview.Workspaces))
	}
	if overview.Summary.UserCount != len(overview.Users) {
		t.Errorf("summary.user_count = %d, len(users) = %d", overview.Summary.UserCount, len(overview.Users))
	}
	if overview.Summary.AgentCount != len(overview.Agents) {
		t.Errorf("summary.agent_count = %d, len(agents) = %d", overview.Summary.AgentCount, len(overview.Agents))
	}

	var sawWorkspace bool
	for _, ws := range overview.Workspaces {
		if ws.ID != testWorkspaceID {
			continue
		}
		sawWorkspace = true
		if ws.Owner == nil || ws.Owner.ID != testUserID {
			t.Fatalf("workspace owner = %+v, want test user %s", ws.Owner, testUserID)
		}
		if ws.MemberCount < 1 || ws.AgentCount < 1 || ws.RuntimeCount < 1 {
			t.Errorf("workspace counts = members:%d agents:%d runtimes:%d, want >= 1 each",
				ws.MemberCount, ws.AgentCount, ws.RuntimeCount)
		}
	}
	if !sawWorkspace {
		t.Fatalf("workspace %s missing from instance overview", testWorkspaceID)
	}

	var sawUser bool
	for _, user := range overview.Users {
		if user.ID != testUserID {
			continue
		}
		sawUser = true
		if user.LastActiveAt == nil {
			t.Error("user last_active_at is nil despite a client_usage_daily row")
		}
		var sawMembership bool
		for _, membership := range user.Workspaces {
			if membership.ID == testWorkspaceID && membership.Role == "owner" {
				sawMembership = true
			}
		}
		if !sawMembership {
			t.Errorf("user memberships = %+v, want the test workspace with role owner", user.Workspaces)
		}
	}
	if !sawUser {
		t.Fatalf("user %s missing from instance overview", testUserID)
	}

	var sawUsage bool
	for _, row := range overview.Usage {
		if row.WorkspaceID != testWorkspaceID || row.Model != "instance-admin-test-model" {
			continue
		}
		sawUsage = true
		if row.Provider != "anthropic" {
			t.Errorf("usage provider = %q, want lowercased anthropic", row.Provider)
		}
		if row.InputTokens != 1200 || row.OutputTokens != 300 || row.CostUSDTicks != 5000 || row.TaskCount != 3 {
			t.Errorf("usage row = %+v, want 1200/300/5000/3", row)
		}
		if row.Date != time.Now().UTC().Format("2006-01-02") {
			t.Errorf("usage date = %q, want today in UTC", row.Date)
		}
	}
	if !sawUsage {
		t.Fatalf("seeded usage row missing from overview")
	}
	if overview.Summary.InputTokens < 1200 {
		t.Errorf("summary.input_tokens = %d, want >= 1200", overview.Summary.InputTokens)
	}
	if overview.Summary.CostUSDTicks < 5000 {
		t.Errorf("summary.cost_usd_ticks = %d, want >= 5000", overview.Summary.CostUSDTicks)
	}
}
