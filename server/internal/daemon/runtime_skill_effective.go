package daemon

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/skillpolicy"
)

// runtimeSkillOverrideModeOn marks a per-agent override that forces a skill on.
// Every other stored mode (including the empty mode of rows written before
// tri-state existed) means "off", which keeps existing user configuration
// meaning exactly what it meant before (GH #10776).
const runtimeSkillOverrideModeOn = "on"

// runtimeSkillDisablesForTask resolves the set of runtime-local skills this
// task must disable, from the agent's tri-state overrides plus the workspace
// default policy.
//
// The daemon owns this computation because it is the only side that can see
// what the runtime machine actually discovered and what the runtime tool has
// disabled in its own configuration; the server never persists discovery.
func (d *Daemon) runtimeSkillDisablesForTask(task Task, provider string) []execenv.RuntimeSkillRefForEnv {
	policy := d.workspaceRuntimeSkillsDefaultPolicy(task.WorkspaceID)

	var discovered []runtimeLocalSkillSummary
	if policy == skillpolicy.AllDisabled && runtimeSkillPolicySupported(provider) {
		// all_disabled has to enumerate what is inherited; follow_external
		// only needs the tool's own disable list, so it pays no directory
		// walk per task.
		skills, supported, err := listRuntimeLocalSkills(provider)
		if err != nil || !supported {
			d.logger.Warn("runtime skills: discovery for all_disabled policy failed; falling back to explicit agent overrides only",
				"runtime_id", task.RuntimeID, "provider", provider, "error", err)
			policy = skillpolicy.AllEnabled
		} else {
			discovered = skills
		}
	}

	// Only follow_external consumes the tool's configuration, and reading it
	// costs a file read, so skip that I/O for the other policies.
	var external *localSkillExternalState
	if policy == skillpolicy.FollowExternal {
		external = loadLocalSkillExternalState(provider)
		if external != nil && !external.known() && d.logger != nil {
			d.logger.Debug("runtime skills: the tool's own skill config is unreadable; following policy as if nothing were disabled",
				"runtime_id", task.RuntimeID, "provider", provider)
		}
	}

	var overrides []DisabledRuntimeSkillData
	if task.Agent != nil {
		// Merge instead of probing for which field the server supports: the
		// tri-state field wins on conflict, and the legacy off-only field still
		// contributes when it is all an older server sent.
		overrides = mergeRuntimeSkillOverrides(task.Agent.DisabledRuntimeSkills, task.Agent.RuntimeSkillOverrides)
	}
	return runtimeSkillDisables(provider, policy, overrides, task.RuntimeID, discovered, external)
}

// mergeRuntimeSkillOverrides unions the legacy and tri-state override lists.
// The tri-state entry wins when both describe the same skill, so a server that
// sends both (off entries in the legacy field, everything in the new one) is
// read exactly as the new field says.
func mergeRuntimeSkillOverrides(legacy, triState []DisabledRuntimeSkillData) []DisabledRuntimeSkillData {
	if len(legacy) == 0 {
		return triState
	}
	if len(triState) == 0 {
		return legacy
	}
	merged := make([]DisabledRuntimeSkillData, 0, len(legacy)+len(triState))
	seen := make(map[string]bool, len(legacy)+len(triState))
	add := func(override DisabledRuntimeSkillData) {
		identity := override.RuntimeID + "\x00" + override.Provider + "\x00" +
			runtimeSkillIdentity(override.Root, override.Key, override.Plugin)
		if seen[identity] {
			return
		}
		seen[identity] = true
		merged = append(merged, override)
	}
	for _, override := range triState {
		add(override)
	}
	for _, override := range legacy {
		add(override)
	}
	return merged
}

// runtimeSkillPolicySupported reports whether this provider has a per-agent
// disable mechanism at all. Other providers keep the pre-policy behaviour:
// only explicit overrides apply.
func runtimeSkillPolicySupported(provider string) bool {
	return provider == "claude" || provider == "codex"
}

