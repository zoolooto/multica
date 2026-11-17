package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
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
		for _, state := range strings.Split(entry, ",") {
			state = strings.TrimSpace(state)
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
	}
	return out, nil
}

// wakeMeOnBody is the rule that reports one state to the issue's creator.
// Delivery and a blocker are status conditions, so a second review round wakes
// the creator again; a failed run is an event, because the issue stays
// in_progress and no status condition would ever hold.
func wakeMeOnBody(state string) map[string]any {
	body := map[string]any{"mode": "continuous", "instruction": wakeMeOnInstruction(state)}
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
	default:
		return "This issue, which you created, reached in_review. Read its latest activity, then decide the next step."
	}
}

// registerCreateWakes subscribes the calling agent to the new issue's states.
// The rule lives on the created issue, so closing that issue withdraws it like
// any other wakeup. A failure is reported as a partial success: the issue
// already exists and repeating the create would duplicate it.
func registerCreateWakes(ctx context.Context, client *cli.APIClient, created map[string]any, states []string) error {
	id := strVal(created, "id")
	if id == "" {
		return errors.New("server returned no created issue ID")
	}
	path := "/api/issues/" + url.PathEscape(id) + "/wakeups"
	failed := make([]string, 0, len(states))
	var errs []error
	for _, state := range states {
		var row map[string]any
		if err := client.PostJSON(ctx, path, wakeMeOnBody(state), &row); err != nil {
			failed = append(failed, state)
			errs = append(errs, fmt.Errorf("%s: %w", state, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("--wake-me-on %s was not registered (%w), which also means the server did not accept an agent identity", strings.Join(failed, ", "), errors.Join(errs...))
	}
	return nil
}
