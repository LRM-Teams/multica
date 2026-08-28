package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MaxProblemEvolutionSecretBytes bounds stored secret material. Hidden answers
// and hidden case sets are small; a large upload here is a sign of misuse.
const MaxProblemEvolutionSecretBytes = 256 * 1024

// ProblemEvolutionSecretResponse is the metadata view of a secret. There is no
// response shape anywhere that carries plaintext back to a user: once stored, a
// hidden answer is only ever readable by a verifier holding a capability.
type ProblemEvolutionSecretResponse struct {
	ID          string  `json:"id"`
	RunID       *string `json:"run_id,omitempty"`
	Kind        string  `json:"kind"`
	Label       string  `json:"label"`
	KeyID       string  `json:"key_id"`
	ContentHash string  `json:"content_hash"`
	Revoked     bool    `json:"revoked"`
	CreatedAt   string  `json:"created_at"`
}

// ProblemEvolutionCapabilityResponse is returned once, at issuance. The token
// is not retrievable afterwards because only its hash is stored.
type ProblemEvolutionCapabilityResponse struct {
	ID        string `json:"id"`
	SecretID  string `json:"secret_id"`
	RunID     string `json:"run_id"`
	Token     string `json:"token"`
	Audience  string `json:"audience"`
	MaxUses   int    `json:"max_uses"`
	ExpiresAt string `json:"expires_at"`
}

type createProblemEvolutionSecretRequest struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Plaintext string `json:"plaintext"`
}

type issueProblemEvolutionCapabilityRequest struct {
	SecretID   string `json:"secret_id"`
	MaxUses    int    `json:"max_uses"`
	TTLSeconds int    `json:"ttl_seconds"`
	IssuedTo   string `json:"issued_to"`
}

type redeemProblemEvolutionCapabilityRequest struct {
	Token string `json:"token"`
	RunID string `json:"run_id"`
}

// loadProblemEvolutionRunWithMember resolves the run and the acting member, so
// a secret write is always attributable to a workspace member in the audit.
func (h *Handler) loadProblemEvolutionRunWithMember(
	w http.ResponseWriter,
	r *http.Request,
) (db.ProblemEvolutionRun, db.Member, bool) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return db.ProblemEvolutionRun{}, db.Member{}, false
	}
	member, ok := h.workspaceMember(w, r, ctxWorkspaceID(r.Context()))
	if !ok {
		return db.ProblemEvolutionRun{}, db.Member{}, false
	}
	return run, member, true
}

// CreateProblemEvolutionSecret stores sealed secret material for a run.
func (h *Handler) CreateProblemEvolutionSecret(w http.ResponseWriter, r *http.Request) {
	run, member, ok := h.loadProblemEvolutionRunWithMember(w, r)
	if !ok {
		return
	}
	var req createProblemEvolutionSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	plaintext := req.Plaintext
	if strings.TrimSpace(plaintext) == "" {
		writeError(w, http.StatusBadRequest, "plaintext is required")
		return
	}
	if len(plaintext) > MaxProblemEvolutionSecretBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "secret is too large")
		return
	}
	kind := req.Kind
	if kind == "" {
		kind = "hidden_answer"
	}
	sealer, err := h.problemEvolutionSealer()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "secret storage is not configured")
		return
	}
	sealed, err := sealer.Seal([]byte(plaintext))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal secret")
		return
	}
	secret, err := h.Queries.CreateProblemEvolutionSecret(r.Context(), db.CreateProblemEvolutionSecretParams{
		WorkspaceID:     run.WorkspaceID,
		RunID:           run.ID,
		Kind:            kind,
		Label:           problemevolution.TruncateFreeText(req.Label),
		Ciphertext:      sealed.Ciphertext,
		Nonce:           sealed.Nonce,
		WrappedKey:      sealed.WrappedKey,
		WrappedKeyNonce: sealed.WrappedKeyNonce,
		KeyID:           sealed.KeyID,
		ContentHash:     sealed.ContentHash,
		CreatedBy:       member.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store secret")
		return
	}
	h.auditProblemEvolutionSecret(r.Context(), problemEvolutionSecretAudit{
		WorkspaceID: run.WorkspaceID,
		SecretID:    secret.ID,
		RunID:       run.ID,
		Action:      "created",
		ActorType:   "user",
		ActorID:     uuidToString(member.UserID),
	})
	writeJSON(w, http.StatusCreated, problemEvolutionSecretToResponse(secret))
}

