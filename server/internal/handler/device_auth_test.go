package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/auth"
)

// Spec pins for RFC 8628 + the official public CLI client. Tests use these
// literals — not the handler's constants — so a silent rename of the
// accepted client_id or grant cannot keep the suite green.
const (
	rfcDeviceClientID  = "multica-cli"
	rfcDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	rfcPATLifetimeSec  = 90 * 24 * 3600
)

func newDeviceAuthTestUser(t *testing.T, email string) string {
	t.Helper()
	ctx := context.Background()

	var userID string
	handle := strings.ReplaceAll(strings.Split(email, "@")[0], "-", "_")
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, display_name, email) VALUES ($1, $2, $3) RETURNING id`,
		handle, "Device Auth Test", email,
	).Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func newDeviceFormRequest(path string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func requestDeviceCode(t *testing.T) deviceCodeResponse {
	t.Helper()
	req := newDeviceFormRequest("/api/device/code", url.Values{
		"client_id": {rfcDeviceClientID},
		"scope":     {""},
	})
	w := httptest.NewRecorder()
	testHandler.RequestDeviceCode(w, req)
	if w.Code != 200 {
		t.Fatalf("RequestDeviceCode status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp deviceCodeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM device_authorization WHERE user_code = $1`, resp.UserCode)
	})
	return resp
}

func pollDeviceToken(deviceCode string) *httptest.ResponseRecorder {
	req := newDeviceFormRequest("/api/device/token", url.Values{
		"grant_type":  {rfcDeviceGrantType},
		"device_code": {deviceCode},
		"client_id":   {rfcDeviceClientID},
	})
	w := httptest.NewRecorder()
	testHandler.IssueDeviceToken(w, req)
	return w
}

func TestRequestDeviceCode_FormStartReturnsRFC8628Shape(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	resp := requestDeviceCode(t)

	if resp.DeviceCode == "" || resp.UserCode == "" {
		t.Fatalf("device_code/user_code must be non-empty: %+v", resp)
	}
	if resp.VerificationURI == "" {
		t.Fatalf("verification_uri must be present: %+v", resp)
	}
	if !strings.Contains(resp.VerificationURIComplete, resp.UserCode) {
		t.Fatalf("verification_uri_complete = %q, want it to contain user_code %q", resp.VerificationURIComplete, resp.UserCode)
	}
	if resp.ExpiresIn != int(deviceAuthorizationTTL.Seconds()) {
		t.Fatalf("expires_in = %d, want %d", resp.ExpiresIn, int(deviceAuthorizationTTL.Seconds()))
	}
	if resp.Interval != deviceAuthPollInterval {
		t.Fatalf("interval = %d, want %d", resp.Interval, deviceAuthPollInterval)
	}
}

func TestDeviceAuthAppURLDefaultsToLeAgent(t *testing.T) {
	t.Setenv("FRONTEND_ORIGIN", "")
	if got := deviceAuthAppURL(); got != "https://www.leagent.me" {
		t.Fatalf("deviceAuthAppURL() = %q, want https://www.leagent.me", got)
	}

	t.Setenv("FRONTEND_ORIGIN", "https://self-host.example")
	if got := deviceAuthAppURL(); got != "https://self-host.example" {
		t.Fatalf("deviceAuthAppURL() = %q, want configured self-host origin", got)
	}
}

func TestRequestDeviceCode_RejectsMissingAndUnknownClientID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	missing := newDeviceFormRequest("/api/device/code", url.Values{})
	missingW := httptest.NewRecorder()
	testHandler.RequestDeviceCode(missingW, missing)
	assertDeviceTokenError(t, missingW, "invalid_request")

	unknown := newDeviceFormRequest("/api/device/code", url.Values{"client_id": {"not-a-client"}})
	unknownW := httptest.NewRecorder()
	testHandler.RequestDeviceCode(unknownW, unknown)
	assertDeviceTokenError(t, unknownW, "invalid_client")
}

