package computer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

// CanonicalCloudOrigin is the production API/auth/WebSocket origin. The
// leagent.me and www.leagent.me historical hosts remain eligible migration
// evidence, but every adopted connection is normalized to this API origin.
const CanonicalCloudOrigin = cli.OfficialCloudAPIURL

type LegacyExclusion struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

// LegacyAdoptionPlan is either one verified, unambiguous Computer plus its
// complete Workspace connection set, or an explicit-choice result. Exclusions
// are safe diagnostic text and contain no credentials.
type LegacyAdoptionPlan struct {
	ComputerID  string             `json:"computer_id,omitempty"`
	Connections []WorkspaceBinding `json:"workspace_connections,omitempty"`
	Exclusions  []LegacyExclusion  `json:"exclusions,omitempty"`
	NeedsChoice bool               `json:"needs_choice"`
}

// PlanLegacyAdoption applies the fail-closed #2492 contract. Automatic
// adoption is possible only when every required proof is present and every
// eligible profile agrees on one immutable Computer identity.
func PlanLegacyAdoption(currentUserID string, evidence []LegacyEvidence) LegacyAdoptionPlan {
	plan := LegacyAdoptionPlan{}
	currentUserID = strings.TrimSpace(currentUserID)
	byComputer := map[string][]WorkspaceBinding{}

	for _, item := range evidence {
		source := strings.TrimSpace(item.Source)
		if source == "" {
			source = "legacy profile"
		}
		exclude := func(reason string) {
			plan.Exclusions = append(plan.Exclusions, LegacyExclusion{Source: source, Reason: reason})
		}
		if !isCanonicalOrigin(item.OriginHost) {
			exclude("non-canonical origin retained; not contacted or adopted")
			continue
		}
		if currentUserID == "" || !item.UserVerified || strings.TrimSpace(item.SignedInUser) != currentUserID {
			exclude("signed-in user ownership could not be proven")
			continue
		}
		if !item.WorkspaceVerified {
			exclude("Workspace membership and immutable identity could not be proven")
			continue
		}
		workspaceID := strings.TrimSpace(item.WorkspaceID)
		if _, err := uuid.Parse(workspaceID); err != nil {
			exclude("Workspace identity is not an immutable UUID")
			continue
		}
		ids := nonEmpty(item.ComputerIDCandidates)
		if len(ids) != 1 || !item.ComputerVerified {
			exclude("server-side Computer ownership could not be proven")
			continue
		}
		computerID := strings.TrimSpace(ids[0])
		if _, err := uuid.Parse(computerID); err != nil {
			exclude("server-side Computer ownership could not be proven")
			continue
		}
		byComputer[computerID] = append(byComputer[computerID], WorkspaceBinding{
			Environment:   string(cli.ServiceEnvironmentProduction),
			Origin:        CanonicalCloudOrigin,
			WorkspaceID:   workspaceID,
			WorkspaceSlug: strings.TrimSpace(item.WorkspaceSlug),
			ComputerID:    computerID,
			Active:        true,
		})
	}

	if len(byComputer) != 1 {
		if len(evidence) > 0 {
			plan.NeedsChoice = true
		}
		if len(byComputer) > 1 {
			plan.Exclusions = append(plan.Exclusions, LegacyExclusion{
				Source: "legacy identities",
				Reason: "multiple verified Computer identities disagree",
			})
		}
		sortLegacyPlan(&plan)
		return plan
	}

	for computerID, connections := range byComputer {
		plan.ComputerID = computerID
		seen := map[string]struct{}{}
		for _, connection := range connections {
			if _, exists := seen[connection.WorkspaceID]; exists {
				continue
			}
			seen[connection.WorkspaceID] = struct{}{}
			plan.Connections = append(plan.Connections, connection)
		}
	}
	sortLegacyPlan(&plan)
	return plan
}

func sortLegacyPlan(plan *LegacyAdoptionPlan) {
	sort.Slice(plan.Connections, func(i, j int) bool {
		return plan.Connections[i].WorkspaceID < plan.Connections[j].WorkspaceID
	})
	sort.Slice(plan.Exclusions, func(i, j int) bool {
		if plan.Exclusions[i].Source == plan.Exclusions[j].Source {
			return plan.Exclusions[i].Reason < plan.Exclusions[j].Reason
		}
		return plan.Exclusions[i].Source < plan.Exclusions[j].Source
	})
}

func (p LegacyAdoptionPlan) ChoiceError() error {
	if !p.NeedsChoice {
		return nil
	}
	return fmt.Errorf("legacy Computer evidence is ambiguous; inspect `multica computer doctor`, then choose `multica computer identity adopt <computer-id>` or `multica computer identity fresh`")
}
