package researchrun

import "fmt"

type DisputeKind string

const (
	DisputeKindLogical              DisputeKind = "logical"
	DisputeKindSourceInterpretation DisputeKind = "source_interpretation"
	DisputeKindVersion              DisputeKind = "version"
	DisputeKindUnit                 DisputeKind = "unit"
	DisputeKindScope                DisputeKind = "scope"
	DisputeKindMethod               DisputeKind = "method"
	DisputeKindSemantic             DisputeKind = "semantic"
)

type DisputeSeverity string

const (
	DisputeSeverityAdvisory DisputeSeverity = "advisory"
	DisputeSeverityBlocking DisputeSeverity = "blocking"
)

type DisputeStatus string

const (
	DisputeStatusOpen                  DisputeStatus = "open"
	DisputeStatusInvestigating         DisputeStatus = "investigating"
	DisputeStatusResolved              DisputeStatus = "resolved"
	DisputeStatusConditionallyResolved DisputeStatus = "conditionally_resolved"
	DisputeStatusIrreducible           DisputeStatus = "irreducible"
	DisputeStatusObsolete              DisputeStatus = "obsolete"
)

type DisputePosition struct {
	PositionID    string
	AuthorAgentID string
	ClaimIDs      []string
	EvidenceIDs   []string
	ScopeHash     string
	Statement     string
}

type DisputeProposal struct {
	Kind              DisputeKind
	Severity          DisputeSeverity
	SubjectArtifactID string
	Positions         []DisputePosition
	Materiality       float64
	ResolutionRequest string
}

// ValidateDisputeProposal validates a normalized post-envelope proposal. The
// V6 wire contract intentionally leaves Position objects open; decoding those
// objects into this policy type happens before this validator is called.
func ValidateDisputeProposal(proposal DisputeProposal) error {
	if !validDisputeKind(proposal.Kind) {
		return fmt.Errorf("%w: unsupported dispute kind %q", ErrInvalidContract, proposal.Kind)
	}
	if proposal.Severity != DisputeSeverityAdvisory && proposal.Severity != DisputeSeverityBlocking {
		return fmt.Errorf("%w: unsupported dispute severity %q", ErrInvalidContract, proposal.Severity)
	}
	if proposal.SubjectArtifactID == "" || proposal.ResolutionRequest == "" {
		return fmt.Errorf("%w: dispute subject and resolution request are required", ErrInvalidContract)
	}
	if proposal.Materiality < 0 || proposal.Materiality > 1 {
		return fmt.Errorf("%w: dispute materiality must be in [0,1]", ErrInvalidContract)
	}
	if len(proposal.Positions) < 2 || len(proposal.Positions) > 16 {
		return fmt.Errorf("%w: dispute must contain 2 to 16 positions", ErrInvalidContract)
	}
	positionIDs := make(map[string]struct{}, len(proposal.Positions))
	substantive := make(map[string]struct{}, len(proposal.Positions))
	for _, position := range proposal.Positions {
		if position.PositionID == "" || position.AuthorAgentID == "" || position.Statement == "" || position.ScopeHash == "" {
			return fmt.Errorf("%w: position identity, author, statement, and scope are required", ErrInvalidContract)
		}
		if len(position.ClaimIDs) == 0 {
			return fmt.Errorf("%w: position %q must reference a Claim", ErrInvalidContract, position.PositionID)
		}
		if _, exists := positionIDs[position.PositionID]; exists {
			return fmt.Errorf("%w: duplicate position %q", ErrInvalidContract, position.PositionID)
		}
		positionIDs[position.PositionID] = struct{}{}
		identity := position.ScopeHash + "\x00" + position.Statement
		if _, exists := substantive[identity]; exists {
			return fmt.Errorf("%w: positions must be substantively distinct", ErrInvalidContract)
		}
		substantive[identity] = struct{}{}
	}
	return nil
}

