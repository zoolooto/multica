package service

import (
	"bytes"
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// findIdenticalEventWakeup returns the caller's enabled event rule that behaves
// exactly like params, so registering the same subscription twice does not add
// a second row that would start a second run on every matching event. Agents
// register wakeups from prompt text, and a retry after an ambiguous failure
// used to be the common way a duplicate appeared.
//
// The caller holds the issue row lock (LockWakeupIssue), so two equal
// registrations serialize: the second one finds the rule the first created.
// The comparison deliberately covers every field that changes what wakes the
// target and when; source run and creation metadata do not, so a rule
// registered by a different run is still the same rule. The creator is part of
// the signature because a shared rule is not the caller's to manage: returning
// another member's row would report success and then forbid updating it.
//
// Only creation reaches here. An explicit update names its rule, so there is
// nothing to dedupe on that path.
func findIdenticalEventWakeup(ctx context.Context, tx pgx.Tx, q *db.Queries, params db.CreateIssueWakeupParams) (db.IssueWakeup, bool, error) {
	// System rules belong to the platform, not to the caller's subscriptions.
	rows, err := tx.Query(ctx, `SELECT id FROM issue_wakeup
		WHERE workspace_id=$1 AND issue_id=$2 AND agent_id=$3 AND created_by=$4
		  AND kind='event' AND enabled AND system_rule IS NULL
		ORDER BY created_at, id`, params.WorkspaceID, params.IssueID, params.AgentID, params.CreatedBy)
	if err != nil {
		return db.IssueWakeup{}, false, err
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return db.IssueWakeup{}, false, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return db.IssueWakeup{}, false, err
	}
	for _, id := range ids {
		existing, err := q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: id, WorkspaceID: params.WorkspaceID})
		if err != nil {
			return db.IssueWakeup{}, false, err
		}
		if sameWakeupSignature(existing, params) {
			return existing, true, nil
		}
	}
	return db.IssueWakeup{}, false, nil
}

func sameWakeupSignature(existing db.IssueWakeup, params db.CreateIssueWakeupParams) bool {
	if existing.Instruction != params.Instruction ||
		existing.Kind != params.Kind ||
		existing.Mode != params.Mode ||
		existing.Timezone != params.Timezone ||
		existing.ParentCommentID != params.ParentCommentID ||
		existing.FilterAgentID != params.FilterAgentID ||
		existing.FilterTaskID != params.FilterTaskID ||
		existing.FilterActorType != params.FilterActorType ||
		existing.FilterActorID != params.FilterActorID ||
		existing.IntervalSeconds != params.IntervalSeconds ||
		existing.CronExpression != params.CronExpression ||
		existing.OnTimeout != params.OnTimeout ||
		existing.ExpirySeconds != params.ExpirySeconds ||
		existing.MaxFires != params.MaxFires ||
		!sameWakeupDeadline(existing, params) {
		return false
	}
	return sameWakeupEvents(existing.EventTypes, params.EventTypes) &&
		bytes.Equal(canonicalWakeupPayload(existing.Condition), canonicalWakeupPayload(params.Condition))
}

// sameWakeupDeadline compares the two ways a rule can carry a deadline. A
// relative one is stored as both the duration and the instant it resolved to,
// and two registrations of the same `expires_in` legitimately resolve to
// different instants, so the duration is the signature whenever both rules
// carry one. Absolute deadlines still compare by value: pgx round-trips the
// same instant in a different location, so a struct comparison would miss it.
func sameWakeupDeadline(existing db.IssueWakeup, params db.CreateIssueWakeupParams) bool {
	if existing.ExpirySeconds.Valid || params.ExpirySeconds.Valid {
		return existing.ExpirySeconds == params.ExpirySeconds
	}
	a, b := existing.ExpiresAt, params.ExpiresAt
	if a.Valid != b.Valid {
		return false
	}
	return !a.Valid || a.Time.Equal(b.Time)
}

func sameWakeupEvents(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := slices.Clone(a)
	right := slices.Clone(b)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
