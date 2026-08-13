package researchrun

import (
	"errors"
	"testing"
)

func TestInquiryModuleStatusTransitionsFailClosed(t *testing.T) {
	module := inquiryModule{}
	tests := []struct {
		kind     InquiryEntityKind
		from, to string
		allowed  bool
	}{
		{InquiryKindHypothesis, "proposed", "investigating", true},
		{InquiryKindHypothesis, "proposed", "supported", false},
		{InquiryKindHypothesis, "obsolete", "investigating", false},
		{InquiryKindBranch, "active", "paused", true},
		{InquiryKindBranch, "completed", "active", false},
		{InquiryKindInsight, "accepted", "stale", true},
		{InquiryKindInsight, "obsolete", "accepted", false},
		{InquiryEntityKind("search"), "pending", "active", false},
		{InquiryEntityKind("search"), "pending", "pending", false},
	}
	for _, tt := range tests {
		err := module.ValidateTransition(tt.kind, tt.from, tt.to)
		if tt.allowed && err != nil {
			t.Fatalf("%s %s -> %s: %v", tt.kind, tt.from, tt.to, err)
		}
		if !tt.allowed && !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s %s -> %s err=%v want ErrInvalidTransition", tt.kind, tt.from, tt.to, err)
		}
	}
}

func TestInquiryModuleEdgeVocabularyAndDAGRelations(t *testing.T) {
	module := inquiryModule{}
	valid := inquiryEdgeCommand{
		From:     inquiryEndpoint{Kind: InquiryKindQuestion, ID: "question-1"},
		To:       inquiryEndpoint{Kind: InquiryKindHypothesis, ID: "hypothesis-1"},
		Relation: InquiryRelationTests,
	}
	if err := module.ValidateEdge(valid); err != nil {
		t.Fatal(err)
	}
	self := valid
	self.To = self.From
	if err := module.ValidateEdge(self); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("self edge err=%v", err)
	}
	unknown := valid
	unknown.Relation = "summarizes"
	if err := module.ValidateEdge(unknown); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("unknown relation err=%v", err)
	}
	for _, relation := range []InquiryRelation{InquiryRelationDecomposes, InquiryRelationDependsOn, InquiryRelationRefines} {
		if !inquiryRelationMustBeAcyclic(relation) {
			t.Fatalf("relation %q must be acyclic", relation)
		}
	}
	if inquiryRelationMustBeAcyclic(InquiryRelationTests) {
		t.Fatal("tests relation may participate in a semantic feedback loop")
	}
}

func TestInquiryModuleTaskTargetsFailClosed(t *testing.T) {
	module := inquiryModule{}
	valid := []TaskInquiryTarget{{Kind: InquiryKindQuestion, EntityID: "question-1"}, {Kind: InquiryKindHypothesis, EntityID: "hypothesis-1"}}
	if err := module.ValidateTaskTargets(valid); err != nil {
		t.Fatal(err)
	}
	for name, targets := range map[string][]TaskInquiryTarget{
		"empty":            nil,
		"duplicate":        {valid[0], valid[0]},
		"dispute before H": {{Kind: InquiryKindDispute, EntityID: "dispute-1"}},
		"unknown":          {{Kind: InquiryEntityKind("search_plan"), EntityID: "search-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := module.ValidateTaskTargets(targets); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
