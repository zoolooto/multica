package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseWakeMeOnRejectsUnknownStateBeforeAnyRequest(t *testing.T) {
	cmd := newIssueCreateTestCmd()
	_ = cmd.Flags().Set("wake-me-on", "in_review,done")
	if _, err := parseWakeMeOn(cmd); err == nil || !strings.Contains(err.Error(), "in_review, blocked, failed") {
		t.Fatalf("err = %v, want the accepted states", err)
	}
}

func TestParseWakeMeOnDeduplicatesAndTrims(t *testing.T) {
	cmd := newIssueCreateTestCmd()
	_ = cmd.Flags().Set("wake-me-on", "blocked, in_review,blocked")
	states, err := parseWakeMeOn(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(states, ",") != "blocked,in_review" {
		t.Fatalf("states = %v, want blocked,in_review", states)
	}
}

func TestRunIssueCreateRegistersWakeMeOnSubscriptions(t *testing.T) {
	var createBody map[string]any
	var wakeBodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/issues" && r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"id": "issue-1", "identifier": "MUL-1", "title": "Audit", "status": "todo", "priority": "none",
			})
		case r.URL.Path == "/api/issues/issue-1/wakeups" && r.Method == http.MethodPost:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode wakeup body: %v", err)
			}
			wakeBodies = append(wakeBodies, body)
			json.NewEncoder(w).Encode(map[string]any{"id": "wake-" + string(rune('0'+len(wakeBodies))), "enabled": true})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newIssueCreateTestCmd()
	_ = cmd.Flags().Set("title", "Audit")
	_ = cmd.Flags().Set("wake-me-on", "in_review,failed")
	if err := runIssueCreate(cmd, nil); err != nil {
		t.Fatalf("runIssueCreate: %v", err)
	}
	if createBody["title"] != "Audit" {
		t.Fatalf("create body = %+v", createBody)
	}
	if len(wakeBodies) != 2 {
		t.Fatalf("registered %d wakeups, want 2: %+v", len(wakeBodies), wakeBodies)
	}
	if got := wakeBodies[0]["condition"]; got == nil {
		t.Fatalf("in_review rule has no condition: %+v", wakeBodies[0])
	} else if condition, _ := got.(map[string]any); condition["field"] != "status" || condition["value"] != "in_review" || condition["type"] != "issue_field" {
		t.Fatalf("in_review condition = %+v", condition)
	}
	events, _ := wakeBodies[1]["event_types"].([]any)
	if len(events) != 1 || events[0] != "task.failed" {
		t.Fatalf("failed rule events = %+v", wakeBodies[1])
	}
	for _, body := range wakeBodies {
		if body["mode"] != "continuous" {
			t.Fatalf("mode = %v, want continuous so a second round wakes again: %+v", body["mode"], body)
		}
		if instruction, _ := body["instruction"].(string); instruction == "" {
			t.Fatalf("wakeup has no instruction: %+v", body)
		}
	}
}

func TestRunIssueCreateReportsPartialWakeMeOnRegistration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/issues" {
			json.NewEncoder(w).Encode(map[string]any{"id": "issue-1", "identifier": "MUL-1", "title": "Audit", "status": "todo"})
			return
		}
		http.Error(w, `{"error":"agent_id is required"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")

	cmd := newIssueCreateTestCmd()
	_ = cmd.Flags().Set("title", "Audit")
	_ = cmd.Flags().Set("wake-me-on", "blocked")
	err := runIssueCreate(cmd, nil)
	if err == nil {
		t.Fatal("want an error when the subscription is not registered")
	}
	// The issue exists; the message must say so and point at the follow-up
	// instead of inviting a retry that would create a duplicate.
	if !strings.Contains(err.Error(), "MUL-1 was created") || !strings.Contains(err.Error(), "multica issue wakeup create") {
		t.Fatalf("err = %v, want a partial-success message", err)
	}
}