func TestRequestDeviceCode_RejectsJSONBody(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	req := newRequest("POST", "/api/device/code", map[string]string{
		"client_id":   rfcDeviceClientID,
		"client_hint": "should-not-work",
	})
	w := httptest.NewRecorder()
	testHandler.RequestDeviceCode(w, req)
	assertDeviceTokenError(t, w, "invalid_request")
}

func TestDeviceAuth_PendingIsVisibleToAnyLoggedInUser(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	approver := newDeviceAuthTestUser(t, "device-approver@multica.ai")
	code := requestDeviceCode(t)

	req := newRequestAs(approver, "GET", "/api/device/pending?user_code="+code.UserCode, nil)
	w := httptest.NewRecorder()
	testHandler.GetPendingDeviceAuthorization(w, req)
	if w.Code != 200 {
		t.Fatalf("GetPendingDeviceAuthorization status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp devicePendingResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ClientHint != "Multica CLI" {
		t.Fatalf("client_hint = %q, want %q", resp.ClientHint, "Multica CLI")
	}
}

func TestDeviceAuth_PendingNormalizesTypedUserCode(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	approver := newDeviceAuthTestUser(t, "device-normalize@multica.ai")
	code := requestDeviceCode(t)

	// RFC 6.1: strip punctuation, ignore case.
	typed := strings.ToLower(strings.ReplaceAll(code.UserCode, "-", ""))
	req := newRequestAs(approver, "GET", "/api/device/pending?user_code="+typed, nil)
	w := httptest.NewRecorder()
	testHandler.GetPendingDeviceAuthorization(w, req)
	if w.Code != 200 {
		t.Fatalf("normalized lookup status = %d, body = %s", w.Code, w.Body.String())
	}
}

// TestDeviceAuth_PendingDoesNotDistinguishUnknownFromExpired locks the
// non-enumeration discipline: an unknown user_code and a real-but-expired
// one must return the identical 404, so a prober can't tell them apart.
func TestDeviceAuth_PendingDoesNotDistinguishUnknownFromExpired(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	approver := newDeviceAuthTestUser(t, "device-prober@multica.ai")

	unknownReq := newRequestAs(approver, "GET", "/api/device/pending?user_code=ZZZZ-ZZZZ", nil)
	unknownW := httptest.NewRecorder()
	testHandler.GetPendingDeviceAuthorization(unknownW, unknownReq)

	code := requestDeviceCode(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE device_authorization SET expires_at = now() - interval '1 minute' WHERE user_code = $1`, code.UserCode); err != nil {
		t.Fatalf("force-expire: %v", err)
	}
	expiredReq := newRequestAs(approver, "GET", "/api/device/pending?user_code="+code.UserCode, nil)
	expiredW := httptest.NewRecorder()
	testHandler.GetPendingDeviceAuthorization(expiredW, expiredReq)

	if unknownW.Code != 404 || expiredW.Code != 404 {
		t.Fatalf("status codes = unknown:%d expired:%d, want both 404", unknownW.Code, expiredW.Code)
	}
	if unknownW.Body.String() != expiredW.Body.String() {
		t.Fatalf("bodies differ: unknown=%q expired=%q, want identical (non-enumeration)", unknownW.Body.String(), expiredW.Body.String())
	}
}

// TestDeviceAuth_FullApproveFlowMintsUsableAccessToken is the end-to-end
// happy path: form start → approve → form poll → the returned access_token
// is the minted PAT and authenticates as the approving user via the same
// lookup the HTTP auth middleware uses.
func TestDeviceAuth_FullApproveFlowMintsUsableAccessToken(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	approver := newDeviceAuthTestUser(t, "device-full-flow@multica.ai")
	code := requestDeviceCode(t)

	confirmReq := newRequestAs(approver, "POST", "/api/device/confirm", confirmDeviceAuthorizationRequest{UserCode: code.UserCode, Approve: true})
	confirmW := httptest.NewRecorder()
	testHandler.ConfirmDeviceAuthorization(confirmW, confirmReq)
	if confirmW.Code != 200 {
		t.Fatalf("ConfirmDeviceAuthorization status = %d, body = %s", confirmW.Code, confirmW.Body.String())
	}

	pollW := pollDeviceToken(code.DeviceCode)
	if pollW.Code != 200 {
		t.Fatalf("poll after approve status = %d, body = %s", pollW.Code, pollW.Body.String())
	}
	var tokenResp issueDeviceTokenResponse
	if err := json.Unmarshal(pollW.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tokenResp.AccessToken == "" || tokenResp.TokenType != "Bearer" {
		t.Fatalf("token response = %+v, want access_token + token_type=Bearer", tokenResp)
	}
	if tokenResp.ExpiresIn != rfcPATLifetimeSec {
		t.Fatalf("expires_in = %d, want %d", tokenResp.ExpiresIn, rfcPATLifetimeSec)
	}
	if !strings.HasPrefix(tokenResp.AccessToken, "mul_") {
		t.Fatalf("access_token = %q, want mul_ prefix", tokenResp.AccessToken)
	}

	pat, err := testHandler.Queries.GetPersonalAccessTokenByHash(context.Background(), auth.HashToken(tokenResp.AccessToken))
	if err != nil {
		t.Fatalf("minted access_token does not resolve: %v", err)
	}
	if uuidToString(pat.UserID) != approver {
		t.Fatalf("minted PAT belongs to user %s, want approver %s", uuidToString(pat.UserID), approver)
	}
}

func TestDeviceAuth_PollWhilePendingReturnsAuthorizationPending(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	code := requestDeviceCode(t)

	w := pollDeviceToken(code.DeviceCode)
	assertDeviceTokenError(t, w, "authorization_pending")
}

func TestDeviceAuth_PollAfterDenyReturnsAccessDenied(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	approver := newDeviceAuthTestUser(t, "device-denier@multica.ai")
	code := requestDeviceCode(t)

	denyReq := newRequestAs(approver, "POST", "/api/device/confirm", confirmDeviceAuthorizationRequest{UserCode: code.UserCode, Approve: false})
	denyW := httptest.NewRecorder()
	testHandler.ConfirmDeviceAuthorization(denyW, denyReq)
	if denyW.Code != 200 {
		t.Fatalf("deny status = %d, body = %s", denyW.Code, denyW.Body.String())
	}

	w := pollDeviceToken(code.DeviceCode)
	assertDeviceTokenError(t, w, "access_denied")
}

func TestDeviceAuth_PollAfterExpiryReturnsExpiredToken(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	code := requestDeviceCode(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE device_authorization SET expires_at = now() - interval '1 minute' WHERE user_code = $1`, code.UserCode); err != nil {
		t.Fatalf("force-expire: %v", err)
	}

	w := pollDeviceToken(code.DeviceCode)
	assertDeviceTokenError(t, w, "expired_token")
}

func TestDeviceAuth_PollRejectsWrongGrantAndClient(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	code := requestDeviceCode(t)

	wrongGrant := newDeviceFormRequest("/api/device/token", url.Values{
		"grant_type":  {"authorization_code"},
		"device_code": {code.DeviceCode},
		"client_id":   {rfcDeviceClientID},
	})
	wrongGrantW := httptest.NewRecorder()
	testHandler.IssueDeviceToken(wrongGrantW, wrongGrant)
	assertDeviceTokenError(t, wrongGrantW, "unsupported_grant_type")

	missingClient := newDeviceFormRequest("/api/device/token", url.Values{
		"grant_type":  {rfcDeviceGrantType},
		"device_code": {code.DeviceCode},
	})
	missingClientW := httptest.NewRecorder()
	testHandler.IssueDeviceToken(missingClientW, missingClient)
	assertDeviceTokenError(t, missingClientW, "invalid_request")

	unknownClient := newDeviceFormRequest("/api/device/token", url.Values{
		"grant_type":  {rfcDeviceGrantType},
		"device_code": {code.DeviceCode},
		"client_id":   {"someone-else"},
	})
	unknownClientW := httptest.NewRecorder()
	testHandler.IssueDeviceToken(unknownClientW, unknownClient)
	assertDeviceTokenError(t, unknownClientW, "invalid_client")

	jsonReq := newRequest("POST", "/api/device/token", map[string]string{"device_code": code.DeviceCode})
	jsonW := httptest.NewRecorder()
	testHandler.IssueDeviceToken(jsonW, jsonReq)
	assertDeviceTokenError(t, jsonW, "invalid_request")
}

// TestDeviceAuth_PollTwiceWithinIntervalReturnsSlowDown is the RFC 8628
// §3.5 backoff signal: a second poll arriving faster than the server's
// advertised interval must not be treated as a normal pending poll.
func TestDeviceAuth_PollTwiceWithinIntervalReturnsSlowDown(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	code := requestDeviceCode(t)

	first := pollDeviceToken(code.DeviceCode)
	assertDeviceTokenError(t, first, "authorization_pending")

	second := pollDeviceToken(code.DeviceCode)
	assertDeviceTokenError(t, second, "slow_down")
}

// TestDeviceAuth_ReplayedPollAfterClaimReturnsExpiredToken is the
// single-claim guard: once the CLI has successfully received the token, a
// second poll (replay, or a losing concurrent poller) must never reissue
// it — the raw value isn't stored anywhere to reissue.
func TestDeviceAuth_ReplayedPollAfterClaimReturnsExpiredToken(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	approver := newDeviceAuthTestUser(t, "device-replay@multica.ai")
	code := requestDeviceCode(t)

	confirmReq := newRequestAs(approver, "POST", "/api/device/confirm", confirmDeviceAuthorizationRequest{UserCode: code.UserCode, Approve: true})
	testHandler.ConfirmDeviceAuthorization(httptest.NewRecorder(), confirmReq)

	first := pollDeviceToken(code.DeviceCode)
	if first.Code != 200 {
		t.Fatalf("first claim status = %d, body = %s", first.Code, first.Body.String())
	}

	// Wait past the poll interval so this second call isn't rejected as
	// slow_down before ever reaching the claimed-state check.
	time.Sleep(deviceAuthPollInterval*time.Second + 100*time.Millisecond)
	second := pollDeviceToken(code.DeviceCode)
	assertDeviceTokenError(t, second, "expired_token")
}

// TestDeviceAuth_DoubleConfirmIsIdempotentNotDoubleMint proves confirming
// an already-approved row a second time does not mint a second PAT for the
// same device_code (RED this first against a naive implementation that
// re-mints on every approve call).
func TestDeviceAuth_DoubleConfirmIsIdempotentNotDoubleMint(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	approver := newDeviceAuthTestUser(t, "device-double-confirm@multica.ai")
	code := requestDeviceCode(t)

	for range 2 {
		req := newRequestAs(approver, "POST", "/api/device/confirm", confirmDeviceAuthorizationRequest{UserCode: code.UserCode, Approve: true})
		w := httptest.NewRecorder()
		testHandler.ConfirmDeviceAuthorization(w, req)
		if w.Code != 200 {
			t.Fatalf("confirm status = %d, body = %s", w.Code, w.Body.String())
		}
	}

	var patCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM personal_access_token WHERE user_id = $1`, approver).Scan(&patCount); err != nil {
		t.Fatalf("count PATs: %v", err)
	}
	if patCount != 0 {
		t.Fatalf("PAT count after double-confirm (before any poll) = %d, want 0 — confirm must not mint", patCount)
	}
}

func assertDeviceTokenError(t *testing.T, w *httptest.ResponseRecorder, wantError string) {
	t.Helper()
	if w.Code != 400 {
		t.Fatalf("status = %d, body = %s, want 400", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp["error"] != wantError {
		t.Fatalf("error = %q, want %q", resp["error"], wantError)
	}
}
