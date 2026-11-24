package handler

import (
	"encoding/json"
	"testing"
)

func TestResolveRuntimeSkillToggleMode(t *testing.T) {
	t.Parallel()

	enabled := true
	disabled := false
	cases := []struct {
		name    string
		rawMode string
		enabled *bool
		want    string
		wantOK  bool
	}{
		{name: "explicit on", rawMode: "on", want: RuntimeSkillModeOn, wantOK: true},
		{name: "explicit off", rawMode: " OFF ", want: RuntimeSkillModeOff, wantOK: true},
		{name: "inherit", rawMode: "inherit", want: RuntimeSkillModeInherit, wantOK: true},
		{name: "unknown mode", rawMode: "maybe", wantOK: false},
		{name: "legacy enabled true means inherit", enabled: &enabled, want: RuntimeSkillModeInherit, wantOK: true},
		{name: "legacy enabled false means off", enabled: &disabled, want: RuntimeSkillModeOff, wantOK: true},
		{name: "nothing supplied", wantOK: false},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := resolveRuntimeSkillToggleMode(test.rawMode, test.enabled)
			if ok != test.wantOK || got != test.want {
				t.Fatalf("resolveRuntimeSkillToggleMode(%q, %v) = (%q,%v), want (%q,%v)",
					test.rawMode, test.enabled, got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestNormalizeRuntimeSkillOverrides(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal([]map[string]any{
		{"runtime_id": "rt", "provider": "claude", "root": "provider", "key": "reviewer"},
		{"runtime_id": "rt", "provider": "claude", "root": "provider", "key": "planner", "mode": "on"},
		{"runtime_id": "rt", "provider": "claude", "root": "provider", "key": "legacy", "mode": "off"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := normalizeRuntimeSkillOverrides(raw)
	if len(got) != 3 {
		t.Fatalf("expected 3 overrides, got %d", len(got))
	}
	// A row written before tri-state existed means "off"; an explicit on stays.
	wantModes := map[string]string{"reviewer": RuntimeSkillModeOff, "planner": RuntimeSkillModeOn, "legacy": RuntimeSkillModeOff}
	for _, override := range got {
		if override.Mode != wantModes[override.Key] {
			t.Fatalf("override %q mode = %q, want %q", override.Key, override.Mode, wantModes[override.Key])
		}
	}
}

func TestRuntimeSkillOverridesForScopesToRuntimeAndProvider(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal([]map[string]any{
		{"runtime_id": "runtime-1", "provider": "claude", "root": "provider", "key": "review", "name": "Review"},
		{"runtime_id": "runtime-2", "provider": "claude", "root": "provider", "key": "other-runtime"},
		{"runtime_id": "runtime-1", "provider": "codex", "root": "provider", "key": "other-provider"},
		{"runtime_id": "runtime-1", "provider": "claude", "root": "provider", "key": "forced-on", "mode": "on"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := runtimeSkillOverridesFor(raw, "runtime-1", "claude")
	if len(got) != 2 {
		t.Fatalf("expected 2 overrides for this runtime/provider, got %+v", got)
	}
	if got[0].Key != "review" || got[1].Key != "forced-on" || got[1].Mode != RuntimeSkillModeOn {
		t.Fatalf("unexpected overrides: %+v", got)
	}
}

func TestDisabledRuntimeSkillOverridesKeepsOnlyOffEntries(t *testing.T) {
	t.Parallel()

	all := []DisabledRuntimeSkill{
		{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "off-one", Mode: RuntimeSkillModeOff},
		{RuntimeID: "rt", Provider: "claude", Root: "provider", Key: "on-one", Mode: RuntimeSkillModeOn},
	}
	got := disabledRuntimeSkillOverrides(all)
	if len(got) != 1 {
		t.Fatalf("expected 1 disabled entry, got %+v", got)
	}
	if got[0].Key != "off-one" || got[0].Mode != "" {
		t.Fatalf("unexpected disabled entry: %+v", got[0])
	}
}

func TestNormalizeRuntimeSkillsDefaultPolicy(t *testing.T) {
	t.Parallel()

	settings := map[string]any{
		"runtime_skills_default_policy": "  FOLLOW_EXTERNAL ",
		"github_enabled":                true,
	}
	normalizeRuntimeSkillsDefaultPolicy(settings)
	if got := settings["runtime_skills_default_policy"]; got != "follow_external" {
		t.Fatalf("policy stored as %v, want the canonical spelling", got)
	}
	if settings["github_enabled"] != true {
		t.Fatal("normalization must not touch other settings keys")
	}

	// An absent or non-string value is left for validation to reject.
	absent := map[string]any{}
	normalizeRuntimeSkillsDefaultPolicy(absent)
	if _, ok := absent["runtime_skills_default_policy"]; ok {
		t.Fatal("normalization must not invent the key")
	}
}

func TestValidateRuntimeSkillsDefaultPolicy(t *testing.T) {
	t.Parallel()

	valid := []map[string]any{
		{},
		{"runtime_skills_default_policy": nil},
		{"runtime_skills_default_policy": "all_enabled"},
		{"runtime_skills_default_policy": "all_disabled"},
		{"runtime_skills_default_policy": "follow_external"},
		{"github_enabled": true},
	}
	for _, settings := range valid {
		if err := validateRuntimeSkillsDefaultPolicy(settings); err != nil {
			t.Fatalf("settings %v rejected: %v", settings, err)
		}
	}

	invalid := []map[string]any{
		{"runtime_skills_default_policy": "all_on"},
		{"runtime_skills_default_policy": true},
		{"runtime_skills_default_policy": ""},
	}
	for _, settings := range invalid {
		if err := validateRuntimeSkillsDefaultPolicy(settings); err == nil {
			t.Fatalf("settings %v accepted, want a 400", settings)
		}
	}
}
