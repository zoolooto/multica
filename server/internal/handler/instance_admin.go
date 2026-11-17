package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/internal/instanceadmin"
	"github.com/multica-ai/multica/server/internal/util"
)

// Instance-level operator surface (self-hosted only).
//
// Everything in Multica is workspace-scoped: roles live on a membership row,
// and every list endpoint takes a workspace id. A self-hosted operator who
// runs the deployment for many teams therefore has no product surface that
// answers "which workspaces, users and agents exist here, and what are they
// costing?" — only direct SQL does. These two endpoints close that gap for
// reading:
//
//	GET /api/instance/access    {"is_instance_admin": bool}
//	GET /api/instance/overview  the instance-wide read model
//
// Access is granted by INSTANCE_ADMIN_EMAILS (handler.Config.InstanceAdminEmails),
// matched against the account email in the database, not against a client
// header: only the JWT auth branch stamps X-User-Email, so a header-based
// check would silently refuse every PAT/CLI session. Machine credentials
// (task tokens, cloud PATs) are refused outright — this is an operator's
// human-facing view, and a prompt-injected agent must not be able to read the
// whole instance through its owner's email.
//
// Deliberately absent: any write action (transfer ownership, disable login,
// archive/delete a workspace) and any read of workspace content (issues,
// comments, chat). Both need their own product decision, confirmation flow
// and audit trail; the upstream issue calls content access a separate
// decision too.

// instanceOverviewDefaultDays matches the workspace dashboard's default
// window; daysParam enforces the same 1..365 range.
const instanceOverviewDefaultDays = 30

type instanceAccessResponse struct {
	IsInstanceAdmin bool `json:"is_instance_admin"`
}

// GetInstanceAccess reports whether the caller may use the instance surface.
// It is a probe for the caller (a future console entry point uses it to decide
// whether to render at all), so a non-administrator gets a plain false rather
// than an error — the endpoint itself reveals nothing about other accounts or
// workspaces.
func (h *Handler) GetInstanceAccess(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, instanceAccessResponse{
		IsInstanceAdmin: h.instanceAdminAllowed(r, userID),
	})
}

// GetInstanceOverview serves the instance-wide read model: workspaces, users,
// agents/runtimes, the windowed activity summary, and a daily usage series.
func (h *Handler) GetInstanceOverview(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !h.instanceAdminAllowed(r, userID) {
		writeError(w, http.StatusForbidden, "instance administration is not available to this account")
		return
	}
	if h.InstanceAdminStore == nil {
		writeError(w, http.StatusServiceUnavailable, "instance overview is unavailable")
		return
	}

	days := daysParam(r, instanceOverviewDefaultDays)
	tz := h.resolveViewingTZ(r)
	// resolveViewingTZ only ever returns a loadable zone (a validated ?tz=, a
	// stored timezone that loads, or "UTC"), so the fallback below is a
	// formality; it keeps the handler honest if that contract changes.
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		tz = "UTC"
		loc = time.UTC
	}
	// Same calendar-day convention as the workspace dashboard: the window
	// covers today plus the previous days-1 days in the viewer's zone.
	since := sinceFromDays(dayWindowNow(), days-1, loc)

	overview, err := h.InstanceAdminStore.Load(r.Context(), since, tz)
	if err != nil {
		slog.Error("instance overview: load failed", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load instance overview")
		return
	}
	overview.WindowDays = days
	overview.Timezone = tz
	overview.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, overview)
}

// instanceAdminAllowed applies the whole identity check in one place.
//
// The order matters for cost and for what leaks: the configuration check
// short-circuits before the user lookup, and the machine-credential refusal
// comes before it too, so an agent token can never even trigger the lookup.
// A missing user row (deleted account with a live token) is a plain refusal.
//
// The route group also carries handler.RequireHumanActor, which is the
// authoritative gate. This check is the fail-closed backstop the actor-guard
// contract asks sensitive handlers to keep, so a future route added to the
// group without the middleware still refuses machine credentials.
func (h *Handler) instanceAdminAllowed(r *http.Request, userID string) bool {
	if !h.InstanceAdmins.Configured() {
		return false
	}
	if isMachineCredentialActor(r) {
		return false
	}
	uid, err := util.ParseUUID(userID)
	if err != nil {
		return false
	}
	user, err := h.Queries.GetUser(r.Context(), uid)
	if err != nil {
		slog.Warn("instance admin: user lookup failed", "path", r.URL.Path, "error", err)
		return false
	}
	return h.InstanceAdmins.Allows(user.Email)
}

// compile-time check that the store satisfies the handler's reader contract.
var _ instanceadmin.Reader = (*instanceadmin.Store)(nil)
