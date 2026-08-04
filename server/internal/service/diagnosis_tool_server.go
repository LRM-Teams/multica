// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// diagnosisToolServerMaxBody is the hard cap on HTTP request bodies.
const diagnosisToolServerMaxBody = 256 * 1024

// DiagnosisToolServer exposes the five on-demand diagnosis capabilities over a
// loopback HTTP server. The caller starts it with ListenAndServe, receives the
// allocated URL and bearer token, and tears it down via Shutdown.
type DiagnosisToolServer struct {
	httpServer  *http.Server
	baseURL     string
	bearerToken string
	cursorKey   []byte

	runCheckpoint  DiagnosisRunCheckpoint
	stateStore     *DiagnosisStateStore
	pager          DiagnosisMessagePager
	dagWriter      DiagnosisDAGWriter
	segmentTargets map[string]DiagnosisSegmentTarget
}

// DiagnosisDAGWriter is the narrow write surface for incremental step-reward
// persistence.
type DiagnosisDAGWriter interface {
	UpsertDiagnosisStepReward(ctx context.Context, projectID, segmentID string, seq int32, score int, rationale string) error
	GetDiagnosisStepReward(ctx context.Context, projectID, segmentID string, seq int32) (score int, rationale string, exists bool, err error)
	CountDiagnosisStepRewards(ctx context.Context, projectID, segmentID string) (int, error)
}

// diagnosisDAGAdapter wraps an InteractionDAGStore to implement
// DiagnosisDAGWriter. The adapter delegates to the existing upsert query,
// adds a point-read and count query for the tool server, and is scoped to
// the concrete store the caller provides.
type diagnosisDAGAdapter struct {
	store InteractionDAGStore
}

func newDiagnosisDAGAdapter(store InteractionDAGStore) *diagnosisDAGAdapter {
	return &diagnosisDAGAdapter{store: store}
}

func (a *diagnosisDAGAdapter) UpsertDiagnosisStepReward(ctx context.Context, projectID, segmentID string, seq int32, score int, rationale string) error {
	return a.store.InsertInteractionDAGStepReward(ctx, db.InsertInteractionDAGStepRewardParams{
		SegmentID: segmentID,
		Seq:       seq,
		Score:     int32(score),
		Rationale: rationale,
	})
}

func (a *diagnosisDAGAdapter) GetDiagnosisStepReward(ctx context.Context, projectID, segmentID string, seq int32) (int, string, bool, error) {
	rewards, err := a.store.ListInteractionDAGStepRewardsForProject(ctx, projectID)
	if err != nil {
		return 0, "", false, err
	}
	for _, r := range rewards {
		if r.SegmentID == segmentID && r.Seq == seq {
			return int(r.Score), r.Rationale, true, nil
		}
	}
	return 0, "", false, nil
}

func (a *diagnosisDAGAdapter) CountDiagnosisStepRewards(ctx context.Context, projectID, segmentID string) (int, error) {
	rewards, err := a.store.ListInteractionDAGStepRewardsForProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, r := range rewards {
		if r.SegmentID == segmentID {
			count++
		}
	}
	return count, nil
}

