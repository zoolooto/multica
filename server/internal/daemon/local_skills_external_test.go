package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parents for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestReadClaudeDisabledSkills(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeTestFile(t, path, `{
		"skillOverrides": {
			"reviewer": "off",
			"shouty": "OFF",
			"legacy-bool": false,
			"planner": "on",
			"future": "something-else"
		}
	}`)

	disabled, known := readClaudeDisabledSkills(path)
	if !known {
		t.Fatal("a readable settings file must report known")
	}
	for _, name := range []string{"reviewer", "shouty", "legacy-bool"} {
		if !disabled[name] {
			t.Fatalf("%q should be reported disabled, got %+v", name, disabled)
		}
	}
	for _, name := range []string{"planner", "future"} {
		if disabled[name] {
			t.Fatalf("%q must not be reported disabled, got %+v", name, disabled)
		}
	}
}

func TestReadClaudeDisabledSkills_UnknownWhenUnreadable(t *testing.T) {
	t.Parallel()

	if _, known := readClaudeDisabledSkills(filepath.Join(t.TempDir(), "missing.json")); known {
		t.Fatal("a missing settings file must report unknown, not enabled")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	writeTestFile(t, path, `{not json`)
	if _, known := readClaudeDisabledSkills(path); known {
		t.Fatal("a malformed settings file must report unknown, not enabled")
	}
}

func TestReadCodexDisabledSkillPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeTestFile(t, path, `
model = "gpt-5"

[[skills.config]]
path = "/home/me/.codex/skills/reviewer/SKILL.md"
enabled = false

[[skills.config]]
path = "/home/me/.codex/skills/planner/SKILL.md"
enabled = true

[[skills.config]]
path = "/home/me/.codex/skills/implicit/SKILL.md"

[[skills.config]]
name = "superpowers:brainstorming"
enabled = false
`)

	disabled, known := readCodexDisabledSkillPaths(path)
	if !known {
		t.Fatal("a readable config must report known")
	}
	if !disabled["/home/me/.codex/skills/reviewer/SKILL.md"] {
		t.Fatalf("explicitly disabled path missing, got %+v", disabled)
	}
	if disabled["/home/me/.codex/skills/planner/SKILL.md"] {
		t.Fatalf("enabled = true must not be reported disabled, got %+v", disabled)
	}
	if disabled["/home/me/.codex/skills/implicit/SKILL.md"] {
		t.Fatalf("an entry without `enabled` must not be reported disabled, got %+v", disabled)
	}
	if len(disabled) != 1 {
		t.Fatalf("only the explicit disable should be recorded, got %+v", disabled)
	}
}

