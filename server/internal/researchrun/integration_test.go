package researchrun

import (
	"errors"
	"testing"
)

const (
	integrationRoundID = "10000000-0000-4000-8000-000000000001"
	integrationAgentA  = "20000000-0000-4000-8000-000000000001"
	integrationAgentB  = "20000000-0000-4000-8000-000000000002"
	integrationTaskA   = "30000000-0000-4000-8000-000000000001"
	integrationTaskB   = "30000000-0000-4000-8000-000000000002"
	integrationClaimA  = "40000000-0000-4000-8000-000000000001"
	integrationClaimB  = "40000000-0000-4000-8000-000000000002"
)

func validIntegrationContext() integrationRoundContext {
	return integrationRoundContext{
		RoundID: integrationRoundID, ThroughEventSequence: 40, StateVersion: 12,
		Artifacts: []integrationArtifactState{
			{ID: integrationClaimA, Kind: "claim", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TaskID: integrationTaskA, AuthorAgentID: integrationAgentA, Status: "supported", Accessible: true},
			{ID: integrationClaimB, Kind: "claim", ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TaskID: integrationTaskB, AuthorAgentID: integrationAgentB, Status: "supported", Accessible: true},
		},
		Participants: []integrationParticipant{{AgentID: integrationAgentA, Availability: "available"}, {AgentID: integrationAgentB, Availability: "available"}},
	}
}

func validIntegrationBatch() integrationBatchCommand {
	return integrationBatchCommand{
		RoundID: integrationRoundID, ExpectedEventSequence: 40, ExpectedStateVersion: 12,
		Contributions: []integrationContributionCommand{
			{ClientKey: "contribution-a", IntegrationRoundID: integrationRoundID, AuthorAgentID: integrationAgentA, ComparedArtifactIDs: []string{integrationClaimA, integrationClaimB}, CommonFindings: []string{"Both describe total cost."}, UniqueFindings: []string{"A covers price."}, Scope: []byte(`{"region":"global"}`)},
			{ClientKey: "contribution-b", IntegrationRoundID: integrationRoundID, AuthorAgentID: integrationAgentB, ComparedArtifactIDs: []string{integrationClaimA, integrationClaimB}, CommonFindings: []string{"Both describe total cost."}, UniqueFindings: []string{"B covers migration."}, Scope: []byte(`{"region":"global"}`)},
		},
		Insights: []insightProposalCommand{{ClientKey: "insight-total-cost", Title: "Total cost differs from price", Summary: "Migration cost conditions the apparent price advantage.", InputIDs: []string{integrationClaimA, integrationClaimB}, Relation: "integrates", Scope: []byte(`{"region":"global"}`), SemanticValue: "new_explanation"}},
	}
}

func TestIntegrationModuleValidateAssimilationDecision(t *testing.T) {
	module := integrationModule{}
	for _, command := range []assimilationDecisionCommand{
		{ResultArtifactID: integrationClaimA, Routing: "no_related_artifacts", Rationale: "No comparable accepted artifacts."},
		{ResultArtifactID: integrationClaimA, Routing: "peer_synthesis", RelatedArtifactIDs: []string{integrationClaimB}, Rationale: "Complementary result."},
		{ResultArtifactID: integrationClaimA, Routing: "open_dispute", RelatedArtifactIDs: []string{integrationClaimB}, ConflictingClaimIDs: []string{integrationClaimB}, Rationale: "Materially incompatible claims."},
	} {
		if err := module.ValidateAssimilationDecision(command); err != nil {
			t.Fatalf("ValidateAssimilationDecision(%s): %v", command.Routing, err)
		}
	}
}

func TestIntegrationModuleValidateAssimilationDecisionFailsClosed(t *testing.T) {
	command := assimilationDecisionCommand{ResultArtifactID: integrationClaimA, Routing: "no_related_artifacts", RelatedArtifactIDs: []string{integrationClaimB}, Rationale: "Contradictory routing."}
	if err := (integrationModule{}).ValidateAssimilationDecision(command); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("err=%v", err)
	}
}

func TestIntegrationModuleValidateBatch(t *testing.T) {
	validation, err := (integrationModule{}).ValidateBatch(validIntegrationBatch(), validIntegrationContext())
	if err != nil {
		t.Fatalf("ValidateBatch: %v", err)
	}
	if len(validation.Derivations) != 1 || validation.Derivations[0].Level != 1 || len(validation.Derivations[0].IdempotencyKey) != 64 {
		t.Fatalf("derivations=%+v", validation.Derivations)
	}
	context := validIntegrationContext()
	context.Artifacts[0].Kind = "insight"
	context.Artifacts[0].Status = "accepted"
	context.Artifacts[0].InsightLevel = 2
	validation, err = (integrationModule{}).ValidateBatch(validIntegrationBatch(), context)
	if err != nil || validation.Derivations[0].Level != 3 {
		t.Fatalf("recursive derivation=%+v err=%v", validation.Derivations, err)
	}
}

func TestIntegrationModuleValidateBatchFailsClosed(t *testing.T) {
	tests := []struct {
		name          string
		mutateBatch   func(*integrationBatchCommand)
		mutateContext func(*integrationRoundContext)
		want          error
	}{
		{name: "stale watermark", mutateBatch: func(b *integrationBatchCommand) { b.ExpectedStateVersion-- }, want: ErrControlTargetChanged},
		{name: "forged unavailable author", mutateContext: func(c *integrationRoundContext) {
			c.Participants[0].Availability = "offline"
			c.Participants[0].AbsenceReason = "Agent disconnected."
		}, want: ErrInvalidContract},
		{name: "author has no input", mutateBatch: func(b *integrationBatchCommand) { b.Contributions[0].AuthorAgentID = integrationAgentB }, want: ErrInvalidContract},
		{name: "inaccessible input", mutateContext: func(c *integrationRoundContext) { c.Artifacts[1].Accessible = false }, want: ErrInvalidContract},
		{name: "same Task inputs", mutateContext: func(c *integrationRoundContext) { c.Artifacts[1].TaskID = integrationTaskA }, want: ErrInvalidContract},
		{name: "no semantic gain", mutateBatch: func(b *integrationBatchCommand) { b.Insights[0].SemanticValue = "shorter_words" }, want: ErrInvalidContract},
		{name: "duplicate derivation", mutateBatch: func(b *integrationBatchCommand) {
			second := b.Insights[0]
			second.ClientKey = "insight-copy"
			b.Insights = append(b.Insights, second)
		}, want: ErrInvalidContract},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch, context := validIntegrationBatch(), validIntegrationContext()
			if test.mutateBatch != nil {
				test.mutateBatch(&batch)
			}
			if test.mutateContext != nil {
				test.mutateContext(&context)
			}
			if _, err := (integrationModule{}).ValidateBatch(batch, context); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want %v", err, test.want)
			}
		})
	}
}

func TestIntegrationModuleRecordsUnavailableParticipantWithoutForgingContribution(t *testing.T) {
	context := validIntegrationContext()
	context.Participants[1] = integrationParticipant{AgentID: integrationAgentB, Availability: "offline", AbsenceReason: "Agent is no longer connected."}
	batch := validIntegrationBatch()
	batch.Contributions = batch.Contributions[:1]
	if _, err := (integrationModule{}).ValidateBatch(batch, context); err != nil {
		t.Fatalf("ValidateBatch: %v", err)
	}
}
