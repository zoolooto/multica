package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// wakeMeOnStates are the states an issue's creator can subscribe to while
// creating it: delivery, a blocker that needs a decision, and a failed run.
// They are the three moments the reporter of an issue waits for and cannot
// otherwise observe.
var wakeMeOnStates = []string{"in_review", "blocked", "failed"}

// parseWakeMeOn reads --wake-me-on, rejecting unknown states before the issue
// is created so a typo cannot leave a created issue with no subscription.
func parseWakeMeOn(cmd *cobra.Command) ([]string, error) {
	raw, _ := cmd.Flags().GetStringSlice("wake-me-on")
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, entry := range raw {
		state := strings.TrimSpace(entry)
		if state == "" {
			continue
		}
		if !slices.Contains(wakeMeOnStates, state) {
			return nil, fmt.Errorf("--wake-me-on takes %s", strings.Join(wakeMeOnStates, ", "))
		}
		if !seen[state] {
			seen[state] = true
			out = append(out, state)
		}
	}
	return out, nil
}

// wakeMeOnBody is the rule that reports one state to the issue's creator.
// Delivery and a blocker are status conditions, so a second review round wakes
// the creator again; a failed run is an event, because the issue stays
// in_progress and no status condition would ever hold.
func wakeMeOnBody(state string) map[string]any {
	body := map[string]any{"kind": "event", "mode": "continuous", "instruction": wakeMeOnInstruction(state)}
	if state == "failed" {
		body["event_types"] = []string{"task.failed"}
		return body
	}
	body["condition"] = map[string]any{"type": "issue_field", "field": "status", "value": state}
	return body
}

func wakeMeOnInstruction(state string) string {
	switch state {
	case "failed":
		return "A run on this issue, which you created, failed. Read the run's result and the issue's latest activity, then decide the next step."
	case "blocked":
		return "This issue, which you created, is blocked. Read what it is waiting for, then decide the next step: answer it, unblock it, or raise the decision with whoever owns it."
	case "in_review":
		return "This issue, which you created, reached in_review. Read its latest activity, then decide the next step."
	}
	return ""
}

// wakeMeOnCommand is the copy-pasteable replacement for one subscription whose
// registration failed after the issue was created. It carries the same rule
// shape wakeMeOnBody sends, so running it registers what the create missed.
func wakeMeOnCommand(key, state string) string {
	args := []string{"multica", "issue", "wakeup", "create", key, "--kind", "event", "--mode", "continuous"}
	if state == "failed" {
		args = append(args, "--event", "task.failed")
	} else {
		args = append(args, "--until-status", state)
	}
	args = append(args, "--instruction", strconv.Quote(wakeMeOnInstruction(state)))
	return strings.Join(args, " ")
}

// registerCreateWakes subscribes the calling agent to the new issue's states.
// The rule lives on the created issue, so closing that issue withdraws it like
// any other wakeup. A failure is reported as a partial success: the issue
// already exists, and repeating the create would duplicate it.
func registerCreateWakes(ctx context.Context, client *cli.APIClient, created map[string]any, states []string) error {
	id := strVal(created, "id")
	if id == "" {
		return errors.New("server returned no created issue ID")
	}
	key := issueDisplayKey(created)
	path := "/api/issues/" + url.PathEscape(id) + "/wakeups"
	var failed []string
	var lines []string
	denied := false
	for _, state := range states {
		if !slices.Contains(wakeMeOnStates, state) {
			return fmt.Errorf("unknown --wake-me-on state %q", state)
		}
		var row map[string]any
		if err := client.PostJSON(ctx, path, wakeMeOnBody(state), &row); err != nil {
			failed = append(failed, state)
			lines = append(lines, fmt.Sprintf("  - %s: %v", state, err))
			var httpErr *cli.HTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusForbidden {
				denied = true
			}
		}
	}
	if len(failed) == 0 {
		return nil
	}
	var msg strings.Builder
	msg.WriteString("these subscriptions were not registered:\n")
	msg.WriteString(strings.Join(lines, "\n"))
	msg.WriteString("\nThe issue exists, so do not create it again. Register the missing ones from this agent's own run with:\n")
	for _, state := range failed {
		msg.WriteString("  " + wakeMeOnCommand(key, state) + "\n")
	}
	if denied {
		msg.WriteString("A 403 here means the server did not accept this agent's identity for a subscription.\n")
	}
	return errors.New(msg.String())
}