func TestReadCodexDisabledSkillPaths_UnknownWhenUnreadable(t *testing.T) {
	t.Parallel()

	if _, known := readCodexDisabledSkillPaths(filepath.Join(t.TempDir(), "missing.toml")); known {
		t.Fatal("a missing config must report unknown, not enabled")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	writeTestFile(t, path, "[[skills.config]\npath = oops")
	if _, known := readCodexDisabledSkillPaths(path); known {
		t.Fatal("a malformed config must report unknown, not enabled")
	}
}

func TestLoadLocalSkillExternalState_OtherProvidersAreNil(t *testing.T) {
	t.Parallel()

	if state := loadLocalSkillExternalState("opencode"); state != nil {
		t.Fatalf("providers without a disable concept must report nil, got %+v", state)
	}
	if state := loadLocalSkillExternalState(""); state != nil {
		t.Fatalf("an empty provider must report nil, got %+v", state)
	}
}

func TestListRuntimeLocalSkills_ReportsClaudeExternalEnabled(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("CODEX_HOME", "")

	writeTestFile(t, filepath.Join(home, ".claude", "settings.json"), `{
		"skillOverrides": {"reviewer": "off", "planner": "on"}
	}`)
	writeTestLocalSkill(t, filepath.Join(home, ".claude", "skills"), "reviewer", map[string]string{
		"SKILL.md": "# Reviewer\n",
	})
	writeTestLocalSkill(t, filepath.Join(home, ".claude", "skills"), "planner", map[string]string{
		"SKILL.md": "# Planner\n",
	})
	writeTestLocalSkill(t, filepath.Join(home, ".claude", "skills"), "unmentioned", map[string]string{
		"SKILL.md": "# Unmentioned\n",
	})

	skills, supported, err := listRuntimeLocalSkills("claude")
	if err != nil || !supported {
		t.Fatalf("listRuntimeLocalSkills(claude): supported=%v err=%v", supported, err)
	}
	byKey := make(map[string]*bool, len(skills))
	for _, summary := range skills {
		byKey[summary.Key] = summary.ExternalEnabled
	}
	assertExternalEnabled(t, byKey, "reviewer", false)
	assertExternalEnabled(t, byKey, "planner", true)
	assertExternalEnabled(t, byKey, "unmentioned", true)
}

func TestListRuntimeLocalSkills_ReportsCodexExternalEnabled(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("CODEX_HOME", codexHome)

	disabledSkill := writeTestLocalSkill(t, filepath.Join(codexHome, "skills"), "reviewer", map[string]string{
		"SKILL.md": "# Reviewer\n",
	})
	writeTestLocalSkill(t, filepath.Join(codexHome, "skills"), "planner", map[string]string{
		"SKILL.md": "# Planner\n",
	})
	writeTestFile(t, filepath.Join(codexHome, "config.toml"),
		"[[skills.config]]\npath = "+`"`+filepath.ToSlash(filepath.Join(disabledSkill, "SKILL.md"))+`"`+"\nenabled = false\n")

	skills, supported, err := listRuntimeLocalSkills("codex")
	if err != nil || !supported {
		t.Fatalf("listRuntimeLocalSkills(codex): supported=%v err=%v", supported, err)
	}
	byKey := make(map[string]*bool, len(skills))
	for _, summary := range skills {
		byKey[summary.Key] = summary.ExternalEnabled
	}
	assertExternalEnabled(t, byKey, "reviewer", false)
	assertExternalEnabled(t, byKey, "planner", true)
}

func TestListRuntimeLocalSkills_ExternalEnabledUnknownWithoutConfig(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("CODEX_HOME", "")

	writeTestLocalSkill(t, filepath.Join(home, ".claude", "skills"), "reviewer", map[string]string{
		"SKILL.md": "# Reviewer\n",
	})

	skills, _, err := listRuntimeLocalSkills("claude")
	if err != nil {
		t.Fatalf("listRuntimeLocalSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].ExternalEnabled != nil {
		t.Fatalf("without a settings file the state is unknown, got %v", *skills[0].ExternalEnabled)
	}
}

func TestListRuntimeLocalSkills_ClaudePluginExternalEnabledUnknown(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("CODEX_HOME", "")

	installPath := writeTestClaudePlugin(t, home, "toolkit@market", "toolkit", true)
	writeTestLocalSkill(t, filepath.Join(installPath, "skills"), "design", map[string]string{
		"SKILL.md": "# Design\n",
	})
	writeTestFile(t, filepath.Join(home, ".claude", "settings.json"), `{
		"enabledPlugins": {"toolkit@market": true},
		"skillOverrides": {"toolkit:design": "off"}
	}`)

	skills, _, err := listRuntimeLocalSkills("claude")
	if err != nil {
		t.Fatalf("listRuntimeLocalSkills: %v", err)
	}
	var pluginSkill *runtimeLocalSkillSummary
	for i := range skills {
		if skills[i].Root == localSkillRootPlugin {
			pluginSkill = &skills[i]
		}
	}
	if pluginSkill == nil {
		t.Fatalf("plugin skill not discovered: %+v", skills)
	}
	// Claude Code ignores skillOverrides for plugin skills, so discovery must
	// not claim a state it cannot observe there.
	if pluginSkill.ExternalEnabled != nil {
		t.Fatalf("plugin skills must report unknown, got %v", *pluginSkill.ExternalEnabled)
	}
}

func assertExternalEnabled(t *testing.T, byKey map[string]*bool, key string, want bool) {
	t.Helper()
	got, ok := byKey[key]
	if !ok {
		t.Fatalf("skill %q not discovered", key)
	}
	if got == nil {
		t.Fatalf("skill %q external_enabled = nil, want %v", key, want)
	}
	if *got != want {
		t.Fatalf("skill %q external_enabled = %v, want %v", key, *got, want)
	}
}
