package handler

import (
	"crypto/rand"
	"encoding/json"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Official public client for the Multica CLI. RFC 8628 requires a client_id;
// we have no OAuth client registry, so this well-known public client is the
// only accepted value on the device-authorization and token endpoints.
const deviceAuthClientID = "multica-cli"

// Display name stored on the authorization row and shown on /device.
const deviceAuthPublicClientName = "Multica CLI"

// RFC 8628 §3.4 grant type. Token polls that do not send this exact value
// are rejected as unsupported_grant_type.
const deviceAuthGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// deviceAuthorizationTTL matches the existing email verification-code TTL —
// no new expiry policy to justify (see design doc).
const deviceAuthorizationTTL = 10 * time.Minute

// deviceAuthPollInterval is the interval (seconds) the CLI is told to poll
// at, and the minimum spacing this server enforces between polls on the
// same device_code before responding slow_down (RFC 8628 §3.5).
const deviceAuthPollInterval = 5

// deviceAuthPATExpiryDays matches the browser login flow's existing PAT
// lifetime (cmd_auth.go's runAuthLoginBrowser) — device-code login produces
// the same kind of credential, not a shorter- or longer-lived one.
const deviceAuthPATExpiryDays = 90

// deviceUserCodeAlphabet excludes visually-ambiguous characters (0/O, 1/I/L)
// since a human reads this off one screen and types it into another.
const deviceUserCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func generateDeviceCode() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mdc_" + auth.HashToken(string(b))[:43], nil
}

// generateUserCode returns an 8-character human-typable code formatted
// XXXX-XXXX, drawn from deviceUserCodeAlphabet via crypto/rand.
func generateUserCode() (string, error) {
	const length = 8
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, by := range b {
		if i == 4 {
			sb.WriteByte('-')
		}
		sb.WriteByte(deviceUserCodeAlphabet[int(by)%len(deviceUserCodeAlphabet)])
	}
	return sb.String(), nil
}

// normalizeUserCode applies RFC 8628 §6.1 input hygiene: uppercase, strip
// dashes/spaces/other punctuation, then re-insert the XXXX-XXXX grouping
// used at rest. Returns "" when the cleaned value is not 8 alphabet chars.
func normalizeUserCode(raw string) string {
	var cleaned strings.Builder
	for _, r := range strings.ToUpper(raw) {
		if strings.ContainsRune(deviceUserCodeAlphabet, r) {
			cleaned.WriteRune(r)
		}
	}
	out := cleaned.String()
	if len(out) != 8 {
		return ""
	}
	return out[:4] + "-" + out[4:]
}

func isFormURLEncoded(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	media, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return media == "application/x-www-form-urlencoded"
}

func writeOAuthError(w http.ResponseWriter, code string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
}

func parseDeviceForm(w http.ResponseWriter, r *http.Request) bool {
	if !isFormURLEncoded(r) {
		writeOAuthError(w, "invalid_request")
		return false
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, "invalid_request")
		return false
	}
	return true
}

func requireDeviceClientID(w http.ResponseWriter, r *http.Request) bool {
	clientID := strings.TrimSpace(r.PostForm.Get("client_id"))
	if clientID == "" {
		writeOAuthError(w, "invalid_request")
		return false
	}
	if clientID != deviceAuthClientID {
		writeOAuthError(w, "invalid_client")
		return false
	}
	return true
}

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// RequestDeviceCode starts an RFC 8628 device authorization flow. No auth
// required — this is the entry point a not-yet-logged-in CLI calls.
func (h *Handler) RequestDeviceCode(w http.ResponseWriter, r *http.Request) {
	if !parseDeviceForm(w, r) {
		return
	}
	if !requireDeviceClientID(w, r) {
		return
	}
	// scope is accepted and ignored — scoped grants are a non-goal.

	rawDeviceCode, err := generateDeviceCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate device code")
		return
	}
	userCode, err := generateUserCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate user code")
		return
	}

	expiresAt := pgtype.Timestamptz{Time: time.Now().Add(deviceAuthorizationTTL), Valid: true}
	_, err = h.Queries.CreateDeviceAuthorization(r.Context(), db.CreateDeviceAuthorizationParams{
		DeviceCodeHash: auth.HashToken(rawDeviceCode),
		UserCode:       userCode,
		ClientHint:     deviceAuthPublicClientName,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create device authorization")
		return
	}

	appURL := deviceAuthAppURL()
	verificationURI := appURL + "/device"
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, deviceCodeResponse{
		DeviceCode:              rawDeviceCode,
		UserCode:                userCode,
		VerificationURI:         verificationURI,
		VerificationURIComplete: verificationURI + "?user_code=" + userCode,
		ExpiresIn:               int(deviceAuthorizationTTL.Seconds()),
		Interval:                deviceAuthPollInterval,
	})

	_ = h.Queries.DeleteExpiredDeviceAuthorizations(r.Context())
}

func deviceAuthAppURL() string {
	appURL := strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN"))
	if appURL == "" {
		return "https://www.leagent.me"
	}
	return appURL
}

type devicePendingResponse struct {
	ClientHint string `json:"client_hint"`
	CreatedAt  string `json:"created_at"`
}

