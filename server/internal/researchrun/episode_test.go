package researchrun

import "testing"

func episodeFixture() EpisodeSource {
	return EpisodeSource{WorkspaceID: "w1", RunID: "r1", CycleID: "cycle-1", CycleEnded: true, ContractVersion: 2, PlanVersion: 3, StrategyVersion: "s1", DecisionIDs: []string{"d1"}, ArtifactRefs: []EpisodeArtifactRef{{WorkspaceID: "w1", ArtifactID: "a1", Kind: "claim"}}, Metrics: EpisodeMetrics{AcceptedResults: 2, DuplicateTaskRate: .1, ConflictDetectionRate: .9, CitationSupportRate: .95, Cost: 5, LatencyMS: 100}}
}

func TestBuildResearchEpisodeRequiresEndedCycleAndIsReadOnly(t *testing.T) {
	episode, err := BuildResearchEpisode(episodeFixture())
	if err != nil || !episode.ReadOnly || episode.StrategyVersion != "s1" {
		t.Fatalf("episode=%+v err=%v", episode, err)
	}
	source := episodeFixture()
	source.CycleEnded = false
	if _, err := BuildResearchEpisode(source); err == nil {
		t.Fatal("expected active cycle rejection")
	}
}

func TestBuildResearchEpisodeRejectsCrossWorkspaceArtifacts(t *testing.T) {
	source := episodeFixture()
	source.ArtifactRefs[0].WorkspaceID = "other"
	if _, err := BuildResearchEpisode(source); err == nil {
		t.Fatal("expected cross-workspace reference rejection")
	}
}

func TestResearchEpisodeContainsReferencesAndMetricsNotPrivatePayloads(t *testing.T) {
	episode, err := BuildResearchEpisode(episodeFixture())
	if err != nil || len(episode.ArtifactIDs) != 1 || episode.Metrics.AcceptedResults != 2 {
		t.Fatalf("episode=%+v err=%v", episode, err)
	}
}
