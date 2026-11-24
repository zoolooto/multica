package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/skillpolicy"
)

func discoveredSkill(root, key, name, plugin string) runtimeLocalSkillSummary {
	return runtimeLocalSkillSummary{Root: root, Key: key, Name: name, Plugin: plugin, ExternalEnabled: nil}
}

func refIdentities(refs []execenv.RuntimeSkillRefForEnv) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.Root+"/"+ref.Plugin+"/"+ref.Key+"/"+ref.Name)
	}
	return out
}

func TestRuntimeSkillDisables_PolicyMatrix(t *testing.T) {
	t.Parallel()

	claudeSkills := []runtimeLocalSkillSummary{
		discoveredSkill("provider", "reviewer", "Reviewer", ""),
		discoveredSkill("universal", "planner", "Planner", ""),
	}
	tests := []struct {
		name       string
		provider   string
		policy     string
		overrides  []DisabledRuntimeSkillData
		discovered []runtimeLocalSkillSummary
		external   *localSkillExternalState
		want       []string
	}{
		{
			name:       "all_enabled inherits everything",
			provider:   "claude",
			policy:     skillpolicy.AllEnabled,
			discovered: claudeSkills,
			want:       nil,
		},
		{
			name:     "all_enabled still honours an explicit off",
			provider: "claude",
			policy:   skillpolicy.AllEnabled,
			overrides: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "reviewer", Name: "Reviewer"},
			},
			want: []string{"provider//reviewer/Reviewer"},
		},
		{
			name:       "all_disabled disables every inherited skill",
			provider:   "claude",
			policy:     skillpolicy.AllDisabled,
			discovered: claudeSkills,
			want:       []string{"provider//reviewer/Reviewer", "universal//planner/Planner"},
		},
		{
			name:       "an explicit on escapes all_disabled",
			provider:   "claude",
			policy:     skillpolicy.AllDisabled,
			discovered: claudeSkills,
			overrides: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "reviewer", Name: "Reviewer", Mode: "on"},
			},
			want: []string{"universal//planner/Planner"},
		},
		{
			name:       "an explicit off does not double count under all_disabled",
			provider:   "claude",
			policy:     skillpolicy.AllDisabled,
			discovered: claudeSkills,
			overrides: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "reviewer", Name: "Reviewer", Mode: "off"},
			},
			want: []string{"provider//reviewer/Reviewer", "universal//planner/Planner"},
		},
		{
			name:     "follow_external applies the tool's own disables",
			provider: "claude",
			policy:   skillpolicy.FollowExternal,
			external: &localSkillExternalState{
				provider:       "claude",
				claudeDisabled: map[string]bool{"reviewer": true},
				claudeKnown:    true,
			},
			want: []string{"provider//reviewer/reviewer"},
		},
		{
			name:     "follow_external with an unknown config falls back to enabled",
			provider: "claude",
			policy:   skillpolicy.FollowExternal,
			external: &localSkillExternalState{provider: "claude"},
			want:     nil,
		},
		{
			name:     "an explicit on beats an external disable",
			provider: "claude",
			policy:   skillpolicy.FollowExternal,
			overrides: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "reviewer", Name: "reviewer", Mode: "on"},
			},
			external: &localSkillExternalState{
				provider:       "claude",
				claudeDisabled: map[string]bool{"reviewer": true},
				claudeKnown:    true,
			},
			want: nil,
		},
		{
			name:     "an explicit on matches an external disable by invocation name when the discovery key differs",
			provider: "claude",
			policy:   skillpolicy.FollowExternal,
			overrides: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "reviewer-dir", Name: "reviewer", Mode: "on"},
			},
			external: &localSkillExternalState{
				provider:       "claude",
				claudeDisabled: map[string]bool{"reviewer": true},
				claudeKnown:    true,
			},
			want: nil,
		},
		{
			name:     "unknown policy falls back to follow_external",
			provider: "claude",
			policy:   "made_up",
			external: &localSkillExternalState{
				provider:       "claude",
				claudeDisabled: map[string]bool{"reviewer": true},
				claudeKnown:    true,
			},
			want: []string{"provider//reviewer/reviewer"},
		},
		{
			name:       "providers without a disable mechanism keep explicit-off semantics",
			provider:   "opencode",
			policy:     skillpolicy.AllDisabled,
			discovered: []runtimeLocalSkillSummary{discoveredSkill("provider", "reviewer", "Reviewer", "")},
			overrides: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "opencode", Root: "provider", Key: "planner", Name: "Planner"},
			},
			want: []string{"provider//planner/Planner"},
		},
		{
			name:     "overrides from another runtime are ignored",
			provider: "claude",
			policy:   skillpolicy.AllEnabled,
			overrides: []DisabledRuntimeSkillData{
				{RuntimeID: "other-runtime", Provider: "claude", Root: "provider", Key: "reviewer", Name: "Reviewer"},
				{RuntimeID: "rt", Provider: "codex", Root: "provider", Key: "reviewer", Name: "Reviewer"},
			},
			want: nil,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := runtimeSkillDisables(test.provider, test.policy, test.overrides, "rt", test.discovered, test.external)
			if gotRefs, wantRefs := refIdentities(got), test.want; !equalStringSlices(gotRefs, wantRefs) {
				t.Fatalf("runtimeSkillDisables() = %v, want %v", gotRefs, wantRefs)
			}
		})
	}
}

