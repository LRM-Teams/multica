// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// Server-side atom budgets (spec 8.1: count/length budgets are validated by
// the server, never trusted from a proposer).
const (
	interactionDAGMaxAtomsPerSegment = 8
	interactionDAGMaxAtomBodyRunes   = 512
	interactionDAGMaxSeqsPerAtom     = 16
	// interactionDAGMinProposedAtomRunes gates the deterministic proposer's
	// extractive candidates; server validation itself only enforces non-empty.
	interactionDAGMinProposedAtomRunes = 12
)

// AtomizerSegment carries the event-time Segment facts the server stamps onto
// atoms. Scope and eligibility were frozen when the segment closed; nothing
// here may be re-derived from current Workspace configuration (spec 8.2).
type AtomizerSegment struct {
	SegmentID               string
	StartSeq, EndSeq        int32
	MemoryTypeAtEvent       string
	GraphProjectionEligible bool
	Derivative              bool
	CloseActionKind         string
	ChannelID               string
	ProjectID               string
}

// AtomCandidate is one proposer output. Proposers propose body, kind, and
// source message refs only — identity, scope, and trust are server-stamped.
type AtomCandidate struct {
	Body              string
	Kind              string
	SourceMessageSeqs []int32
}

// AtomProposer is the extraction seam. A production LLM proposer resolves its
// provider/model/region exclusively through the Task 4A policy resolver; the
// default proposer is deterministic and offline.
type AtomProposer interface {
	ProposeAtoms(ctx context.Context, segment AtomizerSegment, payload SanitizedTrajectory) ([]AtomCandidate, error)
}

// GraphMemoryAtomizer validates, stamps, and bounds atoms for one published
// Segment (spec 8.1). Zero atoms is a legal outcome; an extractor failure
// degrades to exactly one content-free fallback atom.
type GraphMemoryAtomizer struct {
	proposer AtomProposer
}

// NewGraphMemoryAtomizer builds an atomizer. A nil proposer selects the
// deterministic extractive proposer.
func NewGraphMemoryAtomizer(proposer AtomProposer) *GraphMemoryAtomizer {
	if proposer == nil {
		proposer = deterministicAtomProposer{}
	}
	return &GraphMemoryAtomizer{proposer: proposer}
}

// ExtractAtoms turns the sanitized payload of one segment into validated,
// server-stamped atoms. It never returns partially stamped atoms and never
// errors on extraction failure — that path degrades to a fallback atom.
func (a *GraphMemoryAtomizer) ExtractAtoms(
	ctx context.Context, segment AtomizerSegment, payload SanitizedTrajectory,
) ([]memorygraph.Atom, error) {
	if a == nil {
		return nil, nil
	}
	if !segment.GraphProjectionEligible ||
		segment.MemoryTypeAtEvent != "graph" ||
		segment.Derivative ||
		segment.CloseActionKind == string(DAGCloseMetadataOnly) ||
		(segment.ChannelID == "" && segment.ProjectID == "") ||
		len(payload.Messages) == 0 {
		// Event-time eligibility is frozen; nothing here retries or replays
		// on later Workspace changes (spec 8.2, AC 14).
		return nil, nil
	}
	visibility, channelID, projectID := "channel", segment.ChannelID, ""
	if segment.ChannelID == "" {
		visibility, channelID, projectID = "project", "", segment.ProjectID
	}
	bySeq := make(map[int32]SanitizedTaskMessage, len(payload.Messages))
	for _, message := range payload.Messages {
		bySeq[message.Sequence] = message
	}

	candidates, err := a.proposer.ProposeAtoms(ctx, segment, payload)
	if err != nil {
		// Extractor failure degrades to one explicit fallback atom that
		// claims no structured coverage (spec 8.1).
		return []memorygraph.Atom{a.fallbackAtom(segment, payload, visibility, channelID, projectID)}, nil
	}

	atoms := make([]memorygraph.Atom, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if len(atoms) >= interactionDAGMaxAtomsPerSegment {
			break
		}
		if atom, ok := a.validateCandidate(segment, candidate, bySeq); ok && !seen[atom.AtomID] {
			seen[atom.AtomID] = true
			atom.Visibility = visibility
			atom.ChannelID = channelID
			atom.ProjectID = projectID
			atoms = append(atoms, atom)
		}
	}
	return atoms, nil
}

