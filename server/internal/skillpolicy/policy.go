// Package skillpolicy holds the workspace-level default policy for
// runtime-inherited skills (GH #10776).
//
// The policy is stored in the workspace settings JSONB under SettingKey and is
// read by the daemon, which is the only place that can see both what a runtime
// discovered and what the runtime tool itself has disabled.
package skillpolicy

import "strings"

// SettingKey is the workspace settings key carrying the default policy.
const SettingKey = "runtime_skills_default_policy"

const (
	// AllEnabled inherits every discovered skill unless the agent overrides it.
	// Multica adds no disable of its own; the runtime tool's own configuration
	// still decides what the tool actually loads.
	AllEnabled = "all_enabled"
	// AllDisabled inherits no discovered skill unless the agent overrides it on.
	AllDisabled = "all_disabled"
	// FollowExternal inherits a skill only when the runtime tool has not
	// disabled it in its own configuration.
	FollowExternal = "follow_external"
)

// Default is the policy used when the workspace has not chosen one. It is the
// "trust the environment the user already configured" default the upstream
// issue asks for.
const Default = FollowExternal

// Normalize maps any stored or client-supplied value onto one of the three
// policies. Empty and unknown values fall back to Default rather than failing:
// an older or newer peer must never make skill inheritance unpredictable.
func Normalize(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case AllEnabled:
		return AllEnabled
	case AllDisabled:
		return AllDisabled
	default:
		return FollowExternal
	}
}

// Valid reports whether value is one of the three accepted policy values.
// It normalizes exactly like Normalize, so a value one caller accepts is not
// silently rewritten by the other. Callers that write the setting use it to
// reject typos instead of storing a value the daemon would reinterpret.
func Valid(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case AllEnabled, AllDisabled, FollowExternal:
		return true
	default:
		return false
	}
}