func TestCodexSkillRefForPath(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	roots := []localSkillRoot{
		{path: filepath.Join(home, ".codex", "skills"), kind: localSkillRootProvider},
		{path: filepath.Join(home, ".agents", "skills"), kind: localSkillRootUniversal},
	}

	root, key, ok := codexSkillRefForPath(roots, filepath.Join(home, ".codex", "skills", "review", "SKILL.md"))
	if !ok || root != localSkillRootProvider || key != "review" {
		t.Fatalf("provider path mapped to (%q,%q,%v)", root, key, ok)
	}
	root, key, ok = codexSkillRefForPath(roots, filepath.Join(home, ".agents", "skills", "release", "reporter", "SKILL.md"))
	if !ok || root != localSkillRootUniversal || key != "release/reporter" {
		t.Fatalf("universal nested path mapped to (%q,%q,%v)", root, key, ok)
	}
	root, key, ok = codexSkillRefForPath(roots, filepath.Join(home, ".codex", "skills", "bare-dir"))
	if !ok || root != localSkillRootProvider || key != "bare-dir" {
		t.Fatalf("a directory-spelled entry mapped to (%q,%q,%v)", root, key, ok)
	}
	if _, _, ok := codexSkillRefForPath(roots, "/elsewhere/project/.codex/skills/x/SKILL.md"); ok {
		t.Fatal("a path outside every discovery root must not map")
	}
	if _, _, ok := codexSkillRefForPath(roots, filepath.Join(home, ".codex", "skills")); ok {
		t.Fatal("the root itself must not map to a skill")
	}
}

func TestWorkspaceRuntimeSkillsDefaultPolicy(t *testing.T) {
	t.Parallel()

	daemon := &Daemon{workspaces: map[string]*workspaceState{
		"ws-default":   {settings: json.RawMessage(`{}`)},
		"ws-policy":    {settings: json.RawMessage(`{"runtime_skills_default_policy":"all_disabled"}`)},
		"ws-mixed":     {settings: json.RawMessage(`{"runtime_skills_default_policy":" ALL_ENABLED "}`)},
		"ws-unknown":   {settings: json.RawMessage(`{"runtime_skills_default_policy":"whatever"}`)},
		"ws-malformed": {settings: json.RawMessage(`{`)},
		"ws-empty":     {settings: nil},
	}}

	cases := map[string]string{
		"ws-missing":   skillpolicy.FollowExternal,
		"ws-default":   skillpolicy.FollowExternal,
		"ws-policy":    skillpolicy.AllDisabled,
		"ws-mixed":     skillpolicy.AllEnabled,
		"ws-unknown":   skillpolicy.FollowExternal,
		"ws-malformed": skillpolicy.FollowExternal,
		"ws-empty":     skillpolicy.FollowExternal,
	}
	for workspaceID, want := range cases {
		if got := daemon.workspaceRuntimeSkillsDefaultPolicy(workspaceID); got != want {
			t.Fatalf("workspace %s policy = %q, want %q", workspaceID, got, want)
		}
	}
}

