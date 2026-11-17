package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// localSkillExternalState is a one-shot snapshot of the runtime tool's own
// skill disable configuration, read once per discovery pass (GH #10776).
//
// Discovery previously reported a skill as available purely because its
// SKILL.md existed, which disagreed with a user who had already disabled that
// skill in Claude Code (`skillOverrides`) or Codex (`[[skills.config]]`). The
// snapshot lets discovery report the external state and lets the task path
// inherit it, without watching files: a change is picked up on the next
// refresh, matching the documented limitation.
//
// "Known" is tracked separately from "disabled" so an absent or unreadable
// config reports unknown (nil) instead of asserting every skill is enabled.
type localSkillExternalState struct {
	provider string
	// claudeDisabled holds the invocation names Claude Code itself has turned
	// off. Keys are matched against the discovered skill's invocation name —
	// the same name the task environment writes into skillOverrides.
	claudeDisabled map[string]bool
	claudeKnown    bool
	// codexDisabled holds cleaned SKILL.md paths Codex has disabled. Entries
	// are matched against the discovered skill directory (or its SKILL.md).
	codexDisabled map[string]bool
	codexKnown    bool
}

// loadLocalSkillExternalState reads the external disable configuration for
// providers that have a stable one. Other providers return nil: they have no
// such concept, and reporting "enabled" for them would be a guess.
func loadLocalSkillExternalState(provider string) *localSkillExternalState {
	if provider != "claude" && provider != "codex" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	state := &localSkillExternalState{provider: provider}
	switch provider {
	case "claude":
		state.claudeDisabled, state.claudeKnown = readClaudeDisabledSkills(
			filepath.Join(home, ".claude", "settings.json"),
		)
	case "codex":
		state.codexDisabled, state.codexKnown = readCodexDisabledSkillPaths(
			filepath.Join(codexHomeDir(home), "config.toml"),
		)
	}
	return state
}

// codexHomeDir resolves Codex's home the same way discovery resolves its skill
// root, so the config read here describes the registry the same roots feed.
func codexHomeDir(home string) string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return codexHome
	}
	return filepath.Join(home, ".codex")
}

// enabledFor reports whether the external tool would load this discovered
// skill. nil means the provider or its configuration cannot answer.
func (s *localSkillExternalState) enabledFor(rootKind, plugin, key, name, skillDir string) *bool {
	if s == nil {
		return nil
	}
	switch s.provider {
	case "claude":
		// Claude Code ignores skillOverrides for plugin skills; the plugin's
		// own enabledPlugins gate is what exposed them to discovery.
		if rootKind == localSkillRootPlugin || plugin != "" {
			return nil
		}
		if !s.claudeKnown {
			return nil
		}
		enabled := !s.claudeDisabled[strings.TrimSpace(name)]
		return &enabled
	case "codex":
		if !s.codexKnown {
			return nil
		}
		enabled := !s.codexDisabled[cleanExternalSkillPath(skillDir)] &&
			!s.codexDisabled[cleanExternalSkillPath(filepath.Join(skillDir, "SKILL.md"))]
		return &enabled
	default:
		return nil
	}
}

