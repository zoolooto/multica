package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

// Agents register wakeups from prompt text, so the same subscription is often
// registered twice — on a retry, or because two rules in the prompt describe
// the same wait. The second registration must not add a rule that starts a
// second run on every matching event.
func TestIssueWakeupCreateReturnsTheExistingIdenticalRule(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	in := WakeupInput{
		AgentID: agent, Kind: "event", Mode: "continuous",
		EventTypes:  []string{"task.failed", "issue.status_changed"},
		Instruction: "Watch the deploy",
	}
	first := wakeCreate(t, f, s, issue, in)
	second := wakeCreate(t, f, s, issue, in)
	if first.ID != second.ID {
		t.Fatalf("second create made a new rule: %s then %s", util.UUIDToString(first.ID), util.UUIDToString(second.ID))
	}
	if n := f.Count(t, "SELECT count(*) FROM issue_wakeup WHERE issue_id=$1", issue); n != 1 {
		t.Fatalf("%d rules rows, want 1", n)
	}
	// Event order is not part of the rule: the same set is the same rule.
	in.EventTypes = []string{"issue.status_changed", "task.failed"}
	if again := wakeCreate(t, f, s, issue, in); again.ID != first.ID {
		t.Fatalf("reordered events made a new rule: %s", util.UUIDToString(again.ID))
	}
	if n := f.Count(t, "SELECT count(*) FROM issue_wakeup WHERE issue_id=$1", issue); n != 1 {
		t.Fatalf("%d rules rows after reordered events, want 1", n)
	}
}

// Anything that changes when the rule fires is a different rule.
func TestIssueWakeupCreateKeepsDifferentRulesApart(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	base := WakeupInput{AgentID: agent, Kind: "event", Instruction: "Watch the deploy", EventTypes: []string{"comment.created"}}
	first := wakeCreate(t, f, s, issue, base)

	variants := map[string]WakeupInput{
		"different instruction": {AgentID: agent, Kind: "event", Instruction: "Watch the deploy and report", EventTypes: []string{"comment.created"}},
		"extra event":           {AgentID: agent, Kind: "event", Instruction: "Watch the deploy", EventTypes: []string{"comment.created", "task.failed"}},
		"different actor":       {AgentID: agent, Kind: "event", Instruction: "Watch the deploy", EventTypes: []string{"comment.created"}, FilterActorType: "member", FilterActorID: f.UserID},
	}
	for name, in := range variants {
		if got := wakeCreate(t, f, s, issue, in); got.ID == first.ID {
			t.Fatalf("%s: reused the first rule", name)
		}
	}
	if n := f.Count(t, "SELECT count(*) FROM issue_wakeup WHERE issue_id=$1", issue); n != 4 {
		t.Fatalf("%d rules rows, want 4", n)
	}
}

// A withdrawn rule is not a subscription any more: registering it again must
// create a live one instead of returning the disabled row.
func TestIssueWakeupCreateDoesNotReuseADisabledRule(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	in := WakeupInput{AgentID: agent, Kind: "event", Instruction: "Watch the deploy", EventTypes: []string{"comment.created"}}
	first := wakeCreate(t, f, s, issue, in)
	if _, err := s.Disable(context.Background(), issue, first.ID, parseTestUUID(t, f.UserID)); err != nil {
		t.Fatal(err)
	}
	second := wakeCreate(t, f, s, issue, in)
	if second.ID == first.ID || !second.Enabled {
		t.Fatalf("disabled rule was reused: %+v", second)
	}
}

// Condition rules carry the same duplicate risk as event rules.
func TestIssueWakeupCreateIsIdempotentForIdenticalConditionRules(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	in := WakeupInput{
		AgentID: agent, Kind: "event", Mode: "continuous",
		Condition:   []byte(`{"type":"issue_field","field":"status","value":"in_review"}`),
		Instruction: "The issue you created reached in_review",
	}
	first := wakeCreate(t, f, s, issue, in)
	in.Condition = []byte(`{ "value" : "in_review", "field" : "status", "type" : "issue_field" }`)
	second := wakeCreate(t, f, s, issue, in)
	if first.ID != second.ID {
		t.Fatalf("reordered condition keys made a new rule: %s", util.UUIDToString(second.ID))
	}
}

