package instanceadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

// Querier is the subset of the pgx pool the read model needs. The handler
// passes its own executor, so the store adds no second connection pool.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store reads the instance-wide model straight from the tables that already
// own the facts. No rollup table was added for this surface, and no migration
// was needed for it.
//
// Cost note: the rollup tables are indexed by workspace_id / runtime_id, so an
// instance report that drops the workspace key scans them (the window is
// covered, the key is not). On a busy instance the windowed usage query is the
// most expensive statement here. That is accepted for a read-only operator
// view on a self-hosted deployment — a bucket_hour-leading index would buy
// little on tables this size and every migration is a version-bump operators
// have to run. If the surface ever grows a live dashboard, add that index
// rather than caching the response.
type Store struct {
	db Querier
}

func NewStore(db Querier) *Store {
	return &Store{db: db}
}

// Load reads every section of the overview. Sections are fetched one after
// another on the same connection; a self-hosted instance is small enough that
// the latency of five catalog-scale queries is not worth a parallel fan-out
// whose failure mode is harder to attribute.
func (s *Store) Load(ctx context.Context, since time.Time, tz string) (Overview, error) {
	workspaces, err := s.workspaces(ctx)
	if err != nil {
		return Overview{}, err
	}
	users, err := s.users(ctx)
	if err != nil {
		return Overview{}, err
	}
	agents, err := s.agents(ctx)
	if err != nil {
		return Overview{}, err
	}
	usage, err := s.usage(ctx, since, tz)
	if err != nil {
		return Overview{}, err
	}
	summary, err := s.activity(ctx, since)
	if err != nil {
		return Overview{}, err
	}

	summary.WorkspaceCount = len(workspaces)
	summary.UserCount = len(users)
	summary.AgentCount = len(agents)
	for _, ws := range workspaces {
		summary.RuntimeCount += ws.RuntimeCount
		summary.OnlineRuntimeCount += ws.OnlineRuntimeCount
		summary.OpenIssueCount += ws.OpenIssueCount
	}
	// Token and cost totals are the sum of the rows already loaded for the same
	// window, so the meter is read once instead of once per section. A separate
	// SUM query would return the same numbers and pay for a second scan.
	for _, row := range usage {
		summary.InputTokens += row.InputTokens
		summary.OutputTokens += row.OutputTokens
		summary.CacheReadTokens += row.CacheReadTokens
		summary.CacheWriteTokens += row.CacheWriteTokens
		summary.CostUSDTicks += row.CostUSDTicks
		summary.UncostedInputTokens += row.UncostedInputTokens
		summary.UncostedOutputTokens += row.UncostedOutputTokens
		summary.UncostedCacheRead += row.UncostedCacheRead
		summary.UncostedCacheWrite += row.UncostedCacheWrite
	}

	return Overview{
		Workspaces: workspaces,
		Users:      users,
		Agents:     agents,
		Usage:      usage,
		Summary:    summary,
	}, nil
}