// topologyHash returns a deterministic hash of the ordered segment IDs for the
// current run, matching the snapshot taken at CreateRun time.
func (s *DiagnosisToolServer) topologyHash() string {
	data, _ := json.Marshal(s.runCheckpoint.OrderedSegmentIDs)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// NewDiagnosisToolServer creates the server with an ephemeral loopback port and
// a cryptographically random 32-byte bearer token. The caller injects all
// scoped dependencies so the server never reaches beyond the run it was created
// for.
func NewDiagnosisToolServer(
	ckpt DiagnosisRunCheckpoint,
	state *DiagnosisStateStore,
	pager DiagnosisMessagePager,
	dagWriter DiagnosisDAGWriter,
) (*DiagnosisToolServer, error) {
	cursorKey := make([]byte, 32)
	if _, err := rand.Read(cursorKey); err != nil {
		return nil, fmt.Errorf("diagnosis tool server: cursor key: %w", err)
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("diagnosis tool server: bearer token: %w", err)
	}
	bearerToken := fmt.Sprintf("%x", token)

	s := &DiagnosisToolServer{
		runCheckpoint:  ckpt,
		stateStore:     state,
		pager:          pager,
		dagWriter:      dagWriter,
		segmentTargets: make(map[string]DiagnosisSegmentTarget),
		cursorKey:      cursorKey,
		bearerToken:    bearerToken,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/get-segment-messages", s.handleGetSegmentMessages)
	mux.HandleFunc("POST /v1/record-step-rewards", s.handleRecordStepRewards)
	mux.HandleFunc("GET /v1/diagnosis-progress", s.handleDiagnosisProgress)
	mux.HandleFunc("POST /v1/finish-segment", s.handleFinishSegment)
	mux.HandleFunc("POST /v1/complete-diagnosis", s.handleCompleteDiagnosis)

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s, nil
}

// SetSegmentTargets installs the immutable segment/message ranges frozen by
// the runner before Pi starts. The tool server uses these values for paging so
// an agent can never read or score turns outside the diagnosis snapshot.
func (s *DiagnosisToolServer) SetSegmentTargets(targets []DiagnosisSegmentTarget) error {
	configured := make(map[string]DiagnosisSegmentTarget, len(targets))
	for _, target := range targets {
		if !s.segmentInRun(target.SegmentID) {
			return fmt.Errorf("diagnosis tool server: target %s is not in run", target.SegmentID)
		}
		if target.AgentRunID == "" || target.StartSeq < 1 || target.EndSeq < target.StartSeq {
			return fmt.Errorf("diagnosis tool server: invalid target range for %s", target.SegmentID)
		}
		if _, duplicate := configured[target.SegmentID]; duplicate {
			return fmt.Errorf("diagnosis tool server: duplicate target %s", target.SegmentID)
		}
		configured[target.SegmentID] = target
	}
	if len(configured) != len(s.runCheckpoint.OrderedSegmentIDs) {
		return fmt.Errorf("diagnosis tool server: targets do not cover the run")
	}
	s.segmentTargets = configured
	return nil
}

// ListenAndServe binds to 127.0.0.1:0 and starts serving. Returns the allocated
// base URL. The caller is responsible for calling Shutdown.
func (s *DiagnosisToolServer) ListenAndServe() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("diagnosis tool server: listen: %w", err)
	}
	s.baseURL = fmt.Sprintf("http://%s", ln.Addr().String())
	// Install the cursor signing key now that the server is live.
	key := s.cursorKey
	SetDiagnosisPagerKey(func() []byte { return key })
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("diagnosis tool server: serve", "error", err)
		}
	}()
	return s.baseURL, nil
}

// BearerToken returns the per-session capability token. It must never appear in
// prompts, logs, or persisted memory.
func (s *DiagnosisToolServer) BearerToken() string { return s.bearerToken }

// Shutdown gracefully stops the HTTP server.
func (s *DiagnosisToolServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// ── Auth ──

func (s *DiagnosisToolServer) checkAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	token := auth[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.bearerToken)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func readLimitedBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, diagnosisToolServerMaxBody))
}

// ── POST /v1/get-segment-messages ──

type getSegmentMessagesRequest struct {
	SegmentID string `json:"segment_id"`
	Cursor    string `json:"cursor,omitempty"`
}

