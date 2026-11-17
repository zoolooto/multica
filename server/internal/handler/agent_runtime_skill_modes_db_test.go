package handler

import (
	"context"
	"net/http/httptest"
	"testing"
)

// TestSetAgentRuntimeSkillMode covers the tri-state override introduced for
// GH #10776: "on" and "off" are stored explicitly, "inherit" removes the
// entry, and the agent response keeps the pre-tri-state disabled list while
// also exposing the full override set.
func TestSetAgentRuntimeSkillMode(t *testing.T) {
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, permission_mode, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 'private', 'private', 1, $4)
		RETURNING id
	`, testWorkspaceID, "Runtime Skill Mode "+t.Name(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	setMode := func(payload map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		body := map[string]any{
			"runtime_id": runtimeID,
			"root":       "provider",
			"key":        "review",
			"name":       "Review Helper",
		}
		for key, value := range payload {
			body[key] = value
		}
		w := httptest.NewRecorder()
		req := newRequest("PUT", "/api/agents/"+agentID+"/runtime-skills/enabled", body)
		req = withURLParam(req, "id", agentID)
		testHandler.SetAgentRuntimeSkillEnabled(w, req)
		return w
	}

	loadOverrides := func() []DisabledRuntimeSkill {
		t.Helper()
		row, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
		if err != nil {
			t.Fatalf("load agent: %v", err)
		}
		return normalizeRuntimeSkillOverrides(row.DisabledRuntimeSkills)
	}

	if w := setMode(map[string]any{"mode": "on"}); w.Code != 204 {
		t.Fatalf("force on: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	overrides := loadOverrides()
	if len(overrides) != 1 || overrides[0].Mode != RuntimeSkillModeOn || overrides[0].Key != "review" {
		t.Fatalf("forced-on override not persisted: %+v", overrides)
	}

	// The pre-tri-state list must not advertise a forced-on entry as disabled.
	row, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	resp := testHandler.agentToResponse(row)
	if len(resp.DisabledRuntimeSkills) != 0 {
		t.Fatalf("disabled_runtime_skills must stay empty for a forced-on entry, got %+v", resp.DisabledRuntimeSkills)
	}
	if len(resp.RuntimeSkillOverrides) != 1 || resp.RuntimeSkillOverrides[0].Mode != RuntimeSkillModeOn {
		t.Fatalf("runtime_skill_overrides = %+v, want one on entry", resp.RuntimeSkillOverrides)
	}

	if w := setMode(map[string]any{"mode": "off"}); w.Code != 204 {
		t.Fatalf("force off: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	overrides = loadOverrides()
	if len(overrides) != 1 || overrides[0].Mode != RuntimeSkillModeOff {
		t.Fatalf("forced-off override not persisted: %+v", overrides)
	}
	row, err = testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	resp = testHandler.agentToResponse(row)
	if len(resp.DisabledRuntimeSkills) != 1 || len(resp.RuntimeSkillOverrides) != 1 {
		t.Fatalf("forced-off entry must appear in both views: disabled=%+v overrides=%+v",
			resp.DisabledRuntimeSkills, resp.RuntimeSkillOverrides)
	}

	if w := setMode(map[string]any{"mode": "inherit"}); w.Code != 204 {
		t.Fatalf("inherit: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if overrides = loadOverrides(); len(overrides) != 0 {
		t.Fatalf("inherit must remove the override, got %+v", overrides)
	}

	if w := setMode(map[string]any{"mode": "sometimes"}); w.Code != 400 {
		t.Fatalf("unknown mode: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