type DisputeResolution struct {
	Status                DisputeStatus
	AdjudicatorAgentID    string
	VerifiedEvidenceIDs   []string
	Explanation           string
	ResidualUncertainty   string
	UpstreamInvalidated   bool
	HumanDecisionRequired bool
}

func ValidateDisputeTransition(current DisputeStatus, resolution DisputeResolution, positionAuthorIDs []string) error {
	if !allowedDisputeTransition(current, resolution.Status) {
		return fmt.Errorf("%w: dispute transition %q -> %q is not allowed", ErrInvalidTransition, current, resolution.Status)
	}
	if resolution.Status == DisputeStatusInvestigating {
		return nil
	}
	if resolution.Explanation == "" {
		return fmt.Errorf("%w: terminal dispute transition requires an explanation", ErrInvalidContract)
	}
	if resolution.Status == DisputeStatusObsolete {
		if !resolution.UpstreamInvalidated {
			return fmt.Errorf("%w: obsolete dispute requires invalidated upstream state", ErrInvalidContract)
		}
		return nil
	}
	if resolution.AdjudicatorAgentID == "" {
		return fmt.Errorf("%w: adjudicator is required", ErrInvalidContract)
	}
	for _, authorID := range positionAuthorIDs {
		if authorID == resolution.AdjudicatorAgentID {
			return fmt.Errorf("%w: adjudicator must be independent from position authors", ErrInvalidContract)
		}
	}
	if resolution.Status == DisputeStatusResolved && len(resolution.VerifiedEvidenceIDs) == 0 {
		return fmt.Errorf("%w: resolved dispute requires verified evidence", ErrInvalidContract)
	}
	if (resolution.Status == DisputeStatusConditionallyResolved || resolution.Status == DisputeStatusIrreducible) && resolution.ResidualUncertainty == "" {
		return fmt.Errorf("%w: conditional or irreducible resolution requires residual uncertainty", ErrInvalidContract)
	}
	return nil
}

type DisputeDeliveryObligation struct {
	BlocksDelivery     bool
	MustAppearInReport bool
	RequiresHumanGate  bool
}

func DisputeDeliveryPolicy(severity DisputeSeverity, status DisputeStatus, humanDecisionRequired bool) (DisputeDeliveryObligation, error) {
	if severity != DisputeSeverityAdvisory && severity != DisputeSeverityBlocking {
		return DisputeDeliveryObligation{}, fmt.Errorf("%w: unsupported dispute severity %q", ErrInvalidContract, severity)
	}
	if !validDisputeStatus(status) {
		return DisputeDeliveryObligation{}, fmt.Errorf("%w: unsupported dispute status %q", ErrInvalidContract, status)
	}
	return DisputeDeliveryObligation{
		BlocksDelivery:     severity == DisputeSeverityBlocking && (status == DisputeStatusOpen || status == DisputeStatusInvestigating),
		MustAppearInReport: status == DisputeStatusConditionallyResolved || status == DisputeStatusIrreducible,
		RequiresHumanGate:  humanDecisionRequired,
	}, nil
}

func validDisputeKind(kind DisputeKind) bool {
	switch kind {
	case DisputeKindLogical, DisputeKindSourceInterpretation, DisputeKindVersion, DisputeKindUnit, DisputeKindScope, DisputeKindMethod, DisputeKindSemantic:
		return true
	default:
		return false
	}
}

func validDisputeStatus(status DisputeStatus) bool {
	switch status {
	case DisputeStatusOpen, DisputeStatusInvestigating, DisputeStatusResolved, DisputeStatusConditionallyResolved, DisputeStatusIrreducible, DisputeStatusObsolete:
		return true
	default:
		return false
	}
}

func allowedDisputeTransition(current, next DisputeStatus) bool {
	switch current {
	case DisputeStatusOpen:
		return next == DisputeStatusInvestigating || next == DisputeStatusObsolete
	case DisputeStatusInvestigating:
		return next == DisputeStatusResolved || next == DisputeStatusConditionallyResolved || next == DisputeStatusIrreducible || next == DisputeStatusObsolete
	default:
		return false
	}
}