func (s *DiagnosisToolServer) handleGetSegmentMessages(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, err := readLimitedBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_BODY", "cannot read request body")
		return
	}
	var req getSegmentMessagesRequest
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_JSON", "invalid request body")
		return
	}
	if strings.TrimSpace(req.SegmentID) == "" {
		writeError(w, http.StatusBadRequest, "MISSING_SEGMENT_ID", "segment_id is required")
		return
	}
	// Validate segment belongs to this run.
	if !s.segmentInRun(req.SegmentID) {
		writeError(w, http.StatusForbidden, "SEGMENT_NOT_IN_RUN", "segment not in current diagnosis run")
		return
	}
	if _, err := s.stateStore.GetSegment(r.Context(), s.runCheckpoint.RunID, req.SegmentID); err != nil {
		writeError(w, http.StatusNotFound, "SEGMENT_NOT_FOUND", "segment not found")
		return
	}
	target, ok := s.segmentTargets[req.SegmentID]
	if !ok {
		writeError(w, http.StatusConflict, "SEGMENT_NOT_FROZEN", "segment targets are not frozen")
		return
	}
	page, err := GetSegmentMessagePage(r.Context(), s.pager, target.AgentRunID, req.SegmentID, target.StartSeq, target.EndSeq, req.Cursor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PAGE_ERROR", "failed to page messages")
		return
	}
	if err := s.stateStore.RecordSegmentPage(
		r.Context(), s.runCheckpoint.RunID, req.SegmentID, req.Cursor, page.NextCursor, page.FetchedCount,
	); err != nil {
		writeError(w, http.StatusConflict, "STALE_CURSOR", "segment cursor is stale")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ── POST /v1/record-step-rewards ──

type stepRewardEntry struct {
	Seq       int    `json:"seq"`
	Score     int    `json:"score"`
	Rationale string `json:"rationale"`
}

type recordStepRewardsRequest struct {
	SegmentID string            `json:"segment_id"`
	Rewards   []stepRewardEntry `json:"rewards"`
}

type recordStepRewardsResponse struct {
	PersistedSeqs []int            `json:"persisted_seqs"`
	MissingSeqs   []int            `json:"missing_seqs,omitempty"`
	Rejected      []rejectedReward `json:"rejected,omitempty"`
}

type rejectedReward struct {
	Seq    int    `json:"seq"`
	Reason string `json:"reason"`
}

func (s *DiagnosisToolServer) handleRecordStepRewards(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, err := readLimitedBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_BODY", "cannot read request body")
		return
	}
	var req recordStepRewardsRequest
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_JSON", "invalid request body")
		return
	}
	if strings.TrimSpace(req.SegmentID) == "" {
		writeError(w, http.StatusBadRequest, "MISSING_SEGMENT_ID", "segment_id is required")
		return
	}
	if !s.segmentInRun(req.SegmentID) {
		writeError(w, http.StatusForbidden, "SEGMENT_NOT_IN_RUN", "segment not in current diagnosis run")
		return
	}
	if len(req.Rewards) == 0 {
		writeError(w, http.StatusBadRequest, "NO_REWARDS", "at least one reward is required")
		return
	}

	entries := make([]DiagnosisStepRewardInput, 0, len(req.Rewards))
	for _, entry := range req.Rewards {
		entries = append(entries, DiagnosisStepRewardInput{Seq: entry.Seq, Score: entry.Score, Rationale: entry.Rationale})
	}
	outcome, err := RecordDiagnosisStepRewards(
		r.Context(), s.stateStore, s.dagWriter, s.runCheckpoint.ProjectID, s.runCheckpoint.RunID, req.SegmentID, entries,
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "SEGMENT_NOT_FOUND", "segment not found")
		return
	}

	var rejected []rejectedReward
	for _, rej := range outcome.Rejected {
		rejected = append(rejected, rejectedReward{Seq: rej.Seq, Reason: rej.Reason})
	}
	writeJSON(w, http.StatusOK, recordStepRewardsResponse{
		PersistedSeqs: outcome.PersistedSeqs,
		MissingSeqs:   outcome.MissingSeqs,
		Rejected:      rejected,
	})
}

func containsDiagnosisSeq(seqs []int32, needle int32) bool {
	for _, seq := range seqs {
		if seq == needle {
			return true
		}
	}
	return false
}

// ── GET /v1/diagnosis-progress ──

type diagnosisProgressResponse struct {
	RunID                 string   `json:"run_id"`
	CurrentSegmentID      string   `json:"current_segment_id"`
	CurrentSegmentOrdinal int      `json:"current_segment_ordinal"`
	CompletedSegmentIDs   []string `json:"completed_segment_ids"`
	RemainingSegmentIDs   []string `json:"remaining_segment_ids"`
	FetchedMessageCount   int      `json:"fetched_message_count"`
	ExpectedMessageCount  int      `json:"expected_message_count"`
	RecordedRewardCount   int      `json:"recorded_reward_count"`
	ExpectedRewardCount   int      `json:"expected_reward_count"`
}