// ListProblemEvolutionSecrets returns secret metadata for a run.
func (h *Handler) ListProblemEvolutionSecrets(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionSecretsByRun(r.Context(), db.ListProblemEvolutionSecretsByRunParams{
		WorkspaceID: run.WorkspaceID,
		RunID:       run.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list secrets")
		return
	}
	resp := make([]ProblemEvolutionSecretResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, ProblemEvolutionSecretResponse{
			ID:          uuidToString(row.ID),
			RunID:       uuidToPtr(row.RunID),
			Kind:        row.Kind,
			Label:       row.Label,
			KeyID:       row.KeyID,
			ContentHash: row.ContentHash,
			Revoked:     row.RevokedAt.Valid,
			CreatedAt:   timestampToString(row.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// RevokeProblemEvolutionSecret revokes a secret and every capability issued
// against it, which is the break-glass path when material has leaked.
func (h *Handler) RevokeProblemEvolutionSecret(w http.ResponseWriter, r *http.Request) {
	run, member, ok := h.loadProblemEvolutionRunWithMember(w, r)
	if !ok {
		return
	}
	secretID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "secretId"), "secret_id")
	if !ok {
		return
	}
	revoked, err := h.Queries.RevokeProblemEvolutionSecret(r.Context(), db.RevokeProblemEvolutionSecretParams{
		ID:          secretID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "secret not found or already revoked")
		return
	}
	if _, err := h.Queries.RevokeProblemEvolutionSecretCapabilitiesForRun(r.Context(), run.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke capabilities")
		return
	}
	h.auditProblemEvolutionSecret(r.Context(), problemEvolutionSecretAudit{
		WorkspaceID: run.WorkspaceID,
		SecretID:    revoked.ID,
		RunID:       run.ID,
		Action:      "revoked",
		ActorType:   "user",
		ActorID:     uuidToString(member.UserID),
	})
	writeJSON(w, http.StatusOK, problemEvolutionSecretToResponse(revoked))
}

// IssueProblemEvolutionCapability mints a short-lived verifier capability.
func (h *Handler) IssueProblemEvolutionCapability(w http.ResponseWriter, r *http.Request) {
	run, member, ok := h.loadProblemEvolutionRunWithMember(w, r)
	if !ok {
		return
	}
	var req issueProblemEvolutionCapabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	secretID, ok := parseUUIDOrBadRequest(w, req.SecretID, "secret_id")
	if !ok {
		return
	}
	secret, err := h.Queries.GetProblemEvolutionSecret(r.Context(), db.GetProblemEvolutionSecretParams{
		ID:          secretID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "secret not found")
		return
	}
	if secret.RevokedAt.Valid {
		writeError(w, http.StatusConflict, "secret is revoked")
		return
	}
	maxUses := req.MaxUses
	if maxUses <= 0 {
		maxUses = 1
	}
	ttl := problemevolution.CapabilityTTL
	// A caller may shorten the window but never extend it: a long-lived
	// capability is a standing read grant on the hidden answer.
	if req.TTLSeconds > 0 && time.Duration(req.TTLSeconds)*time.Second < ttl {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	token, tokenHash, err := problemevolution.NewCapabilityToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mint capability")
		return
	}
	capability, err := h.Queries.CreateProblemEvolutionSecretCapability(r.Context(), db.CreateProblemEvolutionSecretCapabilityParams{
		SecretID:    secret.ID,
		RunID:       run.ID,
		WorkspaceID: run.WorkspaceID,
		TokenHash:   tokenHash,
		Audience:    problemevolution.AudienceVerifier,
		MaxUses:     int32(maxUses),
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
		IssuedTo:    problemevolution.TruncateFreeText(req.IssuedTo),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue capability")
		return
	}
	h.auditProblemEvolutionSecret(r.Context(), problemEvolutionSecretAudit{
		WorkspaceID:  run.WorkspaceID,
		SecretID:     secret.ID,
		CapabilityID: capability.ID,
		RunID:        run.ID,
		Action:       "issued",
		ActorType:    "user",
		ActorID:      uuidToString(member.UserID),
	})
	writeJSON(w, http.StatusCreated, ProblemEvolutionCapabilityResponse{
		ID:        uuidToString(capability.ID),
		SecretID:  uuidToString(secret.ID),
		RunID:     uuidToString(run.ID),
		Token:     token,
		Audience:  capability.Audience,
		MaxUses:   int(capability.MaxUses),
		ExpiresAt: timestampToString(capability.ExpiresAt),
	})
}

// RedeemProblemEvolutionCapability exchanges a capability for plaintext. This is
// the only path in the system that returns hidden material, it is daemon-facing,
// and every outcome — including each denial — is audited.
func (h *Handler) RedeemProblemEvolutionCapability(w http.ResponseWriter, r *http.Request) {
	var req redeemProblemEvolutionCapabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !problemevolution.IsValidCapabilityToken(req.Token) {
		writeError(w, http.StatusForbidden, "capability denied")
		return
	}
	row, err := h.Queries.GetProblemEvolutionSecretCapabilityByTokenHash(r.Context(),
		problemevolution.HashCapabilityToken(req.Token))
	if err != nil {
		// An unknown token has no workspace to audit against; refuse without
		// revealing whether the token ever existed.
		writeError(w, http.StatusForbidden, "capability denied")
		return
	}
	state := problemevolution.CapabilityState{
		Audience:      row.Audience,
		RunID:         uuidToString(row.RunID),
		MaxUses:       int(row.MaxUses),
		Uses:          int(row.Uses),
		Revoked:       row.RevokedAt.Valid,
		SecretRevoked: row.SecretRevokedAt.Valid,
	}
	if row.ExpiresAt.Valid {
		state.ExpiresAt = row.ExpiresAt.Time
	}
	if err := problemevolution.CheckCapability(state, req.RunID, time.Now()); err != nil {
		h.denyProblemEvolutionCapability(r.Context(), row, problemevolution.DenialReason(err))
		writeError(w, http.StatusForbidden, "capability denied")
		return
	}
	consumed, err := h.Queries.ConsumeProblemEvolutionSecretCapability(r.Context(), row.TokenHash)
	if err != nil {
		// Losing the race for the last use is a denial, not an error: the
		// counter is authoritative, not the pre-check above.
		h.denyProblemEvolutionCapability(r.Context(), row, problemevolution.DenyReasonExhausted)
		writeError(w, http.StatusForbidden, "capability denied")
		return
	}
	secret, err := h.Queries.GetProblemEvolutionSecret(r.Context(), db.GetProblemEvolutionSecretParams{
		ID:          row.SecretID,
		WorkspaceID: row.WorkspaceID,
	})
	if err != nil {
		h.denyProblemEvolutionCapability(r.Context(), row, problemevolution.DenyReasonSecretRevoked)
		writeError(w, http.StatusForbidden, "capability denied")
		return
	}
	sealer, err := h.problemEvolutionSealer()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "secret storage is not configured")
		return
	}
	plaintext, err := sealer.Open(problemevolution.SealedSecret{
		Ciphertext:      secret.Ciphertext,
		Nonce:           secret.Nonce,
		WrappedKey:      secret.WrappedKey,
		WrappedKeyNonce: secret.WrappedKeyNonce,
		KeyID:           secret.KeyID,
	})
	if err != nil {
		h.denyProblemEvolutionCapability(r.Context(), row, problemevolution.DenyReasonSecretNotSealed)
		writeError(w, http.StatusInternalServerError, "failed to open secret")
		return
	}
	h.auditProblemEvolutionSecret(r.Context(), problemEvolutionSecretAudit{
		WorkspaceID:  row.WorkspaceID,
		SecretID:     secret.ID,
		CapabilityID: consumed.ID,
		RunID:        row.RunID,
		Action:       "redeemed",
		ActorType:    "verifier",
		ActorID:      row.IssuedTo,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":         secret.Kind,
		"content_hash": secret.ContentHash,
		"plaintext":    string(plaintext),
		"uses":         consumed.Uses,
		"max_uses":     consumed.MaxUses,
	})
}

// ListProblemEvolutionSecretAudit exposes the audit trail for a run.
func (h *Handler) ListProblemEvolutionSecretAudit(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionSecretAudit(r.Context(), db.ListProblemEvolutionSecretAuditParams{
		RunID:       run.ID,
		ResultLimit: 200,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list audit trail")
		return
	}
	resp := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, map[string]any{
			"id":         uuidToString(row.ID),
			"action":     row.Action,
			"reason":     row.Reason,
			"actor_type": row.ActorType,
			"actor_id":   row.ActorID,
			"created_at": timestampToString(row.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

type problemEvolutionSecretAudit struct {
	WorkspaceID  pgtype.UUID
	SecretID     pgtype.UUID
	CapabilityID pgtype.UUID
	RunID        pgtype.UUID
	Action       string
	Reason       string
	ActorType    string
	ActorID      string
}

// auditProblemEvolutionSecret records one secret-boundary event. A failure to
// write the audit row must not be turned into a failure to deny, so the error
// is swallowed here and surfaced by the caller's own outcome.
func (h *Handler) auditProblemEvolutionSecret(ctx context.Context, entry problemEvolutionSecretAudit) {
	actorType := entry.ActorType
	if actorType == "" {
		actorType = "system"
	}
	_, _ = h.Queries.InsertProblemEvolutionSecretAudit(ctx, db.InsertProblemEvolutionSecretAuditParams{
		WorkspaceID:  entry.WorkspaceID,
		SecretID:     entry.SecretID,
		CapabilityID: entry.CapabilityID,
		RunID:        entry.RunID,
		Action:       entry.Action,
		Reason:       entry.Reason,
		ActorType:    actorType,
		ActorID:      entry.ActorID,
	})
}

func (h *Handler) denyProblemEvolutionCapability(
	ctx context.Context,
	row db.GetProblemEvolutionSecretCapabilityByTokenHashRow,
	reason string,
) {
	h.auditProblemEvolutionSecret(ctx, problemEvolutionSecretAudit{
		WorkspaceID:  row.WorkspaceID,
		SecretID:     row.SecretID,
		CapabilityID: row.ID,
		RunID:        row.RunID,
		Action:       "denied",
		Reason:       reason,
		ActorType:    "verifier",
		ActorID:      row.IssuedTo,
	})
}

// The sealer is process-wide and resolved once: the master key comes from the
// environment, so re-reading it per request would only add failure modes.
var (
	problemEvolutionSealerOnce  sync.Once
	problemEvolutionSealerValue *problemevolution.Sealer
	problemEvolutionSealerErr   error
)

func (h *Handler) problemEvolutionSealer() (*problemevolution.Sealer, error) {
	problemEvolutionSealerOnce.Do(func() {
		problemEvolutionSealerValue, problemEvolutionSealerErr = problemevolution.NewSealerFromEnv()
	})
	if problemEvolutionSealerErr != nil {
		return nil, problemEvolutionSealerErr
	}
	if problemEvolutionSealerValue == nil {
		return nil, problemevolution.ErrMasterKeyMissing
	}
	return problemEvolutionSealerValue, nil
}

func problemEvolutionSecretToResponse(row db.ProblemEvolutionSecret) ProblemEvolutionSecretResponse {
	return ProblemEvolutionSecretResponse{
		ID:          uuidToString(row.ID),
		RunID:       uuidToPtr(row.RunID),
		Kind:        row.Kind,
		Label:       row.Label,
		KeyID:       row.KeyID,
		ContentHash: row.ContentHash,
		Revoked:     row.RevokedAt.Valid,
		CreatedAt:   timestampToString(row.CreatedAt),
	}
}