// workspaces lists every workspace with the counts an operator asks about
// first, plus the earliest-joined owner as its accountable contact.
func (s *Store) workspaces(ctx context.Context) ([]WorkspaceSummary, error) {
	const query = `
SELECT
    w.id,
    w.name,
    w.slug,
    w.created_at,
    (SELECT COUNT(*) FROM member m WHERE m.workspace_id = w.id)::int AS member_count,
    (SELECT COUNT(*) FROM agent a
      WHERE a.workspace_id = w.id AND a.archived_at IS NULL AND a.kind = 'user')::int AS agent_count,
    (SELECT COUNT(*) FROM agent_runtime ar WHERE ar.workspace_id = w.id)::int AS runtime_count,
    (SELECT COUNT(*) FROM agent_runtime ar
      WHERE ar.workspace_id = w.id AND ar.status = 'online')::int AS online_runtime_count,
    -- issue_effective_status resolves custom status keys to their category, so
    -- a workspace whose "shipped" / "wontfix" statuses are archived terminal
    -- states does not count those issues as open (migrations/340, /494).
    (SELECT COUNT(*) FROM issue i
      WHERE i.workspace_id = w.id
        AND issue_effective_status(i.workspace_id, i.status) NOT IN ('done', 'cancelled'))::int AS open_issue_count,
    GREATEST(
        w.updated_at,
        COALESCE((SELECT MAX(i.updated_at) FROM issue i WHERE i.workspace_id = w.id), w.updated_at)
    ) AS last_activity_at,
    owner.id AS owner_id,
    owner.name AS owner_name,
    owner.email AS owner_email
FROM workspace w
LEFT JOIN LATERAL (
    SELECT u.id, u.name, u.email
    FROM member m
    JOIN "user" u ON u.id = m.user_id
    WHERE m.workspace_id = w.id AND m.role = 'owner'
    ORDER BY m.created_at ASC, u.id ASC
    LIMIT 1
) owner ON TRUE
ORDER BY w.created_at ASC, w.name ASC
`
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list instance workspaces: %w", err)
	}
	defer rows.Close()

	workspaces := make([]WorkspaceSummary, 0)
	for rows.Next() {
		var (
			ws         WorkspaceSummary
			id         pgtype.UUID
			createdAt  pgtype.Timestamptz
			activeAt   pgtype.Timestamptz
			ownerID    pgtype.UUID
			ownerName  pgtype.Text
			ownerEmail pgtype.Text
		)
		if err := rows.Scan(
			&id, &ws.Name, &ws.Slug, &createdAt,
			&ws.MemberCount, &ws.AgentCount, &ws.RuntimeCount, &ws.OnlineRuntimeCount,
			&ws.OpenIssueCount, &activeAt,
			&ownerID, &ownerName, &ownerEmail,
		); err != nil {
			return nil, fmt.Errorf("scan instance workspace: %w", err)
		}
		ws.ID = util.UUIDToString(id)
		ws.CreatedAt = util.TimestampToString(createdAt)
		ws.LastActivityAt = util.TimestampToPtr(activeAt)
		ws.Owner = ownerOrNil(ownerID, ownerName, ownerEmail)
		workspaces = append(workspaces, ws)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list instance workspaces: %w", err)
	}
	return workspaces, nil
}

// users lists every account with its workspace memberships and last client
// heartbeat. The membership array is built in SQL so the response cannot grow
// one query per account.
func (s *Store) users(ctx context.Context) ([]UserSummary, error) {
	const query = `
SELECT
    u.id,
    u.name,
    u.email,
    u.created_at,
    COALESCE(
        JSON_AGG(
            JSON_BUILD_OBJECT('id', w.id, 'name', w.name, 'role', m.role)
            ORDER BY w.name ASC, w.id ASC
        ) FILTER (WHERE w.id IS NOT NULL),
        '[]'::json
    ) AS memberships,
    (SELECT MAX(cu.last_active_at) FROM client_usage_daily cu WHERE cu.user_id = u.id) AS last_active_at
FROM "user" u
LEFT JOIN member m ON m.user_id = u.id
LEFT JOIN workspace w ON w.id = m.workspace_id
GROUP BY u.id
ORDER BY u.created_at ASC, u.id ASC
`
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list instance users: %w", err)
	}
	defer rows.Close()

	users := make([]UserSummary, 0)
	for rows.Next() {
		var (
			user       UserSummary
			id         pgtype.UUID
			createdAt  pgtype.Timestamptz
			membership []byte
			activeAt   pgtype.Timestamptz
		)
		if err := rows.Scan(&id, &user.Name, &user.Email, &createdAt, &membership, &activeAt); err != nil {
			return nil, fmt.Errorf("scan instance user: %w", err)
		}
		user.ID = util.UUIDToString(id)
		user.CreatedAt = util.TimestampToString(createdAt)
		user.LastActiveAt = util.TimestampToPtr(activeAt)
		user.Workspaces = []UserWorkspace{}
		if len(membership) > 0 {
			if err := json.Unmarshal(membership, &user.Workspaces); err != nil {
				return nil, fmt.Errorf("decode memberships for user %s: %w", user.ID, err)
			}
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list instance users: %w", err)
	}
	return users, nil
}

