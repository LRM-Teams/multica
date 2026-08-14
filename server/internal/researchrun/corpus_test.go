package researchrun

import (
	"errors"
	"testing"
	"time"
)

const (
	corpusQuestionID = "10000000-0000-4000-8000-000000000001"
	corpusOperatorID = "20000000-0000-4000-8000-000000000001"
)

func validCorpusBatch() corpusBatchCommand {
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	return corpusBatchCommand{
		GoalVersion: 2, PlanVersion: 4,
		Plans: []searchPlanCommand{{
			ClientKey: "search-market", Targets: []corpusTargetRef{{Kind: "question", ID: corpusQuestionID}},
			Adapter: "web_search_v1", QueryStrategies: []string{"independent primary sources"}, Languages: []string{"en"},
			InclusionCriteria: []string{"direct evidence"}, ExclusionCriteria: []string{"unsourced summary"},
			StoppingConditions: []string{"two independent families"}, MaximumCost: 2, StrategyVersion: "strategy-v1",
		}},
		Executions: []queryExecutionCommand{{ClientKey: "query-market", SearchPlanKey: "search-market", Adapter: "web_search_v1", Query: "market evidence", StartedAt: now, FinishedAt: now.Add(time.Second), Outcome: "succeeded", CursorOut: "next", Cost: []byte(`{"usd":0.2}`), Safety: []byte(`{"prompt_injection":"none"}`)}},
		Candidates: []sourceCandidateCommand{{ClientKey: "candidate-registry", QueryExecutionKey: "query-market", URL: "https://example.com/registry", CanonicalIdentity: "url:https://example.com/registry", ContentHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Title: "Registry", Summary: "Primary registry evidence.", Publisher: "Example", IndependenceFamily: "publisher:example-registry", RiskFlags: []string{"dynamic_content"}, ResultPosition: 1}},
		Decisions:  []screeningDecisionCommand{{ClientKey: "screen-registry", CandidateKey: "candidate-registry", Outcome: "include", Rule: "direct_primary", Reason: "Directly answers the target question.", OperatorKind: "agent", OperatorID: corpusOperatorID, GoalVersion: 2, PlanVersion: 4, ReviewedAgainstPlan: true}},
	}
}

func TestCorpusModuleValidateBatch(t *testing.T) {
	validation, err := (corpusModule{}).ValidateBatch(validCorpusBatch())
	if err != nil {
		t.Fatalf("ValidateBatch: %v", err)
	}
	if len(validation.IncludedCandidateKeys) != 1 || validation.IncludedCandidateKeys[0] != "candidate-registry" {
		t.Fatalf("included=%v", validation.IncludedCandidateKeys)
	}
}

func TestCorpusModuleValidateBatchFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*corpusBatchCommand)
		want   error
	}{
		{name: "unresolved target", mutate: func(c *corpusBatchCommand) { c.Plans[0].Targets[0].ID = "q-root" }, want: ErrInvalidContract},
		{name: "adapter mismatch", mutate: func(c *corpusBatchCommand) { c.Executions[0].Adapter = "other" }, want: ErrInvalidContract},
		{name: "failed query candidate", mutate: func(c *corpusBatchCommand) {
			c.Executions[0].Outcome = "failed"
			c.Executions[0].FailureClass = "provider_unavailable"
		}, want: ErrInvalidContract},
		{name: "invalid cost", mutate: func(c *corpusBatchCommand) { c.Executions[0].Cost = []byte(`[]`) }, want: ErrInvalidContract},
		{name: "invalid content hash", mutate: func(c *corpusBatchCommand) { c.Candidates[0].ContentHash = "aaaaaaaa" }, want: ErrInvalidContract},
		{name: "noncanonical URL", mutate: func(c *corpusBatchCommand) { c.Candidates[0].URL = " HTTPS://EXAMPLE.COM/registry " }, want: ErrInvalidContract},
		{name: "unclustered duplicate canonical identity", mutate: func(c *corpusBatchCommand) {
			second := c.Candidates[0]
			second.ClientKey = "candidate-copy"
			second.ResultPosition = 2
			c.Candidates = append(c.Candidates, second)
		}, want: ErrInvalidContract},
		{name: "duplicate decision", mutate: func(c *corpusBatchCommand) {
			second := c.Decisions[0]
			second.ClientKey = "screen-again"
			c.Decisions = append(c.Decisions, second)
		}, want: ErrInvalidContract},
		{name: "stale method", mutate: func(c *corpusBatchCommand) { c.Decisions[0].PlanVersion = 3 }, want: ErrControlTargetChanged},
		{name: "not reviewed against plan", mutate: func(c *corpusBatchCommand) { c.Decisions[0].ReviewedAgainstPlan = false }, want: ErrInvalidContract},
		{name: "rewrite cycle", mutate: func(c *corpusBatchCommand) {
			second := c.Executions[0]
			second.ClientKey = "query-rewrite"
			second.ParentExecutionKey = "query-market"
			c.Executions[0].ParentExecutionKey = "query-rewrite"
			c.Executions = append(c.Executions, second)
		}, want: ErrInvalidContract},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := validCorpusBatch()
			test.mutate(&command)
			if _, err := (corpusModule{}).ValidateBatch(command); !errors.Is(err, test.want) {
				t.Fatalf("ValidateBatch err=%v want %v", err, test.want)
			}
		})
	}
}

func TestCorpusModuleValidateBatchPreservesAuditedDuplicates(t *testing.T) {
	command := validCorpusBatch()
	command.Candidates[0].DuplicateCluster = "content:registry"
	second := command.Candidates[0]
	second.ClientKey = "candidate-copy"
	second.ResultPosition = 2
	command.Candidates = append(command.Candidates, second)
	if _, err := (corpusModule{}).ValidateBatch(command); err != nil {
		t.Fatalf("ValidateBatch clustered duplicate: %v", err)
	}
}

func TestCorpusModuleValidateSourceAdmission(t *testing.T) {
	module := corpusModule{}
	if err := module.ValidateSourceAdmission(sourceAdmissionCommand{CandidateID: corpusQuestionID, ScreeningDecisionID: corpusOperatorID, ScreeningOutcome: "include"}); err != nil {
		t.Fatalf("retrieved admission: %v", err)
	}
	if err := module.ValidateSourceAdmission(sourceAdmissionCommand{DirectIngestionKind: "workspace_artifact"}); err != nil {
		t.Fatalf("direct admission: %v", err)
	}
	for _, command := range []sourceAdmissionCommand{
		{},
		{CandidateID: corpusQuestionID},
		{CandidateID: corpusQuestionID, ScreeningDecisionID: corpusOperatorID, ScreeningOutcome: "exclude"},
		{CandidateID: corpusQuestionID, ScreeningDecisionID: corpusOperatorID, ScreeningOutcome: "include", DirectIngestionKind: "tool_output"},
		{DirectIngestionKind: "retrieved"},
	} {
		if err := module.ValidateSourceAdmission(command); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("ValidateSourceAdmission(%+v) err=%v", command, err)
		}
	}
}
