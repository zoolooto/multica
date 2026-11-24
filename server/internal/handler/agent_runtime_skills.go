package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/logger"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const maxRuntimeSkillKeyLength = 512

// Per-agent runtime skill override modes. An absent entry means "inherit the
// workspace's runtime_skills_default_policy"; "off" and "on" are explicit
// exceptions. Rows written before tri-state existed carry no Mode and mean
// "off", so a user's current configuration keeps its meaning (GH #10776).
const (
	RuntimeSkillModeInherit = "inherit"
	RuntimeSkillModeOn      = "on"
	RuntimeSkillModeOff     = "off"
)

// DisabledRuntimeSkill identifies one runtime-local skill and how this agent
// treats it. RuntimeID scopes the choice to a single machine/runtime so a
// same-named skill on another runtime is unaffected.
type DisabledRuntimeSkill struct {
	RuntimeID string `json:"runtime_id"`
	Provider  string `json:"provider"`
	Root      string `json:"root"`
	Key       string `json:"key"`
	Name      string `json:"name,omitempty"`
	Plugin    string `json:"plugin,omitempty"`
	// Mode is "on" or "off". Empty is the pre-tri-state spelling of "off" and
	// is serialized that way so nothing changes for existing rows or clients.
	Mode string `json:"mode,omitempty"`
}

// resolveRuntimeSkillToggleMode maps a toggle request onto an override mode.
// The legacy `enabled` boolean keeps its pre-tri-state meaning: false stored a
// disable, true removed it (which is inheritance, not a forced-on override).
func resolveRuntimeSkillToggleMode(rawMode string, enabled *bool) (string, bool) {
	switch mode := strings.ToLower(strings.TrimSpace(rawMode)); mode {
	case RuntimeSkillModeInherit, RuntimeSkillModeOn, RuntimeSkillModeOff:
		return mode, true
	case "":
	default:
		return "", false
	}
	if enabled == nil {
		return "", false
	}
	if *enabled {
		return RuntimeSkillModeInherit, true
	}
	return RuntimeSkillModeOff, true
}

// runtimeSkillMode normalizes a stored override to RuntimeSkillModeOn or
// RuntimeSkillModeOff. Unknown values fall back to "off": a permissive reading
// would silently enable a skill the user had disabled.
func runtimeSkillMode(skill DisabledRuntimeSkill) string {
	if strings.EqualFold(strings.TrimSpace(skill.Mode), RuntimeSkillModeOn) {
		return RuntimeSkillModeOn
	}
	return RuntimeSkillModeOff
}

func decodeDisabledRuntimeSkills(raw []byte) []DisabledRuntimeSkill {
	if len(raw) == 0 {
		return []DisabledRuntimeSkill{}
	}
	var skills []DisabledRuntimeSkill
	if err := json.Unmarshal(raw, &skills); err != nil || skills == nil {
		return []DisabledRuntimeSkill{}
	}
	return skills
}

// normalizeRuntimeSkillOverrides fills in the effective mode of every stored
// override: "on" stays "on", everything else becomes the explicit "off" that
// its presence already meant. Callers then filter by mode instead of
// re-deriving the meaning of an empty Mode.
func normalizeRuntimeSkillOverrides(raw []byte) []DisabledRuntimeSkill {
	all := decodeDisabledRuntimeSkills(raw)
	result := make([]DisabledRuntimeSkill, 0, len(all))
	for _, skill := range all {
		skill.Mode = runtimeSkillMode(skill)
		result = append(result, skill)
	}
	return result
}

// runtimeSkillOverridesFor returns every override that applies to one runtime
// and provider, in both directions. The claim wire needs the forced-on entries
// too: they are how an agent escapes an all_disabled workspace policy.
func runtimeSkillOverridesFor(raw []byte, runtimeID, provider string) []DisabledRuntimeSkill {
	all := normalizeRuntimeSkillOverrides(raw)
	result := make([]DisabledRuntimeSkill, 0, len(all))
	for _, skill := range all {
		if skill.RuntimeID == runtimeID && skill.Provider == provider {
			result = append(result, skill)
		}
	}
	return result
}

// disabledRuntimeSkillOverrides keeps only the off entries. The agent response
// exposes these through the pre-tri-state `disabled_runtime_skills` field, so
// clients that predate tri-state keep seeing exactly the entries whose meaning
// they understand.
func disabledRuntimeSkillOverrides(all []DisabledRuntimeSkill) []DisabledRuntimeSkill {
	result := make([]DisabledRuntimeSkill, 0, len(all))
	for _, skill := range all {
		if runtimeSkillMode(skill) == RuntimeSkillModeOn {
			continue
		}
		skill.Mode = ""
		result = append(result, skill)
	}
	return result
}

func normalizeRuntimeSkillIdentity(root, key, plugin string) (string, string, string, bool) {
	root = strings.TrimSpace(root)
	key = strings.TrimSpace(key)
	plugin = strings.TrimSpace(plugin)
	if len(key) == 0 || len(key) > maxRuntimeSkillKeyLength {
		return "", "", "", false
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(key)))
	if cleaned == "." || filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", "", "", false
	}
	switch root {
	case "provider", "universal":
		plugin = ""
	case "plugin":
		if plugin == "" {
			return "", "", "", false
		}
	default:
		return "", "", "", false
	}
	return root, cleaned, plugin, true
}