// A relative deadline resolves to a different instant on every registration,
// so the duration is what makes the same wait the same rule. Registering
// `--expires-in 72h` twice must not add a second live rule.
func TestIssueWakeupCreateMatchesARelativeDeadline(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	in := WakeupInput{
		AgentID: agent, Kind: "event", Mode: "continuous",
		EventTypes: []string{"comment.created"}, Instruction: "Wait for the answer",
		ExpiresInSeconds: 3600,
	}
	first := wakeCreate(t, f, s, issue, in)
	if again := wakeCreate(t, f, s, issue, in); again.ID != first.ID {
		t.Fatalf("same relative deadline made a new rule: %s then %s", util.UUIDToString(first.ID), util.UUIDToString(again.ID))
	}
	if n := f.Count(t, "SELECT count(*) FROM issue_wakeup WHERE issue_id=$1", issue); n != 1 {
		t.Fatalf("%d rules rows, want 1", n)
	}
	// A different duration is a different wait, and an absolute deadline is a
	// different shape, so neither may be folded into the first rule.
	in.ExpiresInSeconds = 7200
	if got := wakeCreate(t, f, s, issue, in); got.ID == first.ID {
		t.Fatal("a different deadline reused the first rule")
	}
	at := time.Now().Add(3 * time.Hour)
	in = WakeupInput{
		AgentID: agent, Kind: "event", Mode: "continuous",
		EventTypes: []string{"comment.created"}, Instruction: "Wait for the answer", ExpiresAt: &at,
	}
	if got := wakeCreate(t, f, s, issue, in); got.ID == first.ID {
		t.Fatal("an absolute deadline reused a relative rule")
	}
}

// The retry that used to duplicate a rule is a retry that runs at the same time
// as the original request. The issue row lock is what keeps them apart, so the
// guarantee needs a test that actually overlaps them.
func TestIssueWakeupCreateSerializesIdenticalRegistrations(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	in := WakeupInput{
		AgentID: agent, Kind: "event", Mode: "continuous",
		EventTypes:  []string{"task.failed"},
		Instruction: "Watch the deploy",
	}
	const callers = 4
	member := parseTestUUID(t, f.UserID)
	var wg sync.WaitGroup
	ids := make([]pgtype.UUID, callers)
	errs := make([]error, callers)
	for n := 0; n < callers; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			w, err := s.Create(context.Background(), issue, member, pgtype.UUID{}, in)
			ids[n], errs[n] = w.ID, err
		}(n)
	}
	wg.Wait()
	for n, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", n, err)
		}
	}
	for n, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("caller %d registered a second rule: %s then %s", n+1, util.UUIDToString(ids[0]), util.UUIDToString(id))
		}
	}
	if n := f.Count(t, "SELECT count(*) FROM issue_wakeup WHERE issue_id=$1", issue); n != 1 {
		t.Fatalf("%d rules rows, want 1", n)
	}
}

// Returning the existing rule is not a new subscription: the timeline must not
// claim the rule was created twice.
func TestIssueWakeupCreateDoesNotRecordTheDuplicateRegistration(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	in := WakeupInput{AgentID: agent, Kind: "event", Instruction: "Watch the deploy", EventTypes: []string{"comment.created"}}
	first := wakeCreate(t, f, s, issue, in)
	wakeCreate(t, f, s, issue, in)
	if n := f.Count(t, "SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action=$2", issue, wakeupActivityCreated); n != 1 {
		t.Fatalf("%d wakeup_created entries, want 1", n)
	}
	var revision int64
	if err := f.Pool.QueryRow(context.Background(), "SELECT revision FROM issue_wakeup WHERE id=$1", first.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != first.Revision {
		t.Fatalf("revision changed from %d to %d", first.Revision, revision)
	}
}
