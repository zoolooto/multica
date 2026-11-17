package instanceadmin

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/testutil"
)

var testPool *pgxpool.Pool
var dbfx *testutil.Fixture

// TestMain mirrors the other DB-backed packages: DATABASE_URL, otherwise the
// local default, and a skip when the database is unreachable. Without this the
// store's SQL would only be exercised through the HTTP handler, where the
// cutoff comes from the wall clock and cannot be pinned to a fixed instant.
func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	dbConfigured := dbURL != ""
	if !dbConfigured {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		if dbConfigured {
			fmt.Fprintln(os.Stderr, "Failed to configure instanceadmin test database from DATABASE_URL")
			os.Exit(1)
		}
		fmt.Printf("Skipping tests: could not connect to database: %v\n", err)
		os.Exit(0)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		if dbConfigured {
			fmt.Fprintln(os.Stderr, "Failed to connect to instanceadmin test database configured by DATABASE_URL")
			os.Exit(1)
		}
		fmt.Printf("Skipping tests: database not reachable: %v\n", err)
		os.Exit(0)
	}

	testPool = pool
	dbfx = testutil.New(pool, "", "")

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// TestStoreAggregatesAcrossWorkspaces exercises the read model directly with
// a pinned cutoff: per-workspace grouping, window filtering, token/cost sums,
// owner/count rows, the archived and kind='system' exclusions, and the
// timezone day boundary.
//
// The database is shared with packages running in parallel, so every assertion
// is about this test's own rows (one provider marker, one model name): the
// instance-wide totals can include another test's fixtures.
func TestStoreAggregatesAcrossWorkspaces(t *testing.T) {
	ctx := context.Background()
	unique := time.Now().UnixNano()

	const provider = "instance_store_provider"
	const runtimeProvider = "instance_store_runtime"
	const model = "instance-store-model"
	const staleModel = "instance-store-stale-model"

	homeUser := dbfx.User(t, "Instance Store Home", "instance-store-home@multica.ai")
	homeWorkspace := dbfx.Workspace(t, "Instance Store Home", fmt.Sprintf("instance-store-home-%d", unique))
	dbfx.Member(t, homeWorkspace, homeUser, "owner")
	homeRuntime := dbfx.Runtime(t, "instance-store-home-runtime", testutil.Cols{
		"workspace_id": homeWorkspace,
		"owner_id":     homeUser,
		"provider":     runtimeProvider,
	})
	homeAgent := dbfx.Agent(t, "instance-store-home-agent", homeRuntime, testutil.Cols{
		"workspace_id": homeWorkspace,
		"owner_id":     homeUser,
	})

	otherUser := dbfx.User(t, "Instance Store Other", "instance-store-other@multica.ai")
	otherWorkspace := dbfx.Workspace(t, "Instance Store Other", fmt.Sprintf("instance-store-other-%d", unique))
	dbfx.Member(t, otherWorkspace, otherUser, "owner")
	otherRuntime := dbfx.Runtime(t, "instance-store-other-runtime", testutil.Cols{
		"workspace_id": otherWorkspace,
		"owner_id":     otherUser,
		"provider":     runtimeProvider,
	})
	otherAgent := dbfx.Agent(t, "instance-store-other-agent", otherRuntime, testutil.Cols{
		"workspace_id": otherWorkspace,
		"owner_id":     otherUser,
	})

	// The two exclusions the list documents: archived agents and the
	// kind='system' carriers that back squads, not user-facing agents.
	archivedAgent := dbfx.Agent(t, "instance-store-archived", homeRuntime, testutil.Cols{
		"workspace_id": homeWorkspace,
		"owner_id":     homeUser,
		"archived_at":  testutil.Raw("now()"),
	})
	systemAgent := dbfx.Agent(t, "instance-store-system", homeRuntime, testutil.Cols{
		"workspace_id": homeWorkspace,
		"owner_id":     homeUser,
		"kind":         "system",
	})

	// 2020-01-01 20:00 UTC is already 2020-01-02 in Asia/Shanghai, which is
	// what makes the timezone assertion meaningful.
	inWindow := time.Date(2020, 1, 1, 20, 0, 0, 0, time.UTC)
	beforeWindow := time.Date(2019, 12, 31, 12, 0, 0, 0, time.UTC)

	usageRow := func(bucket time.Time, workspace, runtime, agent, modelName string, input, output, cost int64) testutil.Cols {
		return testutil.Cols{
			"bucket_hour":        bucket,
			"workspace_id":       workspace,
			"runtime_id":         runtime,
			"agent_id":           agent,
			"project_id":         nil,
			"provider":           provider,
			"model":              modelName,
			"input_tokens":       input,
			"output_tokens":      output,
			"cache_read_tokens":  0,
			"cache_write_tokens": 0,
			// Half of the input is priced by the provider, half still needs the
			// static rate table; the report must carry both halves separately.
			"cost_usd_ticks":              cost,
			"uncosted_input_tokens":       input / 2,
			"uncosted_output_tokens":      output / 2,
			"uncosted_cache_read_tokens":  0,
			"uncosted_cache_write_tokens": 0,
			"task_count":                  1,
			"event_count":                 1,
		}
	}
	// task_usage_hourly has no `id` column, so the fixture needs an explicit
	// teardown key (the provider marker).
	dbfx.InsertNoID(t, "task_usage_hourly", usageRow(inWindow, homeWorkspace, homeRuntime, homeAgent, model, 100, 10, 300), "provider = $1", provider)
	dbfx.InsertNoID(t, "task_usage_hourly", usageRow(inWindow, otherWorkspace, otherRuntime, otherAgent, model, 200, 20, 400), "provider = $1", provider)
	dbfx.InsertNoID(t, "task_usage_hourly", usageRow(beforeWindow, homeWorkspace, homeRuntime, homeAgent, staleModel, 999, 999, 999), "provider = $1", provider)

	since := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	store := NewStore(testPool)

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
		home, ok := seen[homeWorkspace]
		if !ok {
			t.Fatalf("home workspace missing from usage: %+v", seen)
		}
		if home.inputTokens != 100 || home.cost != 300 {
			t.Errorf("home workspace usage = tokens:%d cost:%d, want 100/300", home.inputTokens, home.cost)
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
			if row.WorkspaceID == homeWorkspace && row.Model == model && row.UncostedInputTokens != 50 {
				t.Errorf("uncosted_input_tokens = %d, want 50", row.UncostedInputTokens)
			}
		}
	})

	t.Run("an empty window still returns arrays", func(t *testing.T) {
		// The JSON contract is a list, never null, even when a section has no
		// rows: a consumer must not have to null-check. A cutoff far in the
		// future empties the window no matter what else is in the database.
		future, err := store.Load(ctx, time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC), "UTC")
		if err != nil {
			t.Fatalf("load an empty window: %v", err)
		}
		if future.Usage == nil {
			t.Error("usage is nil for an empty window, want an empty slice")
		}
		if len(future.Usage) != 0 {
			t.Errorf("usage has %d rows in a window that ends before any data", len(future.Usage))
		}
		if future.Workspaces == nil || future.Users == nil || future.Agents == nil {
			t.Error("a list section is nil, want an empty slice")
		}
	})

	t.Run("timezone decides the calendar day", func(t *testing.T) {
		var utcDay, shanghaiDay string
		for _, row := range utcOverview.Usage {
			if row.WorkspaceID == homeWorkspace && row.Model == model {
				utcDay = row.Date
			}
		}
		for _, row := range shanghaiOverview.Usage {
			if row.WorkspaceID == homeWorkspace && row.Model == model {
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
		if overview.Summary.UncostedInputTokens < 150 {
			t.Errorf("summary.uncosted_input_tokens = %d, want at least this test's 150", overview.Summary.UncostedInputTokens)
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
			case "instance-store-other-agent":
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
				if agent.RuntimeProvider != runtimeProvider || agent.RuntimeStatus != "online" {
					t.Errorf("runtime = %q/%q, want %s/online",
						agent.RuntimeProvider, agent.RuntimeStatus, runtimeProvider)
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