// validateCandidate enforces the strict contract on one proposal: closed kind
// (fallback is server-only), non-empty bounded body, and source refs that
// exist inside the segment range (AC 11). It stamps identity, tool trust,
// content hash, and artifact ref from the cited sanitized messages.
func (a *GraphMemoryAtomizer) validateCandidate(
	segment AtomizerSegment, candidate AtomCandidate, bySeq map[int32]SanitizedTaskMessage,
) (memorygraph.Atom, bool) {
	body := memorygraph.NormalizeAtomBody(candidate.Body)
	if body == "" ||
		candidate.Kind == "fallback" ||
		!memorygraph.ValidAtomKind(candidate.Kind) ||
		len(candidate.SourceMessageSeqs) == 0 ||
		len(candidate.SourceMessageSeqs) > interactionDAGMaxSeqsPerAtom {
		return memorygraph.Atom{}, false
	}
	seqs := dedupeSortedSeqs(candidate.SourceMessageSeqs)
	cited := make([]SanitizedTaskMessage, 0, len(seqs))
	for _, seq := range seqs {
		if seq < segment.StartSeq || seq > segment.EndSeq {
			return memorygraph.Atom{}, false
		}
		message, ok := bySeq[seq]
		if !ok {
			return memorygraph.Atom{}, false
		}
		cited = append(cited, message)
	}
	if len([]rune(body)) > interactionDAGMaxAtomBodyRunes {
		body = string([]rune(body)[:interactionDAGMaxAtomBodyRunes])
	}

	sourceTool, trust, contentHash, artifactRef := stampCitations(cited)
	return memorygraph.Atom{
		AtomID:            memorygraph.StableAtomID(segment.SegmentID, candidate.Kind, body, seqs, sourceTool),
		SegmentID:         segment.SegmentID,
		Body:              body,
		Kind:              candidate.Kind,
		SourceMessageSeqs: seqs,
		SourceTool:        sourceTool,
		ToolTrustClass:    string(trust),
		ContentHash:       contentHash,
		ArtifactRef:       artifactRef,
	}, true
}

// fallbackAtom is the explicit degradation for extractor failure: content-free
// body, no source refs, no tool citation, no coverage claim.
func (a *GraphMemoryAtomizer) fallbackAtom(
	segment AtomizerSegment, payload SanitizedTrajectory, visibility, channelID, projectID string,
) memorygraph.Atom {
	return memorygraph.Atom{
		AtomID:         memorygraph.StableAtomID(segment.SegmentID, "fallback", "memory-extraction-fallback", nil, ""),
		SegmentID:      segment.SegmentID,
		Body:           "Memory extraction fallback: this segment published readable content that could not be structurally extracted.",
		Kind:           "fallback",
		ToolTrustClass: string(memorygraph.AtomTrustNone),
		ContentHash:    payload.ContentHash,
		Visibility:     visibility,
		ChannelID:      channelID,
		ProjectID:      projectID,
	}
}

// stampCitations derives the server-owned provenance fields from the cited
// sanitized messages: a single shared tool, the weakest trust among mixed
// citations, the content hash over the cited sanitized bodies, and the first
// externalized artifact ref.
func stampCitations(cited []SanitizedTaskMessage) (string, memorygraph.AtomTrustClass, string, string) {
	sharedTool := ""
	mixedTools := false
	trust := memorygraph.AtomTrustNone
	var content strings.Builder
	artifactRef := ""
	for _, message := range cited {
		if message.Tool != "" {
			if sharedTool == "" && !mixedTools {
				sharedTool = message.Tool
			} else if sharedTool != message.Tool {
				// Divergent citations stamp no single source tool.
				mixedTools = true
				sharedTool = ""
			}
		}
		trust = memorygraph.WeakestToolTrust(trust, memorygraph.ClassifyToolTrust(message.Tool))
		content.WriteString(message.Content)
		content.WriteString(message.Output)
		if artifactRef == "" {
			for _, field := range []string{message.Content, message.Input, message.Output} {
				if ref, ok := artifactRefOf(field); ok {
					artifactRef = ref
					break
				}
			}
		}
	}
	sum := sha256.Sum256([]byte(content.String()))
	return sharedTool, trust, "sha256:" + hex.EncodeToString(sum[:]), artifactRef
}

func dedupeSortedSeqs(seqs []int32) []int32 {
	sorted := append([]int32(nil), seqs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	out := sorted[:0]
	for i, seq := range sorted {
		if i == 0 || seq != sorted[i-1] {
			out = append(out, seq)
		}
	}
	return out
}

// deterministicAtomProposer is the offline default: extractive statement
// candidates from the sanitized payload, in message order.
type deterministicAtomProposer struct{}

func (deterministicAtomProposer) ProposeAtoms(_ context.Context, _ AtomizerSegment, payload SanitizedTrajectory) ([]AtomCandidate, error) {
	candidates := make([]AtomCandidate, 0, len(payload.Messages))
	for _, message := range payload.Messages {
		if message.Type != "user" && message.Type != "assistant" {
			continue
		}
		text := message.Content
		if strings.TrimSpace(text) == "" {
			text = message.Output
		}
		for _, sentence := range splitSentences(text) {
			normalized := memorygraph.NormalizeAtomBody(sentence)
			if len([]rune(normalized)) < interactionDAGMinProposedAtomRunes {
				continue
			}
			candidates = append(candidates, AtomCandidate{
				Body: normalized, Kind: "fact", SourceMessageSeqs: []int32{message.Sequence},
			})
		}
	}
	return candidates, nil
}

// splitSentences splits text into bounded statement fragments.
func splitSentences(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	fragments := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n' || r == ';'
	})
	out := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		if strings.TrimSpace(fragment) != "" {
			out = append(out, fragment)
		}
	}
	return out
}
