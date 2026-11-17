package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/instanceadmin"
	"github.com/multica-ai/multica/server/internal/testutil"
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

	var seededAgent string
	if err := testPool.QueryRow(ctx, `
		SELECT id::text FROM agent
		WHERE workspace_id = $1
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, testWorkspaceID).Scan(&seededAgent); err != nil {
		t.Fatalf("look up a test-workspace agent: %v", err)
	}
	// task_usage_hourly and client_usage_daily have no `id` column, so the
	// fixture writes them through InsertNoID with an explicit teardown key.
	dbfx.InsertNoID(t, "task_usage_hourly", testutil.Cols{
		"bucket_hour":        testutil.Raw("date_trunc('hour', now())"),
		"workspace_id":       testWorkspaceID,
		"runtime_id":         testRuntimeID,
		"agent_id":           seededAgent,
		"project_id":         nil,
		"provider":           "Anthropic",
		"model":              "instance-admin-test-model",
		"input_tokens":       1200,
		"output_tokens":      300,
		"cache_read_tokens":  10,
		"cache_write_tokens": 20,
		"task_count":         3,
		"event_count":        3,
		"cost_usd_ticks":     5000,
	}, "workspace_id = $1 AND model = $2", testWorkspaceID, "instance-admin-test-model")

	clientUsageInstall := uuid.NewString()
	dbfx.InsertNoID(t, "client_usage_daily", testutil.Cols{
		"user_id":         testUserID,
		"client_type":     "web",
		"install_id":      clientUsageInstall,
		"activity_date":   testutil.Raw("CURRENT_DATE"),
		"workspace_id":    testWorkspaceID,
		"client_version":  "test",
		"os":              "linux",
		"first_active_at": testutil.Raw("now()"),
		"last_active_at":  testutil.Raw("now()"),
	}, "install_id = $1", clientUsageInstall)

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
