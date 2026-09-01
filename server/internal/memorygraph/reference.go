// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// memoryRefIDLimit bounds every identity field of a ref. Real ids are node
// ids, atom ids ("atom-" + 24 hex) and segment ids; anything longer is a
// forged or corrupted ref.
const memoryRefIDLimit = 256

// ValidateMemoryRef enforces the strict ref contract (Task 10 Step 2):
// known kind, the kind's own identity field present, the other kind's field
// absent, no control characters, bounded length. Ids may freely contain
// colons, slashes and unicode — the kind travels in the key prefix, never
// in a colon split. A ref that fails validation never reaches a resolver.
func ValidateMemoryRef(m MemoryRef) error {
	switch m.Kind {
	case MemoryRefGraphNode:
		if err := validRefID("node_id", m.NodeID); err != nil {
			return err
		}
		if m.AtomID != "" {
			return fmt.Errorf("memory ref: graph_node must not carry an atom id")
		}
	case MemoryRefStagingAtom:
		if err := validRefID("atom_id", m.AtomID); err != nil {
			return err
		}
		if m.NodeID != "" {
			return fmt.Errorf("memory ref: staging_atom must not carry a node id")
		}
		if m.SegmentID != "" {
			if err := validRefID("segment_id", m.SegmentID); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("memory ref: unknown kind %q", m.Kind)
	}
	if m.ChannelID != "" {
		if err := validRefID("channel_id", m.ChannelID); err != nil {
			return err
		}
	}
	return nil
}

// validRefID rejects empty, control-character-bearing, non-UTF8 or oversize
// identity fields.
func validRefID(name, id string) error {
	if id == "" {
		return fmt.Errorf("memory ref: %s is required", name)
	}
	if len(id) > memoryRefIDLimit {
		return fmt.Errorf("memory ref: %s exceeds %d bytes", name, memoryRefIDLimit)
	}
	if !utf8.ValidString(id) {
		return fmt.Errorf("memory ref: %s is not valid UTF-8", name)
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("memory ref: %s contains control characters", name)
		}
	}
	return nil
}

// ParseMemoryRefKey parses a canonical "kind:id" key back into a ref. The
// kind is matched by prefix — the id itself may contain colons and even the
// other kind's prefix text.
func ParseMemoryRefKey(key string) (MemoryRef, error) {
	for _, kind := range []MemoryRefKind{MemoryRefGraphNode, MemoryRefStagingAtom} {
		prefix := string(kind) + ":"
		if strings.HasPrefix(key, prefix) {
			id := key[len(prefix):]
			ref := MemoryRef{Kind: kind}
			if kind == MemoryRefGraphNode {
				ref.NodeID = id
			} else {
				ref.AtomID = id
			}
			if err := ValidateMemoryRef(ref); err != nil {
				return MemoryRef{}, err
			}
			return ref, nil
		}
	}
	return MemoryRef{}, fmt.Errorf("memory ref: malformed key %q", truncateRefKey(key))
}

func truncateRefKey(key string) string {
	if len(key) > 64 {
		return key[:64] + "…"
	}
	return key
}
