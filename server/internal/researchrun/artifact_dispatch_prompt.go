package researchrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

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
	filtered := filterRunSnapshotByManifest(liveSnapshot, manifestSet.ArtifactIDs)
	members, err := listFleetMembersTx(ctx, tx, in.SessionID, workspaceID)
	if err != nil {
		return DispatchRequest{}, err
	}
	prompt, err := buildTaskPrompt(filtered.Run, in.Request.Task, attempt, filtered, members)
	if err != nil {
		return DispatchRequest{}, err
	}
	if err = verifyManifestPromptShadow(in.Request.Prompt, prompt, liveSnapshot, filtered); err != nil {
		return DispatchRequest{}, err
	}
	request := in.Request
	request.Prompt = prompt
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
	members, err := store.ListFleetMembers(ctx, snapshot.Run.SessionID, workspaceID)
	if err != nil {
		return "", err
	}
	return buildTaskPrompt(snapshot.Run, task, attempt, snapshot, members)
}

func verifyManifestPromptShadow(
	livePrompt, manifestPrompt string,
	liveSnapshot, filtered RunSnapshot,
) error {
	if livePrompt == manifestPrompt {
		return nil
	}
	if len(liveSnapshot.Sources) != len(filtered.Sources) ||
		len(liveSnapshot.Observations) != len(filtered.Observations) ||
		len(liveSnapshot.Claims) != len(filtered.Claims) {
		return nil
	}
	return fmt.Errorf("%w: manifest prompt shadow mismatch", ErrInvalidTransition)
}