// TestRuntimeSkillDisablesForTask covers the daemon-side wiring end to end:
// workspace policy from cached settings + discovery + agent overrides.
func TestRuntimeSkillDisablesForTask(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("CODEX_HOME", "")
	writeTestLocalSkill(t, filepath.Join(home, ".claude", "skills"), "reviewer", map[string]string{
		"SKILL.md": "# Reviewer\n",
	})
	writeTestLocalSkill(t, filepath.Join(home, ".claude", "skills"), "planner", map[string]string{
		"SKILL.md": "# Planner\n",
	})

	daemon := &Daemon{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces: map[string]*workspaceState{
			"ws": {settings: json.RawMessage(`{"runtime_skills_default_policy":"all_disabled"}`)},
		},
	}
	task := Task{
		RuntimeID:   "rt",
		WorkspaceID: "ws",
		Agent: &AgentData{
			DisabledRuntimeSkills: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "reviewer", Name: "reviewer", Mode: "on"},
			},
		},
	}

	refs := daemon.runtimeSkillDisablesForTask(task, "claude")
	if len(refs) != 1 || refs[0].Key != "planner" {
		t.Fatalf("all_disabled should keep only the escaped skill disabled, got %+v", refs)
	}

	// A provider without policy support ignores the workspace policy entirely.
	refs = daemon.runtimeSkillDisablesForTask(task, "opencode")
	if len(refs) != 0 {
		t.Fatalf("unsupported provider must only receive explicit off overrides, got %+v", refs)
	}
}

func TestRuntimeSkillDisablesForTask_DiscoveryFailureFallsBackToOverrides(t *testing.T) {
	home := t.TempDir()
	// A regular file where the provider root's parent directory belongs makes
	// the discovery stat fail with something other than "not exists", which is
	// the only way discovery reports an error for a supported provider.
	if err := os.WriteFile(filepath.Join(home, ".claude"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	setTestUserHome(t, home)
	t.Setenv("CODEX_HOME", "")

	var logs bytes.Buffer
	daemon := &Daemon{
		logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		workspaces: map[string]*workspaceState{
			"ws": {settings: json.RawMessage(`{"runtime_skills_default_policy":"all_disabled"}`)},
		},
	}
	task := Task{
		RuntimeID:   "rt",
		WorkspaceID: "ws",
		Agent: &AgentData{
			RuntimeSkillOverrides: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "reviewer", Name: "reviewer"},
			},
		},
	}

	refs := daemon.runtimeSkillDisablesForTask(task, "claude")
	if len(refs) != 1 || refs[0].Key != "reviewer" {
		t.Fatalf("discovery failure must degrade to explicit overrides only, got %+v", refs)
	}
	// Assert the fallback branch actually ran: without it, a nil discovery
	// result produces the same refs, so the result alone proves nothing.
	if !strings.Contains(logs.String(), "discovery for all_disabled policy failed") {
		t.Fatalf("expected the discovery-failure warning, got %q", logs.String())
	}
}

// TestRuntimeSkillDisablesForTask_PrefersTriStateOverrides proves the claim's
// new field wins over the legacy off-only one, so a forced-on entry can escape
// a policy that the legacy field would otherwise apply.
func TestRuntimeSkillDisablesForTask_PrefersTriStateOverrides(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("CODEX_HOME", "")
	writeTestLocalSkill(t, filepath.Join(home, ".claude", "skills"), "planner", map[string]string{
		"SKILL.md": "# Planner\n",
	})

	daemon := &Daemon{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces: map[string]*workspaceState{
			"ws": {settings: json.RawMessage(`{"runtime_skills_default_policy":"all_disabled"}`)},
		},
	}
	task := Task{
		RuntimeID:   "rt",
		WorkspaceID: "ws",
		Agent: &AgentData{
			DisabledRuntimeSkills: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "planner", Name: "planner"},
			},
			RuntimeSkillOverrides: []DisabledRuntimeSkillData{
				{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "planner", Name: "planner", Mode: "on"},
			},
		},
	}

	if refs := daemon.runtimeSkillDisablesForTask(task, "claude"); len(refs) != 0 {
		t.Fatalf("the tri-state field must win over the legacy off-only field, got %+v", refs)
	}
}

func TestRuntimeSkillDisablesForTask_NilAgent(t *testing.T) {
	t.Parallel()

	daemon := &Daemon{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces: map[string]*workspaceState{"ws": {settings: json.RawMessage(`{}`)}},
	}
	if refs := daemon.runtimeSkillDisablesForTask(Task{RuntimeID: "rt", WorkspaceID: "ws"}, "claude"); len(refs) != 0 {
		t.Fatalf("a task without agent data must not produce disables, got %+v", refs)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
