package instanceadmin

// Owner is the account behind a workspace or agent. It carries no role: an
// instance administrator does not inherit the workspace roles of the owner it
// looks at.
type Owner struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// WorkspaceSummary is one row of the instance-wide workspace list: the
// answers an operator needs before deciding whether a workspace is alive,
// without opening it.
//
// LastActivityAt is the later of the workspace row's own update and its newest
// issue update — the two timestamps that move whenever someone works in the
// workspace. Client heartbeats are reported per account instead, because they
// say who was active, not which workspace changed.
type WorkspaceSummary struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Slug               string  `json:"slug"`
	CreatedAt          string  `json:"created_at"`
	Owner              *Owner  `json:"owner"`
	MemberCount        int     `json:"member_count"`
	AgentCount         int     `json:"agent_count"`
	RuntimeCount       int     `json:"runtime_count"`
	OnlineRuntimeCount int     `json:"online_runtime_count"`
	OpenIssueCount     int     `json:"open_issue_count"`
	LastActivityAt     *string `json:"last_activity_at"`
}

// UserWorkspace is the caller-visible slice of a membership: which workspace
// an account belongs to and with which role.
type UserWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// UserSummary is one row of the instance-wide account list.
//
// LastActiveAt is the most recent client heartbeat recorded in
// client_usage_daily. It is null for accounts that never talked to this
// instance through an official client (API-only or dormant accounts) — an
// honest "unknown" rather than a misleading created_at fallback.
type UserSummary struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Email        string          `json:"email"`
	CreatedAt    string          `json:"created_at"`
	Workspaces   []UserWorkspace `json:"workspaces"`
	LastActiveAt *string         `json:"last_active_at"`
}

// AgentSummary is one row of the cross-workspace agent list, joined with the
// runtime that hosts it and its last run.
type AgentSummary struct {
	ID              string  `json:"id"`
	WorkspaceID     string  `json:"workspace_id"`
	WorkspaceName   string  `json:"workspace_name"`
	Name            string  `json:"name"`
	Owner           *Owner  `json:"owner"`
	RuntimeID       *string `json:"runtime_id"`
	RuntimeProvider string  `json:"runtime_provider"`
	RuntimeStatus   string  `json:"runtime_status"`
	RuntimeLastSeen *string `json:"runtime_last_seen_at"`
	Model           string  `json:"model"`
	LastRunAt       *string `json:"last_run_at"`
}

// UsageRow is instance-wide token/cost usage for one day, workspace, provider
// and model. The dimensions are kept separate so a caller can aggregate by
// any of them; the day is already projected into the viewer's timezone.
//
// CostUSDTicks only counts rows the provider itself priced (Cursor's auto
// model, for one, bills in credits instead); those rows still contribute their
// tokens, with zero cost. The workspace dashboard splits the same two cases
// into priced and unpriced token columns; this report keeps one cost total and
// says so in the operator docs rather than growing two more columns.
type UsageRow struct {
	Date             string `json:"date"`
	WorkspaceID      string `json:"workspace_id"`
	WorkspaceName    string `json:"workspace_name"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	CostUSDTicks     int64  `json:"cost_usd_ticks"`
	TaskCount        int64  `json:"task_count"`
}

// Summary is the instance-level headline: totals that hold regardless of the
// selected workspace (object counts) plus activity confined to the requested
// window (runs, failures, tokens, cost). CostUSDTicks follows UsageRow's
// priced-rows-only rule.
type Summary struct {
	WorkspaceCount     int   `json:"workspace_count"`
	UserCount          int   `json:"user_count"`
	AgentCount         int   `json:"agent_count"`
	RuntimeCount       int   `json:"runtime_count"`
	OnlineRuntimeCount int   `json:"online_runtime_count"`
	OpenIssueCount     int   `json:"open_issue_count"`
	Runs               int   `json:"runs"`
	FailedRuns         int   `json:"failed_runs"`
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	CacheReadTokens    int64 `json:"cache_read_tokens"`
	CacheWriteTokens   int64 `json:"cache_write_tokens"`
	CostUSDTicks       int64 `json:"cost_usd_ticks"`
}

// Overview is the whole instance read model in one payload. It is returned in
// a single response on purpose: every section is small on a self-hosted
// instance, and one round trip keeps a future console page from rendering
// four headers that disagree about the moment they were taken.
type Overview struct {
	GeneratedAt string             `json:"generated_at"`
	WindowDays  int                `json:"window_days"`
	Timezone    string             `json:"timezone"`
	Summary     Summary            `json:"summary"`
	Workspaces  []WorkspaceSummary `json:"workspaces"`
	Users       []UserSummary      `json:"users"`
	Agents      []AgentSummary     `json:"agents"`
	Usage       []UsageRow         `json:"usage"`
}
