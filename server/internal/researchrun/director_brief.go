package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type StartV6DirectorCycleInput struct {
	WorkspaceID, RunID, TriggerKey string
	FromSequence, ThroughSequence  int64
	ExpectedStateVersion           int64
	Now                            time.Time
}

type V6DirectorCycle struct {
	ID, RunID, AssignmentID, BriefID, BriefHash, WorkItemID string
	Generation, PageCount                                   int
	StateVersion                                            int64
	Status                                                  string
	Replayed                                                bool
}
type V6DirectorBriefPage struct {
	Bytes                        json.RawMessage
	PageKey, PageHash, BriefHash string
	Ordinal, PageCount           int
	Reviewed                     bool
}
type AcknowledgeV6DirectorBriefInput struct {
	V6AttemptAccess
	ClientRequestID, BriefID, BriefHash, PageKey, PageHash string
}

type directorBriefStore interface {
	LoadDirectorBriefFacts(context.Context, StartV6DirectorCycleInput) (DirectorBriefFacts, error)
	PersistDirectorCycle(context.Context, StartV6DirectorCycleInput, CompiledDirectorBrief) (V6DirectorCycle, error)
	LoadDirectorBriefPage(context.Context, V6AttemptAccess, string) (V6DirectorBriefPage, error)
	AcknowledgeDirectorBriefPage(context.Context, AcknowledgeV6DirectorBriefInput) error
}
type directorBriefModule struct {
	store    directorBriefStore
	compiler contextCompilerModule
}

func (m directorBriefModule) Start(ctx context.Context, in StartV6DirectorCycleInput) (V6DirectorCycle, error) {
	if m.store == nil || strings.TrimSpace(in.TriggerKey) == "" || in.ExpectedStateVersion < 0 {
		return V6DirectorCycle{}, fmt.Errorf("%w: invalid Director cycle", ErrInvalidContract)
	}
	facts, err := m.store.LoadDirectorBriefFacts(ctx, in)
	if err != nil {
		return V6DirectorCycle{}, err
	}
	compiled, err := m.compiler.CompileDirectorBrief(facts, in.Now)
	if err != nil {
		return V6DirectorCycle{}, err
	}
	return m.store.PersistDirectorCycle(ctx, in, compiled)
}
func (m directorBriefModule) Page(ctx context.Context, access V6AttemptAccess, cursor string) (V6DirectorBriefPage, error) {
	return m.store.LoadDirectorBriefPage(ctx, access, cursor)
}
func (m directorBriefModule) Acknowledge(ctx context.Context, in AcknowledgeV6DirectorBriefInput) error {
	return m.store.AcknowledgeDirectorBriefPage(ctx, in)
}