func sameDisabledRuntimeSkill(a, b DisabledRuntimeSkill) bool {
	return a.RuntimeID == b.RuntimeID && a.Provider == b.Provider &&
		a.Root == b.Root && a.Key == b.Key && a.Plugin == b.Plugin
}

// SetAgentRuntimeSkillEnabled persists a per-agent override for a skill that
// is discovered from the agent's currently assigned local runtime.
func (h *Handler) SetAgentRuntimeSkillEnabled(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}

	var req struct {
		RuntimeID string `json:"runtime_id"`
		Root      string `json:"root"`
		Key       string `json:"key"`
		Name      string `json:"name"`
		Plugin    string `json:"plugin"`
		Enabled   *bool  `json:"enabled"`
		Mode      string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "runtime_id, root, key, and mode are required")
		return
	}
	mode, ok := resolveRuntimeSkillToggleMode(req.Mode, req.Enabled)
	if !ok {
		writeError(w, http.StatusBadRequest, "runtime_id, root, key, and mode are required")
		return
	}
	runtimeID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
	if !ok {
		return
	}
	if !agent.RuntimeID.Valid || agent.RuntimeID != runtimeID {
		writeError(w, http.StatusConflict, "agent is no longer assigned to this runtime")
		return
	}
	rt, err := h.getAgentRuntime(r.Context(), obsmetrics.RuntimeLookupSourceRuntimeAPI, runtimeID)
	if err != nil || rt.WorkspaceID != agent.WorkspaceID {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	if rt.RuntimeMode != "local" || (rt.Provider != "codex" && rt.Provider != "claude") {
		writeError(w, http.StatusBadRequest, "runtime skill controls are only supported for codex and claude")
		return
	}
	root, key, plugin, valid := normalizeRuntimeSkillIdentity(req.Root, req.Key, req.Plugin)
	if !valid || (root == "plugin" && rt.Provider != "claude") {
		writeError(w, http.StatusBadRequest, "invalid runtime skill identity")
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > maxRuntimeSkillKeyLength {
		writeError(w, http.StatusBadRequest, "invalid runtime skill name")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.GetAgentForUpdate(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	if !locked.RuntimeID.Valid || locked.RuntimeID != runtimeID {
		writeError(w, http.StatusConflict, "agent is no longer assigned to this runtime")
		return
	}

	target := DisabledRuntimeSkill{
		RuntimeID: req.RuntimeID,
		Provider:  rt.Provider,
		Root:      root,
		Key:       key,
		Name:      name,
		Plugin:    plugin,
	}
	current := decodeDisabledRuntimeSkills(locked.DisabledRuntimeSkills)
	next := make([]DisabledRuntimeSkill, 0, len(current)+1)
	for _, skill := range current {
		if sameDisabledRuntimeSkill(skill, target) {
			continue
		}
		next = append(next, skill)
	}
	// "inherit" is the absence of an entry; both explicit directions are stored.
	if mode != RuntimeSkillModeInherit {
		target.Mode = mode
		next = append(next, target)
	}
	payload, err := json.Marshal(next)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode runtime skill settings")
		return
	}
	if _, err := qtx.UpdateAgentDisabledRuntimeSkills(r.Context(), db.UpdateAgentDisabledRuntimeSkillsParams{
		ID:                    locked.ID,
		DisabledRuntimeSkills: payload,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update runtime skill")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit")
		return
	}

	// Broadcast the updated agent so every other open web/desktop/mobile client
	// invalidates its cached agent and picks up the new disabled_runtime_skills
	// state. The workspace-skill toggle does the same through
	// writeUpdatedAgentSkills (skill.go); the realtime layer keys off the
	// "agent:status" event to invalidate workspaceKeys.agents. Without this only
	// the initiating tab refreshes and other clients keep showing stale toggles.
	locked.DisabledRuntimeSkills = payload
	resp := h.agentToResponse(locked)
	if err := h.enrichAgentResponseWithTargets(r.Context(), &resp, locked.ID); err != nil {
		slog.Warn("runtime skill toggle: load invocation targets for broadcast failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", agentID)...)
	}
	// agentToResponse initialises Skills as []; reload the junction-table
	// bindings so the broadcast mirrors reality instead of signalling other
	// clients that this agent's skills were cleared (#3459).
	if err := h.attachAgentSkills(r.Context(), &resp, locked.ID); err != nil {
		slog.Warn("runtime skill toggle: load agent skills for broadcast failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", agentID)...)
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(locked.WorkspaceID))
	h.publish(protocol.EventAgentStatus, uuidToString(locked.WorkspaceID), actorType, actorID,
		map[string]any{"agent": broadcastAgentResponse(resp)})

	w.WriteHeader(http.StatusNoContent)
}
