package researchrun

import (
	"encoding/json"
	"testing"
)

func TestBindServerOwnedV6ReportPackageOverwritesAgentFrozenCopies(t *testing.T) {
	in := v6ReportSubmission{
		GoalVersion:       99,
		InputSnapshotHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		InputNodes: []V6NodeRef{{
			Kind: "result_s", ID: "10c8a6f9-b080-436d-ba32-aa20fcea12e5",
			VersionID: "b0108037-06fa-40e9-8ef6-7487d3396ad9", Tier: V6TierS,
			ContentHash: "sha256:ec3083f5fa4bf621f9dc023532c0351e93d4100dbd32acf27fcb2e0f3c683c04",
		}},
		PackageHash: "sha256:33b3384c2545a1b674a6ba2e0e40def56e3688f8bffe186012e0a7a6a06c974a",
		Citations:   json.RawMessage("null"),
	}
	frozen := []V6NodeRef{{
		Kind: "result_s", ID: "10c8a6f9-b080-436d-ba32-aa20fcea12e5",
		VersionID: "b0108037-06fa-40e9-8ef6-7487d3396ad9", Tier: V6TierS,
		ContentHash: "sha256:9335e3c9ed25a1080406a23ed7b6dc1e48d3d8f3d9521537d06bdb11bbc2767e",
	}}
	snapshot := "sha256:4794a400149536761cb090e74248d564c9a232d8dfe3c960994e2a4b25c4cb59"
	bindServerOwnedV6ReportPackage(&in, 1, snapshot, frozen)
	if in.GoalVersion != 1 || in.InputSnapshotHash != snapshot {
		t.Fatalf("goal/snapshot=%d %s", in.GoalVersion, in.InputSnapshotHash)
	}
	if len(in.InputNodes) != 1 || in.InputNodes[0].ContentHash != frozen[0].ContentHash {
		t.Fatalf("input nodes=%+v", in.InputNodes)
	}
	if string(in.Citations) != "[]" || string(in.Outline) != "[]" {
		t.Fatalf("citations=%s outline=%s", in.Citations, in.Outline)
	}
}
