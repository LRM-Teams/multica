// SPDX-License-Identifier: Apache-2.0

// Package arealrl is a minimal Go client for AReaL's experimental openai-proxy
// RL session lifecycle, reached via multica's in-network db_bridge stub which
// forwards /rl/* to the AReaL proxy gateway.
//
// Contract confirmed against the experimental openai-proxy stack (Step 0):
//
//	areal/experimental/openai/proxy/proxy_rollout_server.py
//	areal/experimental/openai/proxy/proxy_gateway.py
//	areal/experimental/openai/proxy/server.py
//
// Endpoints (paths from server.py: RL_*_PATHNAME, no leading slash):
//
//   - POST /rl/start_session — admin-key auth. proxy_rollout_server
//     `_require_admin_key` -> `_extract_bearer_token` accepts
//     "Authorization: Bearer <token>" (or "x-api-key"), but the gateway
//     (`start_session`, `_extract_bearer_token`) accepts ONLY
//     "Authorization: Bearer <token>", so we always use Bearer.
//     Request body model StartSessionRequest {task_id, api_key?}.
//     Response model StartSessionResponse is FLAT {session_id, api_key}
//     where api_key is the per-session proxy key.
//
//   - POST /rl/set_reward — session-key auth (Bearer <proxy_key>) via
//     `_require_session_key`. Body model SetRewardRequest {interaction_id?,
//     reward}; we send only {reward} (NO session_id — session is resolved
//     from the key).
//
//   - POST /rl/close_segment - session-key auth (Bearer <proxy_key>), no body.
//     Closes the session's current segment without reward (the segment-DAG
//     path slices a session into communication-bounded segments). Response
//     CloseSegmentResponse {trajectory_id (nullable), interaction_count,
//     session_id, ready_transition}; trajectory_id is the just-closed
//     segment's trajectory.
//
//   - POST /export_trajectories - admin-key auth (Bearer <admin_key>). Body
//     {session_ids, trajectory_id?, remove_session}. Returns {traj: <merged
//     serialized interactions>}. The segment-DAG path calls this per closed
//     segment with remove_session=false to fetch the tensor ref while keeping
//     the session alive.
//
// There is deliberately no end_session call. The v1 proxy_rollout_server
// exposed /rl/end_session, but the v2 inference_service
// (areal/v2/inference_service/{gateway,data_proxy}/app.py) serves only
// start_session, set_reward and close_segment: a session is torn down on the
// AReaL side by export_trajectories(remove_session=true) or by the store's
// stale-session reaper. Calling end_session against v2 is a guaranteed 404.
//
// Deviations from design §4.6:
//   - §4.6 specifies body {task_id, group_size:1} for start_session. The
//     server-side StartSessionRequest model declares only {task_id, api_key?}
//     and does not declare group_size; Pydantic ignores the extra field. We
//     still send group_size:1 to match §4.6 and future-proof the contract.
//     No other deviations found — reality matches §4.6.
package arealrl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Endpoint paths, mirroring RL_*_PATHNAME in
// areal/experimental/openai/proxy/server.py.
const (
	startSessionPath = "/rl/start_session"
	setRewardPath    = "/rl/set_reward"
	closeSegmentPath = "/rl/close_segment"
	exportTrajPath   = "/export_trajectories"
)

// Client talks to the db_bridge stub that fronts AReaL's experimental RL proxy.
type Client struct {
	httpClient  *http.Client
	stubBaseURL string
	adminKey    string
}

// New builds a Client. stubBaseURL is the base URL of the db_bridge stub
// (any trailing slash is trimmed); adminKey authenticates start_session.
func New(stubBaseURL, adminKey string) *Client {
	return &Client{
		httpClient:  http.DefaultClient,
		stubBaseURL: strings.TrimRight(stubBaseURL, "/"),
		adminKey:    adminKey,
	}
}

// SessionCreds are the credentials returned by StartSession. ProxyKey is the
// per-session api_key used for session-key auth on SetReward/CloseSegment.
type SessionCreds struct {
	SessionID string
	ProxyKey  string
}

// startSessionResponse is the flat JSON returned by /rl/start_session.
type startSessionResponse struct {
	SessionID string `json:"session_id"`
	APIKey    string `json:"api_key"`
}

