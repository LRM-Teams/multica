package researchrun

import (
	"context"
	"errors"
	"testing"
)

type recordingArtifactLifecycleStore struct {
	change  artifactLifecycleChange
	outcome artifactLifecycleOutcome
	err     error
}

func (store *recordingArtifactLifecycleStore) ApplyArtifactLifecycleChange(_ context.Context, change artifactLifecycleChange) (artifactLifecycleOutcome, error) {
	store.change = change
	return store.outcome, store.err
}

func TestArtifactLifecycleModuleValidatesIntentBeforePersistence(t *testing.T) {
	store := &recordingArtifactLifecycleStore{outcome: artifactLifecycleOutcome{ArtifactID: "artifact-1"}}
	module := artifactLifecycleModule{store: store}
	valid := artifactLifecycleChange{
		OperationID: "operation-1", WorkspaceID: "workspace-1", SessionID: "session-1",
		ArtifactID: "artifact-1", Kind: artifactLifecycleWithdraw, Reason: "fact withdrawn",
	}
	if _, err := module.Change(context.Background(), valid); err != nil {
		t.Fatalf("valid withdrawal: %v", err)
	}
	if store.change.ActorType != "system" {
		t.Fatalf("default actor type=%q want system", store.change.ActorType)
	}
	for name, mutate := range map[string]func(*artifactLifecycleChange){
		"missing operation": func(change *artifactLifecycleChange) { change.OperationID = "" },
		"missing reason":    func(change *artifactLifecycleChange) { change.Reason = "" },
		"withdraw successor": func(change *artifactLifecycleChange) {
			change.SuccessorArtifactID = "successor"
		},
		"supersede self": func(change *artifactLifecycleChange) {
			change.Kind = artifactLifecycleSupersede
			change.SuccessorArtifactID = change.ArtifactID
			change.DecisionID = "decision"
		},
		"supersede without decision": func(change *artifactLifecycleChange) {
			change.Kind = artifactLifecycleSupersede
			change.SuccessorArtifactID = "successor"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			before := store.change
			if _, err := module.Change(context.Background(), candidate); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
			if store.change != before {
				t.Fatal("invalid intent reached persistence seam")
			}
		})
	}
}
