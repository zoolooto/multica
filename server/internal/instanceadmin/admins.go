// Package instanceadmin holds the instance-wide read model that backs the
// self-hosted operator surface, plus the identity check that grants access to
// it.
//
// Multica's authorization model is workspace-scoped: every role lives on a
// workspace membership row, so nothing in the product answers "which
// workspaces / users / agents exist on this instance?". A self-hosted
// operator who runs many teams' workspaces can only answer that with direct
// SQL. This package adds the missing instance-level read model and the
// config-granted identity that may read it — nothing here writes rows, reads
// workspace content (issues, comments, chat), or grants workspace roles.
package instanceadmin

import "strings"

// Admins is the set of account emails that may use the instance-level
// surface, compiled from INSTANCE_ADMIN_EMAILS.
//
// The grant is deliberately email-based and default-off: there is no
// instance role table, no migration, and revoking access is a config change
// plus a restart. An empty set means the feature is closed for every caller,
// not that everyone is an administrator.
type Admins struct {
	emails map[string]struct{}
}

// NewAdmins compiles the configured list. Entries are trimmed and compared
// case-insensitively (email domains are case-insensitive in practice, and
// operators should not have to match the stored casing); empty entries are
// ignored, so a trailing comma cannot accidentally admit the empty string.
func NewAdmins(emails []string) Admins {
	set := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		normalized := normalizeEmail(email)
		if normalized == "" {
			continue
		}
		set[normalized] = struct{}{}
	}
	return Admins{emails: set}
}

// Configured reports whether the operator configured any administrator at all.
// Callers expose the same refusal either way; the distinction only exists so
// a request path can skip the user lookup when the feature is off.
func (a Admins) Configured() bool {
	return len(a.emails) > 0
}

// Allows reports whether email is an instance administrator. The caller must
// pass the account email read from the database, not a client-supplied
// header: only the JWT auth branch stamps an email header, so trusting one
// would silently exclude PAT and CLI sessions.
func (a Admins) Allows(email string) bool {
	if len(a.emails) == 0 {
		return false
	}
	_, ok := a.emails[normalizeEmail(email)]
	return ok
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