// agents lists user-facing agents across every workspace. Archived agents and
// the `kind = 'system'` builder carriers are excluded: they are not something
// an operator manages, and no other list surface names them.
func (s *Store) agents(ctx context.Context) ([]AgentSummary, error) {
	const query = `
SELECT
    a.id,
    a.workspace_id,
    w.name AS workspace_name,
    a.name,
    a.model,
    a.runtime_id,
    ar.provider,
    ar.status,
    ar.last_seen_at,
    owner.id AS owner_id,
    owner.name AS owner_name,
    owner.email AS owner_email,
    (SELECT MAX(t.created_at) FROM agent_task_queue t WHERE t.agent_id = a.id) AS last_run_at
FROM agent a
JOIN workspace w ON w.id = a.workspace_id
LEFT JOIN agent_runtime ar ON ar.id = a.runtime_id
LEFT JOIN "user" owner ON owner.id = a.owner_id
WHERE a.archived_at IS NULL AND a.kind = 'user'
ORDER BY w.name ASC, a.name ASC, a.id ASC
`
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list instance agents: %w", err)
	}
	defer rows.Close()

	agents := make([]AgentSummary, 0)
	for rows.Next() {
		var (
			agent      AgentSummary
			id         pgtype.UUID
			workspace  pgtype.UUID
			model      pgtype.Text
			runtimeID  pgtype.UUID
			provider   pgtype.Text
			status     pgtype.Text
			lastSeen   pgtype.Timestamptz
			ownerID    pgtype.UUID
			ownerName  pgtype.Text
			ownerEmail pgtype.Text
			lastRunAt  pgtype.Timestamptz
		)
		if err := rows.Scan(
			&id, &workspace, &agent.WorkspaceName, &agent.Name, &model,
			&runtimeID, &provider, &status, &lastSeen,
			&ownerID, &ownerName, &ownerEmail, &lastRunAt,
		); err != nil {
			return nil, fmt.Errorf("scan instance agent: %w", err)
		}
		agent.ID = util.UUIDToString(id)
		agent.WorkspaceID = util.UUIDToString(workspace)
		agent.Model = textOrEmpty(model)
		agent.RuntimeID = util.UUIDToPtr(runtimeID)
		agent.RuntimeProvider = textOrEmpty(provider)
		agent.RuntimeStatus = textOrEmpty(status)
		agent.RuntimeLastSeen = util.TimestampToPtr(lastSeen)
		agent.Owner = ownerOrNil(ownerID, ownerName, ownerEmail)
		agent.LastRunAt = util.TimestampToPtr(lastRunAt)
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list instance agents: %w", err)
	}
	return agents, nil
}

