package researchrun

import "context"

type RecordV6MatchDecisionInput struct {
	WorkspaceID, RunID, CandidateSetHash, BranchScopeHash string
	Decision, ReasonCode, ReasonDetail, DecidedBy         string
	DirectorCycleID                                       string
	GoalVersion                                           int
	Inputs                                                []V6NodeRef
	BranchRefs                                            []V6BranchRef
}

type V6MatchDecision struct {
	ID, CandidateSetHash, BranchScopeHash, Decision, ReasonCode, ReasonDetail string
	GoalVersion                                                               int
}

type matchV6Store interface {
	RecordV6MatchDecision(context.Context, RecordV6MatchDecisionInput) (V6MatchDecision, error)
}

type matchV6Module struct{ store matchV6Store }

func (m matchV6Module) Record(ctx context.Context, in RecordV6MatchDecisionInput) (V6MatchDecision, error) {
	if m.store == nil || len(in.Inputs) == 0 || len(in.BranchRefs) == 0 || in.CandidateSetHash != v6InputSetHash(in.Inputs) ||
		in.BranchScopeHash != v6BranchScopeHash(in.BranchRefs) {
		return V6MatchDecision{}, ErrInvalidContract
	}
	return m.store.RecordV6MatchDecision(ctx, in)
}
