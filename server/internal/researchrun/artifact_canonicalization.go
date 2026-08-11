package researchrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

const artifactLegacySchemaVersion = "legacy-v1"

// ArtifactSchemaFamily names the immutable content schema for one entity kind.
type ArtifactSchemaFamily struct {
	SchemaName              string
	SchemaVersion           string
	CanonicalizationVersion string
}

// RegisteredArtifactSchemaFamilies returns the closed D1 schema registry.
func RegisteredArtifactSchemaFamilies() map[ArtifactEntityKind]ArtifactSchemaFamily {
	out := make(map[ArtifactEntityKind]ArtifactSchemaFamily, len(registeredArtifactEntityKinds))
	for kind := range registeredArtifactEntityKinds {
		out[kind] = ArtifactSchemaFamily{
			SchemaName:              string(kind),
			SchemaVersion:           artifactLegacySchemaVersion,
			CanonicalizationVersion: ArtifactCanonicalizationVersion,
		}
	}
	return out
}

// SchemaFamilyForKind returns the registered schema family for a kind.
func SchemaFamilyForKind(kind ArtifactEntityKind) (ArtifactSchemaFamily, error) {
	families := RegisteredArtifactSchemaFamilies()
	family, ok := families[kind]
	if !ok {
		return ArtifactSchemaFamily{}, fmt.Errorf("%w: unknown artifact entity kind %q", ErrInvalidContract, kind)
	}
	return family, nil
}

// MarshalArtifactCanonicalJSON renders research-artifact-c14n-v1 bytes:
// sorted object keys, semantic array order, json.Marshal scalars.
func MarshalArtifactCanonicalJSON(value any) ([]byte, error) {
	return marshalArtifactCanonicalValue(value)
}

// ArtifactContentHashFromCanonicalJSON hashes canonical bytes as sha256:<hex>.
func ArtifactContentHashFromCanonicalJSON(canonical []byte) string {
	return contentHashFromPayload(canonical)
}

// ArtifactContentHash canonicalizes content and returns the version content hash.
func ArtifactContentHash(kind ArtifactEntityKind, content map[string]any) (string, error) {
	if _, err := SchemaFamilyForKind(kind); err != nil {
		return "", err
	}
	canonical, err := MarshalArtifactCanonicalJSON(content)
	if err != nil {
		return "", fmt.Errorf("marshal canonical content for %s: %w", kind, err)
	}
	return ArtifactContentHashFromCanonicalJSON(canonical), nil
}

func marshalArtifactCanonicalValue(value any) ([]byte, error) {
	switch typed := value.(type) {
	case map[string]any:
		return marshalArtifactCanonicalObject(typed)
	case []any:
		return marshalArtifactCanonicalArray(typed)
	case json.RawMessage:
		decoded, err := decodeArtifactCanonicalJSON(typed)
		if err != nil {
			return nil, err
		}
		return marshalArtifactCanonicalValue(decoded)
	default:
		return json.Marshal(typed)
	}
}

func marshalArtifactCanonicalObject(value map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out bytes.Buffer
	out.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			out.WriteByte(',')
		}
		keyBytes, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("marshal key %q: %w", key, err)
		}
		valueBytes, err := marshalArtifactCanonicalValue(value[key])
		if err != nil {
			return nil, fmt.Errorf("marshal field %q: %w", key, err)
		}
		out.Write(keyBytes)
		out.WriteByte(':')
		out.Write(valueBytes)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func marshalArtifactCanonicalArray(values []any) ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			out.WriteByte(',')
		}
		valueBytes, err := marshalArtifactCanonicalValue(value)
		if err != nil {
			return nil, fmt.Errorf("marshal array index %d: %w", index, err)
		}
		out.Write(valueBytes)
	}
	out.WriteByte(']')
	return out.Bytes(), nil
}

func decodeArtifactCanonicalJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func contentHashFromPayload(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}
