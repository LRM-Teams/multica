package researchrun

import "fmt"

type EpisodeSource struct {
	WorkspaceID     string
	RunID           string
	CycleID         string
	CycleEnded      bool
	RunTerminal     bool
	ContractVersion int
	PlanVersion     int
	StrategyVersion string
	DecisionIDs     []string
	ArtifactRefs    []EpisodeArtifactRef
	Metrics         EpisodeMetrics
}

type EpisodeArtifactRef struct {
	WorkspaceID string
	ArtifactID  string
	Kind        string
}

type EpisodeMetrics struct {
	AcceptedResults       int
	RejectedResults       int
	DuplicateTaskRate     float64
	ConflictDetectionRate float64
	CitationSupportRate   float64
	Cost                  float64
	LatencyMS             int64
}

type ResearchEpisode struct {
	WorkspaceID     string
	RunID           string
	CycleID         string
	ContractVersion int
	PlanVersion     int
	StrategyVersion string
	DecisionIDs     []string
	ArtifactIDs     []string
	Metrics         EpisodeMetrics
	ReadOnly        bool
}

func BuildResearchEpisode(source EpisodeSource) (ResearchEpisode, error) {
	if source.WorkspaceID == "" || source.RunID == "" || source.CycleID == "" || (!source.CycleEnded && !source.RunTerminal) || source.ContractVersion <= 0 || source.PlanVersion <= 0 || source.StrategyVersion == "" {
		return ResearchEpisode{}, fmt.Errorf("%w: Episode requires an ended delivery cycle or terminal Run and pinned versions", ErrInvalidContract)
	}
	if source.Metrics.AcceptedResults < 0 || source.Metrics.RejectedResults < 0 || source.Metrics.DuplicateTaskRate < 0 || source.Metrics.DuplicateTaskRate > 1 || source.Metrics.ConflictDetectionRate < 0 || source.Metrics.ConflictDetectionRate > 1 || source.Metrics.CitationSupportRate < 0 || source.Metrics.CitationSupportRate > 1 || source.Metrics.Cost < 0 || source.Metrics.LatencyMS < 0 {
		return ResearchEpisode{}, fmt.Errorf("%w: Episode metrics are invalid", ErrInvalidContract)
	}
	episode := ResearchEpisode{WorkspaceID: source.WorkspaceID, RunID: source.RunID, CycleID: source.CycleID, ContractVersion: source.ContractVersion, PlanVersion: source.PlanVersion, StrategyVersion: source.StrategyVersion, DecisionIDs: uniqueEpisodeValues(source.DecisionIDs), Metrics: source.Metrics, ReadOnly: true}
	for _, ref := range source.ArtifactRefs {
		if ref.WorkspaceID != source.WorkspaceID {
			return ResearchEpisode{}, fmt.Errorf("%w: Episode cannot reference another workspace", ErrInvalidContract)
		}
		if ref.ArtifactID == "" || ref.Kind == "" {
			return ResearchEpisode{}, fmt.Errorf("%w: Episode artifact reference is incomplete", ErrInvalidContract)
		}
		episode.ArtifactIDs = append(episode.ArtifactIDs, ref.ArtifactID)
	}
	episode.ArtifactIDs = uniqueEpisodeValues(episode.ArtifactIDs)
	return episode, nil
}

func uniqueEpisodeValues(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
