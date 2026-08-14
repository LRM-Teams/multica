package researchrun

import (
	"encoding/json"
	"fmt"
	"sort"
)

type shadowRepresentationRecord struct {
	Kind       ArtifactEntityKind `json:"kind"`
	ArtifactID string             `json:"artifact_id"`
	ParentID   string             `json:"parent_id,omitempty"`
	Ordinal    int                `json:"ordinal"`
	Bytes      []byte             `json:"bytes"`
	Hash       string             `json:"hash"`
}

// compareShadowSnapshotRepresentations proves that every retained evidence
// representation is byte-identical to the independently loaded live snapshot.
// Evidence Links are separate nested records so an allowed Claim cannot carry
// an omitted link into an Agent prompt.
func compareShadowSnapshotRepresentations(live, filtered RunSnapshot) error {
	allowed := collectSnapshotRepresentationIDs(filtered)
	liveProjection, err := projectSnapshotRepresentations(live, allowed, true)
	if err != nil {
		return err
	}
	filteredProjection, err := projectSnapshotRepresentations(filtered, allowed, false)
	if err != nil {
		return err
	}
	if len(liveProjection) == len(filteredProjection) {
		match := true
		for i := range liveProjection {
			if liveProjection[i].Kind != filteredProjection[i].Kind ||
				liveProjection[i].ArtifactID != filteredProjection[i].ArtifactID ||
				liveProjection[i].ParentID != filteredProjection[i].ParentID ||
				liveProjection[i].Ordinal != filteredProjection[i].Ordinal ||
				liveProjection[i].Hash != filteredProjection[i].Hash ||
				string(liveProjection[i].Bytes) != string(filteredProjection[i].Bytes) {
				match = false
				break
			}
		}
		if match {
			return nil
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"live_representations":     liveProjection,
		"filtered_representations": filteredProjection,
	})
	return fmt.Errorf("%w: manifest representation shadow mismatch: %s", ErrInvalidTransition, payload)
}

func collectSnapshotRepresentationIDs(snapshot RunSnapshot) map[string]struct{} {
	ids := make(map[string]struct{}, len(snapshot.Sources)+len(snapshot.Observations)+len(snapshot.Claims))
	for _, source := range snapshot.Sources {
		ids[source.ID] = struct{}{}
	}
	for _, observation := range snapshot.Observations {
		ids[observation.ID] = struct{}{}
	}
	for _, claim := range snapshot.Claims {
		ids[claim.ID] = struct{}{}
		for _, evidence := range claim.Evidence {
			if evidence.ArtifactID != "" {
				ids[evidence.ArtifactID] = struct{}{}
			}
		}
	}
	return ids
}

func projectSnapshotRepresentations(snapshot RunSnapshot, allowed map[string]struct{}, selectAllowed bool) ([]shadowRepresentationRecord, error) {
	records := make([]shadowRepresentationRecord, 0, len(allowed))
	ordinals := make(map[string]int)
	appendRecord := func(kind ArtifactEntityKind, id, parent string, value any) error {
		if id == "" {
			return fmt.Errorf("%w: shadow representation has no artifact ID", ErrInvalidTransition)
		}
		if selectAllowed {
			if _, ok := allowed[id]; !ok {
				return nil
			}
		} else if _, ok := allowed[id]; !ok {
			return fmt.Errorf("%w: filtered representation %s/%s is outside its own manifest set", ErrInvalidTransition, kind, id)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode shadow representation %s/%s: %w", kind, id, err)
		}
		ordinalKey := string(kind) + "\x00" + parent
		ordinal := ordinals[ordinalKey]
		ordinals[ordinalKey] = ordinal + 1
		records = append(records, shadowRepresentationRecord{
			Kind: kind, ArtifactID: id, ParentID: parent, Ordinal: ordinal,
			Bytes: encoded, Hash: contentHashFromPayload(encoded),
		})
		return nil
	}
	for _, source := range snapshot.Sources {
		if err := appendRecord(ArtifactKindSourceSnapshot, source.ID, "", source); err != nil {
			return nil, err
		}
	}
	for _, observation := range snapshot.Observations {
		if err := appendRecord(ArtifactKindObservation, observation.ID, observation.SourceSnapshotID, observation); err != nil {
			return nil, err
		}
	}
	for _, claim := range snapshot.Claims {
		claimBody := claim
		claimBody.Evidence = nil
		if err := appendRecord(ArtifactKindClaim, claim.ID, "", claimBody); err != nil {
			return nil, err
		}
		for _, evidence := range claim.Evidence {
			if err := appendRecord(ArtifactKindEvidenceLink, evidence.ArtifactID, claim.ID, evidence); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		if records[i].ParentID != records[j].ParentID {
			return records[i].ParentID < records[j].ParentID
		}
		if records[i].Ordinal != records[j].Ordinal {
			return records[i].Ordinal < records[j].Ordinal
		}
		return records[i].ArtifactID < records[j].ArtifactID
	})
	return records, nil
}
