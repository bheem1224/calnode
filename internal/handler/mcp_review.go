package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/access"
	"github.com/calnode/calnode/internal/uid"
)

func (h *Handler) agentReviewPolicy(ctx context.Context, userID string) (required, enforced bool, err error) {
	policy, err := h.signinPolicy(ctx)
	if err != nil {
		return true, true, err
	}
	var personal bool
	if err = h.db.QueryRowContext(ctx, `SELECT mcp_review_required FROM users WHERE id=? AND archived_at IS NULL`, userID).Scan(&personal); err != nil {
		return true, true, err
	}
	return personal || policy.MCPReviewEnforced, policy.MCPReviewEnforced, nil
}
func (h *Handler) GetMCPPreferences(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	required, enforced, err := h.agentReviewPolicy(r.Context(), u.ID)
	if err != nil {
		h.writeError(w, 500, "could not load agent policy")
		return
	}
	var personal bool
	_ = h.db.QueryRowContext(r.Context(), `SELECT mcp_review_required FROM users WHERE id=?`, u.ID).Scan(&personal)
	h.writeJSON(w, 200, map[string]any{"review_required": personal, "enforced": enforced, "effective_review_required": required})
}
func (h *Handler) PutMCPPreferences(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if u.SessionID == "" || u.CredentialID != "" || u.ImpersonatorID != "" {
		h.writeError(w, 403, "change review preferences from your own interactive session")
		return
	}
	var in struct {
		Required *bool `json:"review_required"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || in.Required == nil {
		h.writeError(w, 400, "review_required is required")
		return
	}
	_, enforced, err := h.agentReviewPolicy(r.Context(), u.ID)
	if err != nil {
		h.writeError(w, 500, "could not load policy")
		return
	}
	if enforced && !*in.Required {
		h.writeError(w, 403, "company policy requires review")
		return
	}
	if !*in.Required && !h.freshAuthFactor(w, r, u) {
		return
	}
	if _, err = h.db.ExecContext(r.Context(), `UPDATE users SET mcp_review_required=? WHERE id=?`, *in.Required, u.ID); err != nil {
		h.writeError(w, 500, "could not save preferences")
		return
	}
	h.securityAudit(r.Context(), u.ID, "mcp.review_preference", u.ID, map[string]bool{"required": *in.Required})
	h.GetMCPPreferences(w, r)
}

func (h *Handler) submitAgentChanges(ctx context.Context, u AuthUser, operations []agentOperation) (map[string]any, error) {
	if len(operations) == 0 || len(operations) > 100 {
		return nil, errors.New("submit 1 to 100 operations")
	}
	raw, _ := json.Marshal(operations)
	if len(raw) > 1048576 {
		return nil, errors.New("batch is too large")
	}
	var pending int
	if h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_change_requests WHERE user_id=? AND status='pending' AND datetime(expires_at)>datetime('now')`, u.ID).Scan(&pending) != nil || pending >= 50 {
		return nil, errors.New("too many pending change requests")
	}
	plan := make([]plannedOperation, 0, len(operations))
	needsReview := false
	for _, op := range operations {
		spec, err := h.checkAgentOperation(ctx, u, op)
		if err != nil {
			return nil, err
		}
		if spec.method == "GET" {
			return nil, errors.New("read operations must be called directly")
		}
		if op.Name == "create_event_type" || op.Name == "import_event_type" {
			if op.Values == nil {
				op.Values = map[string]any{}
			}
			op.Values["is_active"] = false
			op.Values["is_public"] = false
		}
		item := plannedOperation{agentOperation: op}
		if spec.before != "" {
			before, _, err := h.callAgentOperation(ctx, u, agentOperation{Name: spec.before, Target: op.Target, ChildID: op.ChildID})
			if err != nil {
				return nil, fmt.Errorf("cannot review current state: %w", err)
			}
			item.Before = before
			item.Fingerprint = agentFingerprint(before)
		}
		needsReview = needsReview || spec.sensitive
		plan = append(plan, item)
	}
	required, _, err := h.agentReviewPolicy(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	id := uid.New()
	now := time.Now().UTC()
	raw, _ = json.Marshal(plan)
	_, err = h.db.ExecContext(ctx, `INSERT INTO mcp_change_requests(id,user_id,credential_id,permissions_json,operations_json,created_at,expires_at)VALUES(?,?,?,?,?,?,?)`, id, u.ID, u.CredentialID, u.Permissions.JSON(), string(raw), now.Format(time.RFC3339), now.Add(30*time.Minute).Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	h.securityAudit(ctx, u.ID, "mcp.proposed", id, map[string]any{"count": len(plan), "credential_id": u.CredentialID})
	if needsReview && required {
		return map[string]any{"id": id, "status": "pending", "review_url": h.baseURL + "/admin/connections/review?id=" + id, "expires_at": now.Add(30 * time.Minute).Format(time.RFC3339), "message": "A human must review this immutable proposal in Calnode. Do not claim it has been applied."}, nil
	}
	return h.executeAgentChanges(ctx, id, u.ID, false)
}

type agentChange struct {
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	Created    string             `json:"created_at"`
	Expires    string             `json:"expires_at"`
	Operations []plannedOperation `json:"operations"`
	Results    []any              `json:"results"`
}

func (h *Handler) loadAgentChange(ctx context.Context, id, user string) (agentChange, string, access.Grants, error) {
	var c agentChange
	var ops, results, cred, permissions string
	c.ID = id
	err := h.db.QueryRowContext(ctx, `SELECT status,created_at,expires_at,operations_json,results_json,credential_id,permissions_json FROM mcp_change_requests WHERE id=? AND user_id=?`, id, user).Scan(&c.Status, &c.Created, &c.Expires, &ops, &results, &cred, &permissions)
	if err == nil {
		err = json.Unmarshal([]byte(ops), &c.Operations)
		_ = json.Unmarshal([]byte(results), &c.Results)
	}
	return c, cred, access.Parse(permissions), err
}
func (h *Handler) GetMCPChangeRequest(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if u.CredentialID != "" || u.SessionID == "" {
		h.writeError(w, 403, "interactive review session required")
		return
	}
	c, _, _, err := h.loadAgentChange(r.Context(), r.PathValue("id"), u.ID)
	if err != nil {
		h.writeError(w, 404, "change request not found")
		return
	}
	h.writeJSON(w, 200, c)
}
func (h *Handler) ListMCPChangeRequests(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	rows, err := h.db.QueryContext(r.Context(), `SELECT id,status,created_at,expires_at FROM mcp_change_requests WHERE user_id=? ORDER BY created_at DESC LIMIT 50`, u.ID)
	if err != nil {
		h.writeError(w, 500, "could not load requests")
		return
	}
	defer rows.Close()
	items := []agentChange{}
	for rows.Next() {
		var c agentChange
		if rows.Scan(&c.ID, &c.Status, &c.Created, &c.Expires) == nil {
			items = append(items, c)
		}
	}
	h.writeJSON(w, 200, map[string]any{"items": items})
}
func (h *Handler) DecideMCPChangeRequest(w http.ResponseWriter, r *http.Request) {
	u, _ := userFromContext(r.Context())
	if u.SessionID == "" || u.CredentialID != "" || u.ImpersonatorID != "" {
		h.writeError(w, 403, "only your own interactive session may approve changes")
		return
	}
	var in struct {
		Approve *bool `json:"approve"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || in.Approve == nil {
		h.writeError(w, 400, "approve is required")
		return
	}
	id := r.PathValue("id")
	if !*in.Approve {
		res, err := h.db.ExecContext(r.Context(), `UPDATE mcp_change_requests SET status='rejected' WHERE id=? AND user_id=? AND status='pending'`, id, u.ID)
		if err != nil {
			h.writeError(w, 500, "could not reject")
			return
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			h.writeError(w, 409, "request is no longer pending")
			return
		}
		h.securityAudit(r.Context(), u.ID, "mcp.rejected", id, nil)
		h.writeJSON(w, 200, map[string]string{"status": "rejected"})
		return
	}
	result, err := h.executeAgentChanges(r.Context(), id, u.ID, true)
	if err != nil {
		h.writeError(w, 409, err.Error())
		return
	}
	h.writeJSON(w, 200, result)
}
func (h *Handler) executeAgentChanges(ctx context.Context, id, userID string, human bool) (map[string]any, error) {
	c, cred, grants, err := h.loadAgentChange(ctx, id, userID)
	if err != nil {
		return nil, errors.New("request not found")
	}
	if c.Status != "pending" {
		return nil, errors.New("request already processed")
	}
	expiry, err := time.Parse(time.RFC3339, c.Expires)
	if err != nil || !time.Now().Before(expiry) {
		return nil, errors.New("request expired; prepare a new proposal")
	}
	u, err := h.currentAgentActor(ctx, userID, cred, grants)
	if err != nil {
		return nil, err
	}
	required, _, err := h.agentReviewPolicy(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, op := range c.Operations {
		spec, err := h.checkAgentOperation(ctx, u, op.agentOperation)
		if err != nil {
			return nil, err
		}
		if required && spec.sensitive && !human {
			return nil, errors.New("human review required")
		}
		if op.Fingerprint != "" {
			before, _, err := h.callAgentOperation(ctx, u, agentOperation{Name: spec.before, Target: op.Target, ChildID: op.ChildID})
			if err != nil || agentFingerprint(before) != op.Fingerprint {
				return nil, errors.New("resource changed; prepare a new proposal")
			}
		}
	}
	// Claim once, before any side effects. A crash leaves an explicit 'executing'
	// state requiring operator reconciliation; it never silently replays a batch.
	res, err := h.db.ExecContext(ctx, `UPDATE mcp_change_requests SET status='executing' WHERE id=? AND user_id=? AND status='pending' AND datetime(expires_at)>datetime('now')`, id, userID)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, errors.New("request was already claimed or expired")
	}
	h.securityAudit(ctx, userID, "mcp.executing", id, map[string]any{"human_review": human, "credential_id": cred})
	results := []any{}
	status := "completed"
	for _, op := range c.Operations {
		// Recheck the live credential and role between operations, including revocation.
		u, e := h.currentAgentActor(ctx, userID, cred, grants)
		var value any
		code := 403
		if e == nil {
			value, code, e = h.callAgentOperation(ctx, u, op.agentOperation)
		}
		result := map[string]any{"operation": op.Name, "status": code, "result": value}
		if e != nil {
			result["error"] = e.Error()
			status = "partial"
		}
		results = append(results, result)
		raw, _ := json.Marshal(results)
		if _, err = h.db.ExecContext(ctx, `UPDATE mcp_change_requests SET results_json=? WHERE id=?`, string(raw), id); err != nil {
			return nil, errors.New("execution result could not be recorded; do not retry blindly")
		}
		h.securityAudit(ctx, userID, "mcp.operation", id, map[string]any{"operation": op.Name, "status": code, "credential_id": cred})
		if e != nil {
			break
		}
	}
	if _, err = h.db.ExecContext(ctx, `UPDATE mcp_change_requests SET status=? WHERE id=? AND status='executing'`, status, id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": status, "results": results, "message": "Only completed operations were applied. A partial batch is not atomic; inspect results before preparing a new proposal."}, nil
}