// usage returns the windowed daily series across all workspaces, bucketed by
// the day the caller's timezone puts each hour in. provider is LOWER()-normalized
// exactly as the workspace dashboard does (pkg/db/queries/task_usage.sql), so
// mixed-case historical rows merge with canonical lowercase ones instead of
// splitting one provider across two buckets.
//
// The cost split mirrors that same query: cost_usd_ticks is NOT NULL DEFAULT 0
// and counts only provider-priced rows, while the uncosted_* columns carry the
// tokens still priced from the static rate table (NULL on buckets the rollup
// has not recomputed since migration 213 — read as "estimate all of them").
// LEFT JOIN because task_usage_hourly has no foreign key to workspace: rows
// that outlive a deleted workspace still count toward the instance's usage,
// and hiding them would make this report disagree with the meter.
func (s *Store) usage(ctx context.Context, since time.Time, tz string) ([]UsageRow, error) {
	const query = `
SELECT
    DATE(tu.bucket_hour AT TIME ZONE $1::text) AS day,
    tu.workspace_id,
    COALESCE(w.name, '') AS workspace_name,
    LOWER(tu.provider) AS provider,
    tu.model,
    SUM(tu.input_tokens)::bigint AS input_tokens,
    SUM(tu.output_tokens)::bigint AS output_tokens,
    SUM(tu.cache_read_tokens)::bigint AS cache_read_tokens,
    SUM(tu.cache_write_tokens)::bigint AS cache_write_tokens,
    SUM(tu.cost_usd_ticks)::bigint AS cost_usd_ticks,
    SUM(COALESCE(tu.uncosted_input_tokens, tu.input_tokens))::bigint AS uncosted_input_tokens,
    SUM(COALESCE(tu.uncosted_output_tokens, tu.output_tokens))::bigint AS uncosted_output_tokens,
    SUM(COALESCE(tu.uncosted_cache_read_tokens, tu.cache_read_tokens))::bigint AS uncosted_cache_read_tokens,
    SUM(COALESCE(tu.uncosted_cache_write_tokens, tu.cache_write_tokens))::bigint AS uncosted_cache_write_tokens,
    SUM(tu.task_count)::bigint AS task_count
FROM task_usage_hourly tu
LEFT JOIN workspace w ON w.id = tu.workspace_id
WHERE tu.bucket_hour >= $2
GROUP BY 1, 2, 3, 4, 5
ORDER BY 1 DESC, 3 ASC, 4 ASC, 5 ASC
`
	rows, err := s.db.Query(ctx, query, tz, since)
	if err != nil {
		return nil, fmt.Errorf("list instance usage: %w", err)
	}
	defer rows.Close()

	usage := make([]UsageRow, 0)
	for rows.Next() {
		var (
			row       UsageRow
			day       time.Time
			workspace pgtype.UUID
		)
		if err := rows.Scan(
			&day, &workspace, &row.WorkspaceName, &row.Provider, &row.Model,
			&row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CacheWriteTokens,
			&row.CostUSDTicks,
			&row.UncostedInputTokens, &row.UncostedOutputTokens,
			&row.UncostedCacheRead, &row.UncostedCacheWrite,
			&row.TaskCount,
		); err != nil {
			return nil, fmt.Errorf("scan instance usage: %w", err)
		}
		row.Date = day.Format("2006-01-02")
		row.WorkspaceID = util.UUIDToString(workspace)
		usage = append(usage, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list instance usage: %w", err)
	}
	return usage, nil
}

// activity reads the run counts confined to the window: tasks that were queued
// inside it and how many of them ended failed. Token and cost totals are not
// read here — Load sums them from the usage rows, which are already loaded for
// the same window. Object counts are filled in by Load from the lists it
// already has.
func (s *Store) activity(ctx context.Context, since time.Time) (Summary, error) {
	const query = `
SELECT
    (SELECT COUNT(*) FROM agent_task_queue WHERE created_at >= $1)::int AS runs,
    (SELECT COUNT(*) FROM agent_task_queue WHERE created_at >= $1 AND status = 'failed')::int AS failed_runs
`
	var summary Summary
	if err := s.db.QueryRow(ctx, query, since).Scan(
		&summary.Runs,
		&summary.FailedRuns,
	); err != nil {
		return Summary{}, fmt.Errorf("read instance activity summary: %w", err)
	}
	return summary, nil
}

func textOrEmpty(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func ownerOrNil(id pgtype.UUID, name, email pgtype.Text) *Owner {
	if !id.Valid {
		return nil
	}
	return &Owner{
		ID:    util.UUIDToString(id),
		Name:  textOrEmpty(name),
		Email: textOrEmpty(email),
	}
}