// disabledRefs returns the task-environment references for skills this
// provider's own configuration disables, so a `follow_external` policy can
// apply them without walking the skill roots. Claude refs use the override
// name as both the key and the invocation name, which is exactly what
// prepareClaudeSkillSettings consumes; Codex refs carry the discovery root so
// ensureCodexDisabledSkillsConfig can rebuild the SKILL.md path.
func (s *localSkillExternalState) disabledRefs() []runtimeSkillRef {
	if s == nil {
		return nil
	}
	switch s.provider {
	case "claude":
		if !s.claudeKnown {
			return nil
		}
		names := make([]string, 0, len(s.claudeDisabled))
		for name := range s.claudeDisabled {
			names = append(names, name)
		}
		sort.Strings(names)
		refs := make([]runtimeSkillRef, 0, len(names))
		for _, name := range names {
			refs = append(refs, runtimeSkillRef{Root: localSkillRootProvider, Key: name, Name: name})
		}
		return refs
	case "codex":
		if !s.codexKnown {
			return nil
		}
		roots, supported, err := localSkillRootsForProvider("codex")
		if err != nil || !supported {
			return nil
		}
		paths := make([]string, 0, len(s.codexDisabled))
		for path := range s.codexDisabled {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		refs := make([]runtimeSkillRef, 0, len(paths))
		for _, path := range paths {
			root, key, ok := codexSkillRefForPath(roots, path)
			if !ok {
				continue
			}
			refs = append(refs, runtimeSkillRef{Root: root, Key: key})
		}
		return refs
	default:
		return nil
	}
}

// codexSkillRefForPath maps a disabled SKILL.md path back onto the discovery
// root that owns it. Paths outside every writable root (a project-level
// skill, a checked-out plugin) cannot be expressed as a runtime skill ref and
// are skipped: the task environment never copies them anyway.
func codexSkillRefForPath(roots []localSkillRoot, path string) (string, string, bool) {
	for _, root := range roots {
		rel, err := filepath.Rel(root.path, path)
		if err != nil || rel == "." || filepath.IsAbs(rel) ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		// Codex writes `path = "…/skills/<key>/SKILL.md"`, but a hand-edited
		// config may name the skill directory instead; accept both spellings.
		key := strings.TrimSuffix(rel, "/SKILL.md")
		if _, err := normalizeLocalSkillKey(key); err != nil {
			continue
		}
		return root.kind, key, true
	}
	return "", "", false
}

// readClaudeDisabledSkills parses `skillOverrides` out of a Claude Code
// settings file. Only an explicit off counts: the values Multica itself writes
// are the string "off" (see execenv/runtime_skill_policy.go), and a JSON
// `false` is the boolean spelling of the same intent. Anything else — an
// unknown future value, a plugin preference — is left to the tool.
func readClaudeDisabledSkills(settingsPath string) (map[string]bool, bool) {
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil, false
	}
	var settings struct {
		SkillOverrides map[string]json.RawMessage `json:"skillOverrides"`
	}
	if json.Unmarshal(raw, &settings) != nil {
		return nil, false
	}
	disabled := make(map[string]bool, len(settings.SkillOverrides))
	for name, value := range settings.SkillOverrides {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if claudeSkillOverrideDisabled(value) {
			disabled[name] = true
		}
	}
	return disabled, true
}

func claudeSkillOverrideDisabled(raw json.RawMessage) bool {
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return strings.EqualFold(strings.TrimSpace(asString), "off")
	}
	var asBool bool
	if json.Unmarshal(raw, &asBool) == nil {
		return !asBool
	}
	return false
}

// readCodexDisabledSkillPaths parses the `[[skills.config]]` entries Codex
// itself uses to disable a skill. Entries without a path (plugin-backed
// skills are written with only a `name`) and entries that do not set
// `enabled = false` are ignored.
func readCodexDisabledSkillPaths(configPath string) (map[string]bool, bool) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, false
	}
	var config struct {
		Skills struct {
			Config []struct {
				Path    string `toml:"path"`
				Enabled *bool  `toml:"enabled"`
			} `toml:"config"`
		} `toml:"skills"`
	}
	if err := toml.Unmarshal(raw, &config); err != nil {
		return nil, false
	}
	disabled := make(map[string]bool, len(config.Skills.Config))
	for _, entry := range config.Skills.Config {
		if entry.Enabled == nil || *entry.Enabled {
			continue
		}
		path := cleanExternalSkillPath(entry.Path)
		if path == "" {
			continue
		}
		disabled[path] = true
	}
	return disabled, true
}

// cleanExternalSkillPath normalizes a path coming from an external tool's
// config so it compares equal to a path this daemon built. Relative paths are
// kept relative: they cannot be matched against an absolute discovery root and
// are simply never found.
func cleanExternalSkillPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(trimmed))
}