// StartSession opens a new RL session for sessionRef using admin-key auth and
// returns the session id and per-session proxy key.
//
// sessionRef is the canonical session reference forwarded to the bridge as
// "session_ref": for the legacy training flow it is the task id; for
// env-dispatch derived-agent provisioning it is the persistent source-binding
// id. The bridge accepts exactly one of session_ref/task_id and derives its
// canonical namespace from whichever is supplied, so sending session_ref with
// the same string value is backward compatible with the prior task_id flow.
func (c *Client) StartSession(ctx context.Context, sessionRef, envID string) (SessionCreds, error) {
	body := map[string]any{
		"session_ref": sessionRef,
		"group_size":  1,
	}
	if envID != "" {
		body["env_id"] = envID
	}
	resp, err := c.doJSON(ctx, startSessionPath, c.adminKey, body)
	if err != nil {
		return SessionCreds{}, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp, "start_session"); err != nil {
		return SessionCreds{}, err
	}

	var out startSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SessionCreds{}, fmt.Errorf("arealrl: decode start_session response: %w", err)
	}
	if out.SessionID == "" {
		return SessionCreds{}, fmt.Errorf("arealrl: start_session response missing session_id")
	}
	if out.APIKey == "" {
		return SessionCreds{}, fmt.Errorf("arealrl: start_session response missing api_key")
	}
	return SessionCreds{SessionID: out.SessionID, ProxyKey: out.APIKey}, nil
}

// SetReward records a reward for the session identified by proxyKey
// (session-key auth). No session_id is sent; the server resolves the session
// from the key.
func (c *Client) SetReward(ctx context.Context, proxyKey string, reward float64) error {
	body := map[string]any{"reward": reward}
	resp, err := c.doJSON(ctx, setRewardPath, proxyKey, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp, "set_reward")
}

// closeSegmentResponse mirrors AReaL's CloseSegmentResponse. TrajectoryID is
// nil when close_segment did not close a trajectory (no active segment); the
// segment-DAG flow treats that as an error since every close should yield a
// trajectory to export.
type closeSegmentResponse struct {
	TrajectoryID     *int   `json:"trajectory_id"`
	InteractionCount int    `json:"interaction_count"`
	SessionID        string `json:"session_id"`
	ReadyTransition  bool   `json:"ready_transition"`
}

// CloseSegment closes the session's current segment (session-key auth via
// proxyKey) and returns the trajectory id of the just-closed segment. The
// endpoint resolves the session from the bearer token, so no session_id is
// sent - mirroring SetReward.
func (c *Client) CloseSegment(ctx context.Context, proxyKey string) (int, error) {
	resp, err := c.doJSON(ctx, closeSegmentPath, proxyKey, map[string]any{})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, "close_segment"); err != nil {
		return 0, err
	}
	var out closeSegmentResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("arealrl: decode close_segment response: %w", err)
	}
	if out.TrajectoryID == nil {
		return 0, fmt.Errorf("arealrl: close_segment response missing trajectory_id")
	}
	return *out.TrajectoryID, nil
}

// exportRequest mirrors AReaL's ExportTrajectoriesRequest. RemoveSession is
// false so the session stays alive for subsequent per-segment exports (the
// gateway would otherwise revoke the session group on removal). No group_id
// is sent.
type exportRequest struct {
	SessionIDs    []string `json:"session_ids"`
	TrajectoryID  *int     `json:"trajectory_id,omitempty"`
	RemoveSession bool     `json:"remove_session"`
}

// ExportTrajectory exports one trajectory from sessionID (admin-key auth) and
// returns the raw “traj“ JSON. The caller decodes the tensor_ref it needs
// from the returned payload. Admin key is the client's configured key, the
// same used by StartSession.
func (c *Client) ExportTrajectory(
	ctx context.Context,
	sessionID string,
	trajectoryID int,
) (json.RawMessage, error) {
	body := exportRequest{
		SessionIDs:    []string{sessionID},
		TrajectoryID:  &trajectoryID,
		RemoveSession: false,
	}
	resp, err := c.doJSON(ctx, exportTrajPath, c.adminKey, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, "export_trajectories"); err != nil {
		return nil, err
	}
	var out struct {
		Traj json.RawMessage `json:"traj"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("arealrl: decode export response: %w", err)
	}
	return out.Traj, nil
}

// doJSON POSTs a JSON body to path with Bearer authentication.
func (c *Client) doJSON(
	ctx context.Context,
	path string,
	bearer string,
	body any,
) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("arealrl: marshal request body for %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.stubBaseURL+path, bytes.NewReader(payload),
	)
	if err != nil {
		return nil, fmt.Errorf("arealrl: build request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("arealrl: POST %s: %w", path, err)
	}
	return resp, nil
}

// checkStatus returns an error for any non-2xx response, including a snippet
// of the response body for diagnostics.
func checkStatus(resp *http.Response, op string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf(
		"arealrl: %s returned status %d: %s",
		op, resp.StatusCode, strings.TrimSpace(string(snippet)),
	)
}
