package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResearchProjectorFailsBeforeDatabaseAccessWithoutAuthorizedSnapshot(t *testing.T) {
	event := researchrun.RunEvent{SessionID: "session", WorkspaceID: "workspace"}

	t.Run("missing projection capability", func(t *testing.T) {
		projector := &researchRunProjector{handler: &Handler{}}
		err := projector.Project(context.Background(), event)
		if err == nil || !strings.Contains(err.Error(), "snapshot reader is unavailable") {
			t.Fatalf("Project error=%v", err)
		}
	})

	t.Run("authorized snapshot load failure", func(t *testing.T) {
		loadErr := errors.New("projection authorization denied")
		engine := &recordingResearchRunEngine{snapshotForProjectionErr: loadErr}
		projector := &researchRunProjector{handler: &Handler{ResearchRun: engine}}
		err := projector.Project(context.Background(), event)
		if !errors.Is(err, loadErr) {
			t.Fatalf("Project error=%v want wrapped load error", err)
		}
		if !engine.snapshotForProjectionCalled {
			t.Fatal("Project did not attempt the least-privilege snapshot")
		}
	})
}