// runtimeSkillDisables applies `effective = agent override ?? policy` to the
// skills the daemon knows about. An explicit "on" always wins, so one agent can
// escape an all_disabled workspace; an explicit "off" is emitted even when the
// skip is not currently discovered, preserving the pre-policy behaviour where a
// stale disable still applied.
func runtimeSkillDisables(
	provider, policy string,
	overrides []DisabledRuntimeSkillData,
	runtimeID string,
	discovered []runtimeLocalSkillSummary,
	external *localSkillExternalState,
) []execenv.RuntimeSkillRefForEnv {
	if !runtimeSkillPolicySupported(provider) {
		policy = skillpolicy.AllEnabled
	}
	policy = skillpolicy.Normalize(policy)

	refs := make([]execenv.RuntimeSkillRefForEnv, 0, len(overrides)+len(discovered))
	seen := make(map[string]bool)
	add := func(ref execenv.RuntimeSkillRefForEnv) {
		key := strings.Join([]string{ref.Root, ref.Plugin, ref.Key, ref.Name}, "\x00")
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, ref)
	}

	forcedOn := make(map[string]bool, len(overrides))
	forcedOff := make(map[string]bool, len(overrides))
	// Claude's own config is keyed by invocation name while an override is
	// stored under the discovery key, so identity alone cannot match the two
	// (a skill directory named `reviewer-dir` may expose `reviewer`). Track the
	// names too; a name is unique per Claude install, which is exactly the
	// namespace `skillOverrides` uses.
	forcedOnName := make(map[string]bool, len(overrides))
	forcedOffName := make(map[string]bool, len(overrides))
	for _, override := range overrides {
		if override.RuntimeID != runtimeID || override.Provider != provider {
			continue
		}
		identity := runtimeSkillIdentity(override.Root, override.Key, override.Plugin)
		if identity == "" {
			continue
		}
		name := strings.TrimSpace(override.Name)
		if override.Mode == runtimeSkillOverrideModeOn {
			forcedOn[identity] = true
			if name != "" {
				forcedOnName[name] = true
			}
			continue
		}
		forcedOff[identity] = true
		if name != "" {
			forcedOffName[name] = true
		}
		add(execenv.RuntimeSkillRefForEnv{Root: override.Root, Key: override.Key, Name: override.Name, Plugin: override.Plugin})
	}

	inherits := func(root, key, plugin string) bool {
		identity := runtimeSkillIdentity(root, key, plugin)
		return identity != "" && !forcedOn[identity] && !forcedOff[identity]
	}

	switch policy {
	case skillpolicy.AllDisabled:
		for _, skill := range discovered {
			if !inherits(skill.Root, skill.Key, skill.Plugin) {
				continue
			}
			add(execenv.RuntimeSkillRefForEnv{Root: skill.Root, Key: skill.Key, Name: skill.Name, Plugin: skill.Plugin})
		}
	case skillpolicy.FollowExternal:
		for _, ref := range external.disabledRefs() {
			identity := runtimeSkillIdentity(ref.Root, ref.Key, ref.Plugin)
			if identity != "" && (forcedOn[identity] || forcedOff[identity]) {
				continue
			}
			if name := strings.TrimSpace(ref.Name); name != "" && (forcedOnName[name] || forcedOffName[name]) {
				continue
			}
			add(ref)
		}
	}

	sort.Slice(refs, func(i, j int) bool {
		left := strings.Join([]string{refs[i].Root, refs[i].Plugin, refs[i].Key, refs[i].Name}, "\x00")
		right := strings.Join([]string{refs[j].Root, refs[j].Plugin, refs[j].Key, refs[j].Name}, "\x00")
		return left < right
	})
	return refs
}

// runtimeSkillIdentity is the comparable identity of one discovered skill:
// root + plugin + key. Name is display-only and deliberately excluded so a
// rename of the invocation name does not orphan a stored override.
func runtimeSkillIdentity(root, key, plugin string) string {
	root = strings.TrimSpace(root)
	key = strings.TrimSpace(key)
	plugin = strings.TrimSpace(plugin)
	if key == "" {
		return ""
	}
	return root + "\x00" + plugin + "\x00" + key
}

// workspaceRuntimeSkillsDefaultPolicy reads the workspace's default policy from
// the settings the daemon already caches (see workspaceState.settings). Any
// missing, malformed, or unknown value falls back to the documented default
// instead of changing inheritance behaviour.
func (d *Daemon) workspaceRuntimeSkillsDefaultPolicy(workspaceID string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ws, ok := d.workspaces[workspaceID]
	if !ok || len(ws.settings) == 0 {
		return skillpolicy.Default
	}
	var settings struct {
		RuntimeSkillsDefaultPolicy string `json:"runtime_skills_default_policy"`
	}
	if err := json.Unmarshal(ws.settings, &settings); err != nil {
		return skillpolicy.Default
	}
	return skillpolicy.Normalize(settings.RuntimeSkillsDefaultPolicy)
}
