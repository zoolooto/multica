package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// TestSetAgentRuntimeSkillMode covers the tri-state override introduced for
// GH #10776: "on" and "off" are stored explicitly, "inherit" removes the
// entry, and the agent response keeps the pre-tri-state disabled list while
// also exposing the full override set.
func TestSetAgentRuntimeSkillMode(t *testing.T) {
	runtimeID := dbfx.Runtime(t, "Runtime Skill Mode "+t.Name(), testutil.Cols{
		"runtime_mode": "local",
		"provider":     "claude",
	})
	agentID := dbfx.Agent(t, "Runtime Skill Mode "+t.Name(), runtimeID, testutil.Cols{
		"runtime_mode": "local",
	})

	setMode := func(want int, payload map[string]any) {
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
		req := withURLParam(
			newRequest("PUT", "/api/agents/"+agentID+"/runtime-skills/enabled", body),
			"id", agentID,
		)
		testutil.Call(t, testHandler.SetAgentRuntimeSkillEnabled, req).Want(want)
	}

	loadAgent := func() ([]DisabledRuntimeSkill, AgentResponse) {
		t.Helper()
		row, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
		if err != nil {
			t.Fatalf("load agent: %v", err)
		}
		return normalizeRuntimeSkillOverrides(row.DisabledRuntimeSkills), testHandler.agentToResponse(row)
	}

	setMode(204, map[string]any{"mode": "on"})
	overrides, resp := loadAgent()
	if len(overrides) != 1 || overrides[0].Mode != RuntimeSkillModeOn || overrides[0].Key != "review" {
		t.Fatalf("forced-on override not persisted: %+v", overrides)
	}
	// The pre-tri-state list must not advertise a forced-on entry as disabled.
	if len(resp.DisabledRuntimeSkills) != 0 {
		t.Fatalf("disabled_runtime_skills must stay empty for a forced-on entry, got %+v", resp.DisabledRuntimeSkills)
	}
	if len(resp.RuntimeSkillOverrides) != 1 || resp.RuntimeSkillOverrides[0].Mode != RuntimeSkillModeOn {
		t.Fatalf("runtime_skill_overrides = %+v, want one on entry", resp.RuntimeSkillOverrides)
	}

	setMode(204, map[string]any{"mode": "off"})
	overrides, resp = loadAgent()
	if len(overrides) != 1 || overrides[0].Mode != RuntimeSkillModeOff {
		t.Fatalf("forced-off override not persisted: %+v", overrides)
	}
	if len(resp.DisabledRuntimeSkills) != 1 || len(resp.RuntimeSkillOverrides) != 1 {
		t.Fatalf("forced-off entry must appear in both views: disabled=%+v overrides=%+v",
			resp.DisabledRuntimeSkills, resp.RuntimeSkillOverrides)
	}

	setMode(204, map[string]any{"mode": "inherit"})
	if overrides, _ = loadAgent(); len(overrides) != 0 {
		t.Fatalf("inherit must remove the override, got %+v", overrides)
	}

	setMode(400, map[string]any{"mode": "sometimes"})
}
