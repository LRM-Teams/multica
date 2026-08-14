package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type manifestPrincipalMember struct {
	AgentID string `json:"agent_id"`
	Role    string `json:"role"`
	Status  string `json:"status"`
	IsLead  bool   `json:"is_lead"`
}

func encodeManifestPrincipalHeader(members []FleetMember) ([]byte, error) {
	frozen := make([]manifestPrincipalMember, 0, len(members))
	for _, member := range members {
		frozen = append(frozen, manifestPrincipalMember{AgentID: member.AgentID, Role: member.Role, Status: member.Status, IsLead: member.IsLead})
	}
	return json.Marshal(frozen)
}

func decodeManifestPrincipalHeader(raw []byte) ([]FleetMember, error) {
	var frozen []manifestPrincipalMember
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	members := make([]FleetMember, 0, len(frozen))
	for _, member := range frozen {
		members = append(members, FleetMember{AgentID: member.AgentID, Role: member.Role, Status: member.Status, IsLead: member.IsLead})
	}
	return members, nil
}

func loadManifestPrincipalHeaderTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, manifestID string) ([]FleetMember, error) {
	var raw []byte
	var storedHash string
	if err := tx.QueryRow(ctx, `SELECT principal_header_bytes, principal_header_hash FROM research_artifact_context_manifest WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, workspaceID, sessionID, manifestID).Scan(&raw, &storedHash); err != nil {
		return nil, err
	}
	if len(raw) == 0 || contentHashFromPayload(raw) != storedHash {
		return nil, fmt.Errorf("%w: manifest principal header missing or invalid", ErrInvalidTransition)
	}
	return decodeManifestPrincipalHeader(raw)
}

func loadManifestPrincipalHeaderPool(ctx context.Context, store *PostgresStore, workspaceID, sessionID, attemptID string) ([]FleetMember, error) {
	var raw []byte
	var storedHash string
	if err := store.pool.QueryRow(ctx, `SELECT principal_header_bytes, principal_header_hash FROM research_artifact_context_manifest WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND attempt_id=$3::uuid`, workspaceID, sessionID, attemptID).Scan(&raw, &storedHash); err != nil {
		return nil, err
	}
	if len(raw) == 0 || contentHashFromPayload(raw) != storedHash {
		return nil, fmt.Errorf("%w: manifest principal header missing or invalid", ErrInvalidTransition)
	}
	return decodeManifestPrincipalHeader(raw)
}

func rebindDispatchPromptForManifestTx(
	ctx context.Context,
	tx pgx.Tx,
	store *PostgresStore,
	in CreateDispatchIntentInput,
	attempt Attempt,
	workspaceID, manifestID string,
) (DispatchRequest, error) {
	if manifestID == "" {
		return in.Request, nil
	}
	manifestSet, err := loadManifestArtifactSetTx(ctx, tx, workspaceID, in.SessionID, manifestID)
	if err != nil {
		return DispatchRequest{}, err
	}
	liveSnapshot, err := store.TaskContext(ctx, in.TaskID, workspaceID)
	if err != nil {
		return DispatchRequest{}, err
	}
	// TaskContext uses the pool and therefore cannot observe the attempt that
	// this transaction just inserted. Align the independently loaded shadow
	// with the transaction-visible task/attempt state used to freeze the
	// manifest, otherwise derived fields such as Task.AttemptCount compare 0
	// against 1 and reject every first dispatch.
	liveTask, err := scanTask(tx.QueryRow(ctx, taskSelectSQL+` WHERE t.workspace_id=$1::uuid AND t.session_id=$2::uuid AND t.id=$3::uuid`, workspaceID, in.SessionID, in.TaskID))
	if err != nil {
		return DispatchRequest{}, err
	}
	for i := range liveSnapshot.Tasks {
		if liveSnapshot.Tasks[i].ID == liveTask.ID {
			liveSnapshot.Tasks[i] = liveTask
			break
		}
	}
	foundAttempt := false
	for i := range liveSnapshot.Attempts {
		if liveSnapshot.Attempts[i].ID == attempt.ID {
			liveSnapshot.Attempts[i] = attempt
			foundAttempt = true
			break
		}
	}
	if !foundAttempt {
		liveSnapshot.Attempts = append(liveSnapshot.Attempts, attempt)
	}
	filtered := filterRunSnapshotByManifest(liveSnapshot, manifestSet.ArtifactIDs)
	filtered.Run, err = loadFrozenRunRepresentationTx(
		ctx, tx, workspaceID, in.SessionID, attempt.ID,
	)
	if err != nil {
		return DispatchRequest{}, err
	}
	frozenDurable, err := loadFrozenDurableContextTx(
		ctx, tx, workspaceID, in.SessionID, attempt.ID,
	)
	if err != nil {
		return DispatchRequest{}, err
	}
	filtered.Contract = frozenDurable.Contract
	filtered.Method = frozenDurable.Method
	filtered.Questions = frozenDurable.Questions
	filtered.Tasks = frozenDurable.Tasks
	filtered.Attempts = frozenDurable.Attempts
	frozenSources, frozenObservations, frozenClaims, err := loadFrozenEvidenceRepresentationsTx(
		ctx, tx, workspaceID, in.SessionID, attempt.ID,
	)
	if err != nil {
		return DispatchRequest{}, err
	}
	filtered.Sources = frozenSources
	filtered.Observations = frozenObservations
	filtered.Claims = frozenClaims
	frozenLegacy, err := loadFrozenLegacyContextTx(
		ctx, tx, workspaceID, in.SessionID, attempt.ID,
	)
	if err != nil {
		return DispatchRequest{}, err
	}
	filtered.LegacyContext = &frozenLegacy
	filtered.EvaluationPrivate, err = loadFrozenEvaluationPrivateTx(ctx, tx, workspaceID, in.SessionID, manifestID)
	if err != nil {
		return DispatchRequest{}, err
	}
	members, err := loadManifestPrincipalHeaderTx(ctx, tx, workspaceID, in.SessionID, manifestID)
	if err != nil {
		return DispatchRequest{}, err
	}
	livePrompt, err := buildTaskPrompt(liveSnapshot.Run, in.Request.Task, attempt, liveSnapshot, members)
	if err != nil {
		return DispatchRequest{}, err
	}
	prompt, err := buildTaskPrompt(filtered.Run, in.Request.Task, attempt, filtered, members)
	if err != nil {
		return DispatchRequest{}, err
	}
	if err = verifyManifestPromptShadow(livePrompt, prompt, liveSnapshot, filtered); err != nil {
		return DispatchRequest{}, err
	}
	request := in.Request
	request.Prompt = prompt
	request.ManifestID = manifestID
	request.ManifestHash = manifestSet.Hash
	return request, nil
}

func resolveDispatchRequestTx(
	ctx context.Context,
	tx pgx.Tx,
	store *PostgresStore,
	in CreateDispatchIntentInput,
	attempt Attempt,
	workspaceID string,
	artifactPassportEnabled bool,
	manifestID string,
) (DispatchRequest, []byte, string, error) {
	request := in.Request
	if artifactPassportEnabled {
		var err error
		request, err = rebindDispatchPromptForManifestTx(ctx, tx, store, in, attempt, workspaceID, manifestID)
		if err != nil {
			return DispatchRequest{}, nil, "", err
		}
	} else if in.Request.RequestHash != "" {
		hash, err := HashDispatchRequest(in.Request)
		if err != nil {
			return DispatchRequest{}, nil, "", err
		}
		if in.Request.RequestHash != hash {
			return DispatchRequest{}, nil, "", fmt.Errorf("%w: dispatch request hash does not match payload", ErrResultConflict)
		}
	}
	encoded, hash, err := encodeDispatchRequest(request)
	if err != nil {
		return DispatchRequest{}, nil, "", fmt.Errorf("encode dispatch request: %w", err)
	}
	return request, encoded, hash, nil
}

func loadAttemptManifestIDTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID, attemptID string,
) (string, error) {
	var manifestID string
	err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&manifestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return manifestID, err
}

// replayDispatchPromptFromManifest rebuilds the dispatch prompt from the frozen
// manifest-filtered task snapshot without using the caller-supplied placeholder.
func replayDispatchPromptFromManifest(
	ctx context.Context,
	store *PostgresStore,
	workspaceID, attemptID string,
) (string, error) {
	snapshot, err := store.TaskContextForAttempt(ctx, attemptID, workspaceID)
	if err != nil {
		return "", err
	}
	if snapshot.AttemptContext == nil || !snapshot.AttemptContext.ManifestFiltered {
		return "", fmt.Errorf("%w: attempt has no frozen manifest context", ErrInvalidTransition)
	}
	var attempt Attempt
	for _, candidate := range snapshot.Attempts {
		if candidate.ID == attemptID {
			attempt = candidate
			break
		}
	}
	if attempt.ID == "" {
		return "", ErrRunNotFound
	}
	var task Task
	for _, candidate := range snapshot.Tasks {
		if candidate.ID == attempt.TaskID {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		return "", ErrRunNotFound
	}
	members, err := loadManifestPrincipalHeaderPool(ctx, store, workspaceID, snapshot.Run.SessionID, attemptID)
	if err != nil {
		return "", err
	}
	return buildTaskPrompt(snapshot.Run, task, attempt, snapshot, members)
}

func verifyManifestPromptShadow(
	livePrompt, manifestPrompt string,
	liveSnapshot, filtered RunSnapshot,
) error {
	if err := compareShadowSnapshotRepresentations(liveSnapshot, filtered); err != nil {
		return err
	}
	// Passport-enabled dispatch intentionally leaves the caller prompt empty:
	// the frozen manifest is the only authoritative input for prompt rendering.
	if livePrompt == "" {
		return nil
	}
	if livePrompt == manifestPrompt {
		return nil
	}
	if len(liveSnapshot.Sources) != len(filtered.Sources) ||
		len(liveSnapshot.Observations) != len(filtered.Observations) ||
		len(liveSnapshot.Claims) != len(filtered.Claims) ||
		countSnapshotEvidence(liveSnapshot) != countSnapshotEvidence(filtered) ||
		len(filtered.EvaluationPrivate) > 0 {
		return nil
	}
	return fmt.Errorf("%w: manifest prompt shadow mismatch", ErrInvalidTransition)
}

func countSnapshotEvidence(snapshot RunSnapshot) int {
	count := 0
	for _, claim := range snapshot.Claims {
		count += len(claim.Evidence)
	}
	return count
}
