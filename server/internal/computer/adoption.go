package computer

import (
	"fmt"
	"strings"
)

// Legacy-adoption decision rules (#2492). Pure functions so the "what may be
// adopted automatically vs. must be chosen explicitly" contract is single and
// unit-testable. Adoption NEVER deletes legacy evidence; it only ever records
// an adopted identity after it is fully verifiable.

// CanonicHost is the single Cloud origin the Computer authenticates through.
const CanonicHost = "leagent.me"

// AdoptionVerdict is the outcome of evaluating legacy Computer evidence.
type AdoptionVerdict int

const (
	// AdoptAuto: evidence is provable and unambiguous → adopt automatically.
	AdoptAuto AdoptionVerdict = iota
	// AdoptNeedsExplicitChoice: ambiguity remains → require adopt/fresh-create.
	AdoptNeedsExplicitChoice
	// AdoptRejected: evidence is disqualified (localhost/custom origin/conflict).
	AdoptRejected
)

// LegacyEvidence is what is known about a candidate legacy identity.
type LegacyEvidence struct {
	// OriginHost is the server origin the legacy profile pointed at.
	OriginHost string
	// SignedInUser is present when the current user is provable.
	SignedInUser string
	// WorkspaceID is the immutable workspace the profile was bound to.
	WorkspaceID string
	// ComputerIDCandidates are the legacy computer ids found (per-profile).
	ComputerIDCandidates []string
	// HasHostnameEvidence / HasDisplayEvidence describe, but are never proof.
	HasHostnameEvidence bool
	HasDisplayEvidence  bool
}

// EvaluateAdoption decides how to treat a legacy candidate. Hostname and
// display-name are never identity proof; localhost/custom-origin/conflicting
// profiles are excluded from Cloud adoption; automatic adoption requires the
// canonical origin + a signed-in user + an immutable workspace id + exactly
// one unambiguous computer id.
func EvaluateAdoption(e LegacyEvidence) (AdoptionVerdict, error) {
	if !isCanonicalOrigin(e.OriginHost) {
		return AdoptRejected, fmt.Errorf("origin %q is not canonical and is excluded from Cloud adoption", e.OriginHost)
	}
	if e.SignedInUser == "" || e.WorkspaceID == "" {
		return AdoptNeedsExplicitChoice, nil
	}
	// Hostname/display-name are never identity proof — there must be a real
	// computer id to adopt, regardless of hostname/display evidence.

	ids := nonEmpty(e.ComputerIDCandidates)
	if len(ids) == 0 {
		return AdoptNeedsExplicitChoice, nil
	}
	if len(ids) > 1 {
		// Conflicting candidates → require an explicit adopt or fresh-create choice.
		return AdoptNeedsExplicitChoice, nil
	}
	return AdoptAuto, nil
}

func isCanonicalOrigin(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimSuffix(h, "/")
	if h == "" || h == "localhost" ||
		strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "192.168.") || strings.HasPrefix(h, "10.") {
		return false
	}
	return h == CanonicHost || h == "leagent.me"
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