// GetPendingDeviceAuthorization backs the App's confirmation page. Requires
// a logged-in human — any workspace member may confirm a device login for
// their own account, there is no workspace scoping here (a PAT is
// user-scoped, not workspace-scoped).
func (h *Handler) GetPendingDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	userCode := normalizeUserCode(r.URL.Query().Get("user_code"))
	if userCode == "" {
		writeError(w, http.StatusBadRequest, "user_code is required")
		return
	}

	da, err := h.Queries.GetDeviceAuthorizationByUserCode(r.Context(), userCode)
	if err != nil || da.Status != "pending" {
		// Expired, unknown, and already-resolved codes all collapse to the
		// same 404 — do not let a prober distinguish "wrong code" from
		// "expired code" (same non-enumeration discipline VerifyCode follows
		// for email codes). The query itself is status-agnostic (see its
		// comment — ConfirmDeviceAuthorization needs that), so this handler
		// checks status itself.
		writeError(w, http.StatusNotFound, "device authorization not found")
		return
	}

	writeJSON(w, http.StatusOK, devicePendingResponse{
		ClientHint: da.ClientHint,
		CreatedAt:  timestampToString(da.CreatedAt),
	})
}

type confirmDeviceAuthorizationRequest struct {
	UserCode string `json:"user_code"`
	Approve  bool   `json:"approve"`
}

type confirmDeviceAuthorizationResponse struct {
	Status string `json:"status"`
}

// ConfirmDeviceAuthorization approves or denies a pending device login.
// Requires a logged-in human — the resulting PAT is minted for whoever
// confirms, same as clicking "Authorize" in any RFC 8628 flow.
func (h *Handler) ConfirmDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req confirmDeviceAuthorizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userCode := normalizeUserCode(req.UserCode)
	if userCode == "" {
		writeError(w, http.StatusBadRequest, "user_code is required")
		return
	}

	da, err := h.Queries.GetDeviceAuthorizationByUserCode(r.Context(), userCode)
	if err != nil {
		writeError(w, http.StatusNotFound, "device authorization not found")
		return
	}

	if !req.Approve {
		if _, err := h.Queries.DenyDeviceAuthorization(r.Context(), da.ID); err != nil && !isNotFound(err) {
			writeError(w, http.StatusInternalServerError, "failed to deny device authorization")
			return
		}
		writeJSON(w, http.StatusOK, confirmDeviceAuthorizationResponse{Status: "denied"})
		return
	}

	// Only flips status + records who approved. The PAT itself is minted
	// later, when the CLI actually claims it (IssueDeviceToken) — see that
	// query's comment for why. Idempotent: a double-confirm (double-click,
	// back button) matches zero rows here (WHERE status='pending') and is
	// treated as an already-resolved no-op.
	if _, err := h.Queries.ApproveDeviceAuthorization(r.Context(), db.ApproveDeviceAuthorizationParams{
		ID:               da.ID,
		ApprovedByUserID: parseUUID(userID),
	}); err != nil && !isNotFound(err) {
		writeError(w, http.StatusInternalServerError, "failed to approve device authorization")
		return
	}

	writeJSON(w, http.StatusOK, confirmDeviceAuthorizationResponse{Status: "approved"})
}

type issueDeviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// IssueDeviceToken is the CLI's poll endpoint. No auth required — the
// device_code itself is the bearer secret (RFC 8628 §3.4-3.5). On a
// successful claim this is where the PAT is actually minted (see
// ClaimDeviceAuthorizationToken's comment) — the raw value is generated,
// hashed for storage, and returned in this one response; it is never
// persisted in raw form and this handler never returns it again after this
// call.
func (h *Handler) IssueDeviceToken(w http.ResponseWriter, r *http.Request) {
	if !parseDeviceForm(w, r) {
		return
	}
	grantType := strings.TrimSpace(r.PostForm.Get("grant_type"))
	if grantType != deviceAuthGrantType {
		writeOAuthError(w, "unsupported_grant_type")
		return
	}
	if !requireDeviceClientID(w, r) {
		return
	}
	rawDeviceCode := strings.TrimSpace(r.PostForm.Get("device_code"))
	if rawDeviceCode == "" {
		writeOAuthError(w, "invalid_request")
		return
	}

	da, err := h.Queries.GetDeviceAuthorizationByDeviceCodeHash(r.Context(), auth.HashToken(rawDeviceCode))
	if err != nil {
		writeOAuthError(w, "expired_token")
		return
	}

	polled, err := h.Queries.MarkDeviceAuthorizationPolled(r.Context(), da.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record poll")
		return
	}
	if polled.PreviousPolledAt.Valid && time.Since(polled.PreviousPolledAt.Time) < deviceAuthPollInterval*time.Second {
		writeOAuthError(w, "slow_down")
		return
	}

	switch da.Status {
	case "denied":
		writeOAuthError(w, "access_denied")
		return
	case "pending":
		writeOAuthError(w, "authorization_pending")
		return
	}

	rawToken, err := auth.GeneratePATToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	prefix := rawToken
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	patName := "CLI"
	if da.ClientHint != "" {
		patName = "CLI (" + da.ClientHint + ")"
	}
	pat, err := h.Queries.CreatePersonalAccessToken(r.Context(), db.CreatePersonalAccessTokenParams{
		UserID:      da.ApprovedByUserID,
		Name:        patName,
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: prefix,
		ExpiresAt: pgtype.Timestamptz{
			Time:  time.Now().Add(deviceAuthPATExpiryDays * 24 * time.Hour),
			Valid: true,
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	if _, err := h.Queries.ClaimDeviceAuthorizationToken(r.Context(), db.ClaimDeviceAuthorizationTokenParams{
		ID:            da.ID,
		IssuedTokenID: pat.ID,
	}); err != nil {
		// Already claimed (single-claim guard) or otherwise no longer
		// claimable — a concurrent poll on the same device_code won this
		// race. The PAT minted just above is orphaned but harmless (it's
		// simply never revealed to a caller and can be reaped like any
		// other unused PAT); the loser must not retry with a stale success.
		writeOAuthError(w, "expired_token")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, issueDeviceTokenResponse{
		AccessToken: rawToken,
		TokenType:   "Bearer",
		ExpiresIn:   deviceAuthPATExpiryDays * 24 * 3600,
	})
}
