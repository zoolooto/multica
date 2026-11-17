package handler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/instanceadmin"
	"github.com/multica-ai/multica/server/internal/testutil"
)

// TestInstanceStoreAggregatesAcrossWorkspaces exercises the read model
// directly, with an explicit cutoff, because the HTTP handler always derives
// its cutoff from the wall clock and the timezone day boundary can only be
// asserted against a pinned instant.
//
// The suite shares one database with packages running in parallel, so every
// assertion is about this test's own rows (one provider marker, one model
// name): the instance-wide totals can include another test's fixtures.
func TestInstanceStoreAggregatesAcrossWorkspaces(t *testing.T) {
	ctx := context.Background()

	otherUser := dbfx.User(t, "Instance Store Other", "instance-store-other@multica.ai")
	otherWorkspace := dbfx.Workspace(t, "Instance Store Other", fmt.Sprintf("instance-store-other-%d", time.Now().UnixNano()))
	dbfx.Member(t, otherWorkspace, otherUser, "owner")
	otherRuntime := dbfx.Runtime(t, "instance-store-runtime", testutil.Cols{
		"workspace_id": otherWorkspace,
		"owner_id":     otherUser,
	})
	otherAgent := dbfx.Agent(t, "instance-store-agent", otherRuntime, testutil.Cols{
		"workspace_id": otherWorkspace,
		"owner_id":     otherUser,
	})
	// The two exclusions the list documents: archived agents and the
	// kind='system' carriers that back squads, not user-facing agents.
	archivedAgent := dbfx.Agent(t, "instance-store-archived", testRuntimeID, testutil.Cols{
		"archived_at": testutil.Raw("now()"),
	})
	systemAgent := dbfx.Agent(t, "instance-store-system", testRuntimeID, testutil.Cols{
		"kind": "system",
	})

	var homeAgent string
	if err := testPool.QueryRow(ctx, `
		SELECT id::text FROM agent
		WHERE workspace_id = $1
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, testWorkspaceID).Scan(&homeAgent); err != nil {
		t.Fatalf("look up a test-workspace agent: %v", err)
	}

	const provider = "instance_store_provider"
	const model = "instance-store-model"
	const staleModel = "instance-store-stale-model"
	// 2020-01-01 20:00 UTC is already 2020-01-02 in Asia/Shanghai, which is
	// what makes the timezone assertion meaningful.
	inWindow := time.Date(2020, 1, 1, 20, 0, 0, 0, time.UTC)
	beforeWindow := time.Date(2019, 12, 31, 12, 0, 0, 0, time.UTC)

	insertUsage := `
		INSERT INTO task_usage_hourly (
			bucket_hour, workspace_id, runtime_id, agent_id, project_id,
			provider, model, input_tokens, output_tokens,
			cache_read_tokens, cache_write_tokens, task_count, event_count,
			cost_usd_ticks
		)
		VALUES ($1, $2, $3, $4, NULL, $5, $6, $7, $8, 0, 0, 1, 1, $9)
	`
	dbfx.Exec(t, insertUsage, inWindow, testWorkspaceID, testRuntimeID, homeAgent, provider, model, 100, 10, 300)
	dbfx.Exec(t, insertUsage, inWindow, otherWorkspace, otherRuntime, otherAgent, provider, model, 200, 20, 400)
	dbfx.Exec(t, insertUsage, beforeWindow, testWorkspaceID, testRuntimeID, homeAgent, provider, staleModel, 999, 999, 999)
	dbfx.Cleanup(t, `DELETE FROM task_usage_hourly WHERE provider = $1`, provider)

	since := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	store := instanceadmin.NewStore(testPool)

	utcOverview, err := store.Load(ctx, since, "UTC")
	if err != nil {
		t.Fatalf("load overview in UTC: %v", err)
	}
	shanghaiOverview, err := store.Load(ctx, since, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("load overview in Asia/Shanghai: %v", err)
	}

	t.Run("usage is grouped per workspace inside the window", func(t *testing.T) {
		type bucket struct {
			date        string
			inputTokens int64
			cost        int64
		}
		seen := map[string]bucket{}
		for _, row := range utcOverview.Usage {
			if row.Provider != provider || row.Model != model {
				continue
			}
			seen[row.WorkspaceID] = bucket{date: row.Date, inputTokens: row.InputTokens, cost: row.CostUSDTicks}
		}
		home, ok := seen[testWorkspaceID]
		if !ok {
			t.Fatalf("test workspace missing from usage: %+v", seen)
		}
		if home.inputTokens != 100 || home.cost != 300 {
			t.Errorf("test workspace usage = tokens:%d cost:%d, want 100/300", home.inputTokens, home.cost)
		}
		other, ok := seen[otherWorkspace]
		if !ok {
			t.Fatalf("second workspace missing from usage: %+v", seen)
		}
		if other.inputTokens != 200 || other.cost != 400 {
			t.Errorf("second workspace usage = tokens:%d cost:%d, want 200/400", other.inputTokens, other.cost)
		}
		for _, row := range utcOverview.Usage {
			if row.Model == staleModel {
				t.Errorf("row before the cutoff leaked into the window: %+v", row)
			}
		}
	})

	t.Run("timezone decides the calendar day", func(t *testing.T) {
		var utcDay, shanghaiDay string
		for _, row := range utcOverview.Usage {
			if row.WorkspaceID == testWorkspaceID && row.Model == model {
				utcDay = row.Date
			}
		}
		for _, row := range shanghaiOverview.Usage {
			if row.WorkspaceID == testWorkspaceID && row.Model == model {
				shanghaiDay = row.Date
			}
		}
		if utcDay != "2020-01-01" {
			t.Errorf("UTC day = %q, want 2020-01-01", utcDay)
		}
		if shanghaiDay != "2020-01-02" {
			t.Errorf("Asia/Shanghai day = %q, want 2020-01-02", shanghaiDay)
		}
	})

	t.Run("summary is consistent with the lists it returns", func(t *testing.T) {
		overview := utcOverview
		if overview.Summary.WorkspaceCount != len(overview.Workspaces) {
			t.Errorf("workspace_count = %d, len(workspaces) = %d", overview.Summary.WorkspaceCount, len(overview.Workspaces))
		}
		if overview.Summary.UserCount != len(overview.Users) {
			t.Errorf("user_count = %d, len(users) = %d", overview.Summary.UserCount, len(overview.Users))
		}
		if overview.Summary.AgentCount != len(overview.Agents) {
			t.Errorf("agent_count = %d, len(agents) = %d", overview.Summary.AgentCount, len(overview.Agents))
		}
		// The meter is summed from the usage rows; every windowed row this
		// test wrote must be included, and rows from other suites only add.
		if overview.Summary.InputTokens < 300 {
			t.Errorf("summary.input_tokens = %d, want at least this test's 300", overview.Summary.InputTokens)
		}
		if overview.Summary.CostUSDTicks < 700 {
			t.Errorf("summary.cost_usd_ticks = %d, want at least this test's 700", overview.Summary.CostUSDTicks)
		}
	})

	t.Run("workspace rows carry the owner and counts", func(t *testing.T) {
		var found bool
		for _, ws := range utcOverview.Workspaces {
			if ws.ID != otherWorkspace {
				continue
			}
			found = true
			if ws.Owner == nil || ws.Owner.ID != otherUser || ws.Owner.Email != "instance-store-other@multica.ai" {
				t.Errorf("owner = %+v, want the second user", ws.Owner)
			}
			if ws.MemberCount != 1 || ws.AgentCount != 1 || ws.RuntimeCount != 1 {
				t.Errorf("counts = members:%d agents:%d runtimes:%d, want 1/1/1",
					ws.MemberCount, ws.AgentCount, ws.RuntimeCount)
			}
			if ws.LastActivityAt == nil {
				t.Error("last_activity_at is nil for a workspace that was just updated")
			}
		}
		if !found {
			t.Fatalf("workspace %s missing from the instance list", otherWorkspace)
		}
	})

	t.Run("agents exclude archived and system rows", func(t *testing.T) {
		var sawOther, sawArchived, sawSystem bool
		for _, agent := range utcOverview.Agents {
			switch agent.Name {
			case "instance-store-agent":
				sawOther = true
				if agent.WorkspaceID != otherWorkspace || agent.WorkspaceName != "Instance Store Other" {
					t.Errorf("agent workspace = %s/%s, want %s/Instance Store Other",
						agent.WorkspaceID, agent.WorkspaceName, otherWorkspace)
				}
				if agent.Owner == nil || agent.Owner.ID != otherUser {
					t.Errorf("agent owner = %+v, want the second user", agent.Owner)
				}
				if agent.RuntimeID == nil || *agent.RuntimeID != otherRuntime {
					t.Errorf("agent runtime = %v, want %s", agent.RuntimeID, otherRuntime)
				}
				if agent.RuntimeProvider != "handler_test_runtime" || agent.RuntimeStatus != "online" {
					t.Errorf("runtime = %q/%q, want handler_test_runtime/online",
						agent.RuntimeProvider, agent.RuntimeStatus)
				}
			case "instance-store-archived":
				sawArchived = true
			case "instance-store-system":
				sawSystem = true
			}
		}
		if !sawOther {
			t.Fatal("cross-workspace agent missing from the instance list")
		}
		if sawArchived {
			t.Errorf("archived agent %s leaked into the instance list", archivedAgent)
		}
		if sawSystem {
			t.Errorf("system carrier %s leaked into the instance list", systemAgent)
		}
	})
}