func (s *DiagnosisToolServer) handleDiagnosisProgress(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	segments, err := s.stateStore.ListSegments(r.Context(), s.runCheckpoint.RunID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PROGRESS_ERROR", "failed to list segments")
		return
	}
	var completed, remaining []string
	var currentSeg *SegmentDiagnosisCheckpoint
	for i := range segments {
		seg := &segments[i]
		if seg.Status == SegmentDiagnosisCompleted {
			completed = append(completed, seg.SegmentID)
		} else {
			remaining = append(remaining, seg.SegmentID)
			if currentSeg == nil {
				currentSeg = seg
			}
		}
	}

	resp := diagnosisProgressResponse{
		RunID:               s.runCheckpoint.RunID,
		CompletedSegmentIDs: completed,
		RemainingSegmentIDs: remaining,
	}
	if currentSeg != nil {
		resp.CurrentSegmentID = currentSeg.SegmentID
		resp.CurrentSegmentOrdinal = currentSeg.Ordinal
		resp.FetchedMessageCount = currentSeg.FetchedMessageCount
		resp.ExpectedMessageCount = currentSeg.ExpectedMessageCount
		resp.RecordedRewardCount = currentSeg.RewardCount
		resp.ExpectedRewardCount = currentSeg.ExpectedRewardCount
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── POST /v1/finish-segment ──

type finishSegmentRequest struct {
	SegmentID string `json:"segment_id"`
}

type finishSegmentResponse struct {
	Status      string `json:"status"`
	MissingSeqs []int  `json:"missing_seqs,omitempty"`
}

func (s *DiagnosisToolServer) handleFinishSegment(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, err := readLimitedBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_BODY", "cannot read request body")
		return
	}
	var req finishSegmentRequest
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_JSON", "invalid request body")
		return
	}
	if strings.TrimSpace(req.SegmentID) == "" {
		writeError(w, http.StatusBadRequest, "MISSING_SEGMENT_ID", "segment_id is required")
		return
	}
	if !s.segmentInRun(req.SegmentID) {
		writeError(w, http.StatusForbidden, "SEGMENT_NOT_IN_RUN", "segment not in current diagnosis run")
		return
	}

	if err := s.stateStore.CompleteSegment(r.Context(), s.runCheckpoint.RunID, req.SegmentID); err != nil {
		// Surface the precise missing coverage.
		segCkpt, fetchErr := s.stateStore.GetSegment(r.Context(), s.runCheckpoint.RunID, req.SegmentID)
		if fetchErr != nil {
			writeError(w, http.StatusInternalServerError, "FINISH_ERROR", err.Error())
			return
		}
		resp := finishSegmentResponse{Status: "incomplete"}
		if segCkpt.FetchedMessageCount < segCkpt.ExpectedMessageCount {
			resp.MissingSeqs = append(resp.MissingSeqs, -1) // signal message coverage gap
		}
		if segCkpt.RewardCount < segCkpt.ExpectedRewardCount {
			resp.MissingSeqs = append(resp.MissingSeqs, -2) // signal reward coverage gap
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	writeJSON(w, http.StatusOK, finishSegmentResponse{Status: "completed"})
}

// ── POST /v1/complete-diagnosis ──

type completeDiagnosisResponse struct {
	Status      string          `json:"status"`
	MissingSegs []missingSegRef `json:"missing_segments,omitempty"`
}

type missingSegRef struct {
	SegmentID string `json:"segment_id"`
	Reason    string `json:"reason"`
}

func (s *DiagnosisToolServer) handleCompleteDiagnosis(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if err := s.stateStore.CompleteRun(r.Context(), s.runCheckpoint.RunID, s.runCheckpoint.TopologyHash); err != nil {
		// Enumerate incomplete segments.
		segments, listErr := s.stateStore.ListSegments(r.Context(), s.runCheckpoint.RunID)
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "COMPLETE_ERROR", err.Error())
			return
		}
		var missing []missingSegRef
		for _, seg := range ListIncompleteDiagnosisSegments(segments) {
			missing = append(missing, missingSegRef{SegmentID: seg.SegmentID, Reason: seg.Reason})
		}
		writeJSON(w, http.StatusOK, completeDiagnosisResponse{Status: "incomplete", MissingSegs: missing})
		return
	}
	writeJSON(w, http.StatusOK, completeDiagnosisResponse{Status: "completed"})
}

// ── Helpers ──

func (s *DiagnosisToolServer) segmentInRun(segmentID string) bool {
	return SegmentInRun(s.runCheckpoint, segmentID)
}
