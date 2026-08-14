package researchrun

import (
	"os"
	"sort"
	"testing"
)

func artifactCanonicalTestVectorContent(kind ArtifactEntityKind) map[string]any {
	return map[string]any{
		"_test_vector": "d1j",
		"kind":         string(kind),
	}
}

func TestRegisteredArtifactSchemaFamiliesCoverInventory(t *testing.T) {
	kinds := RegisteredArtifactEntityKinds()
	families := RegisteredArtifactSchemaFamilies()
	if len(families) != len(kinds) {
		t.Fatalf("schema families=%d kinds=%d", len(families), len(kinds))
	}
	for _, kind := range kinds {
		family, ok := families[kind]
		if !ok {
			t.Fatalf("missing schema family for kind %q", kind)
		}
		if family.SchemaName != string(kind) {
			t.Fatalf("schema name=%q want %q", family.SchemaName, kind)
		}
		if family.SchemaVersion != artifactLegacySchemaVersion {
			t.Fatalf("schema version=%q", family.SchemaVersion)
		}
		if family.CanonicalizationVersion != ArtifactCanonicalizationVersion {
			t.Fatalf("c14n version=%q", family.CanonicalizationVersion)
		}
	}
}

func TestMarshalArtifactCanonicalJSONSortsObjectKeys(t *testing.T) {
	first := map[string]any{
		"z": 1,
		"a": map[string]any{
			"nested_z": true,
			"nested_a": false,
		},
		"m": []any{"keep-order", 2},
	}
	second := map[string]any{
		"m": []any{"keep-order", 2},
		"a": map[string]any{
			"nested_a": false,
			"nested_z": true,
		},
		"z": 1,
	}
	firstBytes, err := MarshalArtifactCanonicalJSON(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondBytes, err := MarshalArtifactCanonicalJSON(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("canonical bytes differ:\nfirst=%s\nsecond=%s", firstBytes, secondBytes)
	}
	want := `{"a":{"nested_a":false,"nested_z":true},"m":["keep-order",2],"z":1}`
	if string(firstBytes) != want {
		t.Fatalf("canonical=%s want=%s", firstBytes, want)
	}
}

func TestArtifactCanonicalTestVectors(t *testing.T) {
	wantHashes := map[ArtifactEntityKind]string{
		ArtifactKindAttempt:                 "sha256:fd514dbd54cf1257729cca91d3f76ceaee59a13b7bc16cbe63af78669a89a05d",
		ArtifactKindBranch:                  "sha256:816d340d10128f77bb4a7461192b912e89e679b5f7828652ac486ff3c5d7b665",
		ArtifactKindClaim:                   "sha256:1b045a093bfdb9669cd08566c1856821552da22096a10491fb57c1ac63bfe381",
		ArtifactKindContextManifest:         "sha256:892cbc9e44d8848ac9a7793f3853039d6c740c3e679d5bbff6707edbb92ea67d",
		ArtifactKindContractRevision:        "sha256:3c32cb46b4bdc2e06c5cb86acfc7b3bb45d02bf98352d627b44f055355796ee9",
		ArtifactKindEvaluationDecision:      "sha256:bf1c0a5ea9d0c74ed868801f8451c0fb942775c3df0a3da0c5c8738d34dea397",
		ArtifactKindEvidenceLink:            "sha256:e273d7c9b1895334047fed87a75ac26c3357e972057c8c803b0b90c7e794b331",
		ArtifactKindGraphEdge:               "sha256:b126ec6ebfe107f30c6d164253a542ac83d0e0a2721c34f7b8046bb93cdbf3fb",
		ArtifactKindGraphNode:               "sha256:84243fa3e1f5b531497051b4d08dcd8d9d6feef75ef3ae25f6464f097e662eb8",
		ArtifactKindHypothesis:              "sha256:51e66938d67aeceee2c56d98984649058a49c66b156876d025348a2d0fdb8053",
		ArtifactKindInquiryEdge:             "sha256:3b0fbaa81b31561ad281cc04b1fdf90c0f7e19a2c8963ce8cf1790d1dfb548ef",
		ArtifactKindInsight:                 "sha256:adf8c419d7305ea850d9d4e5ae9b46791eef3c07ce2707b9b37ed44dbf3c42af",
		ArtifactKindIntegrationContribution: "sha256:73f6481e9aef5280ac62fdf40b6e21195b7e6836fb2d0557c11dd6a1400973bc",
		ArtifactKindLegacySource:            "sha256:7410eb392af128f3c95814bf60ca99aa1d9926680fa64ed8640a3a9b79671adb",
		ArtifactKindMethodDecision:          "sha256:12f728b8316e54dd36ad6c2aa4a710ad2f5816bbae5c30286596ee0e557d8b9e",
		ArtifactKindObservation:             "sha256:e49c9298680ff6c6e24b8aaa5dacfba9e875d65ca17357b5af4923a71ba6b5e8",
		ArtifactKindProductRoundDecision:    "sha256:6b4c77628e86914ca9dd1e976fcebefe66c29c3df72060da71d3a29bbf1af2fc",
		ArtifactKindQuestion:                "sha256:f65ceea78b68877e180970aa28dde69e3ff02ada01776cccc3fed8cfe4b8ec72",
		ArtifactKindQueryExecution:          "sha256:389d014713a462c4cddaa6a9404d4e08af676b5a5bf231ccd91eb621e85f9cfb",
		ArtifactKindReportRevision:          "sha256:8ff3e44fcb6e7268a85bc554e59c4ee415e870ff231965899fb57ba8ac16650e",
		ArtifactKindResearchMessage:         "sha256:f1b473b82da65722a522d2ef942516fa310c92913caec5343caf58682638db00",
		ArtifactKindResultArtifact:          "sha256:89b8552d2d77dce2a622700db73f7f4d0c343a79404dcb1f15cb020591e6b38b",
		ArtifactKindRunEvent:                "sha256:465bf4b41562ce2b17d2598fb114e16b5580cee36b8f70594534bfb99ba19b3e",
		ArtifactKindRunSession:              "sha256:50895381a481692f875cef805fd577551a41e9c4c7d347a709b9c21e59856dfc",
		ArtifactKindScreeningDecision:       "sha256:e2390ff9a67baf1a030f9d8082266215c088fb7e82362d83e59275c13bd20286",
		ArtifactKindSearchPlan:              "sha256:bdf2f07ed426e591f8f13e6265ca482b2717de2c9c6e321611aa8c165a18a68a",
		ArtifactKindSourceCandidate:         "sha256:577f0dec948c5485c769e647a89ff89b14a794d790e2180ebf19dfd2d3fbc4a0",
		ArtifactKindSourceSnapshot:          "sha256:1002f0864fbfa80b7af7be20a4ddd5d989d3bb410f8e8d1492e0b0a7bc5af26f",
		ArtifactKindStageEvaluation:         "sha256:2e005c506f708f927dfb01f116de004caa74fd4390cccc1031f7fb93ad67e145",
		ArtifactKindTask:                    "sha256:fd3c19485e52c2944282f4216830f4ab791fcb4b82c855bcee5092e6ab381fe4",
	}
	if len(wantHashes) != len(registeredArtifactEntityKinds) {
		t.Fatalf("vector count=%d kinds=%d", len(wantHashes), len(registeredArtifactEntityKinds))
	}
	for kind := range registeredArtifactEntityKinds {
		if _, ok := wantHashes[kind]; !ok {
			t.Fatalf("missing test vector for kind %q", kind)
		}
	}

	kinds := RegisteredArtifactEntityKinds()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	for _, kind := range kinds {
		got, err := ArtifactContentHash(kind, artifactCanonicalTestVectorContent(kind))
		if err != nil {
			t.Fatalf("hash kind=%s: %v", kind, err)
		}
		want := wantHashes[kind]
		if got != want {
			t.Fatalf("kind=%s hash=%q want=%q", kind, got, want)
		}
	}
}

func TestGenerateArtifactCanonicalTestVectorHashes(t *testing.T) {
	if os.Getenv("GENERATE_ARTIFACT_VECTORS") == "" {
		t.Skip("set GENERATE_ARTIFACT_VECTORS=1 to regenerate fixed vectors")
	}
	kinds := RegisteredArtifactEntityKinds()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	for _, kind := range kinds {
		hash, err := ArtifactContentHash(kind, artifactCanonicalTestVectorContent(kind))
		if err != nil {
			t.Fatalf("hash kind=%s: %v", kind, err)
		}
		t.Logf("%s: %q", kind, hash)
	}
}
