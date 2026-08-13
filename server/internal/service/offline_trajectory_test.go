package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOfflineCall_PreservesSystemMessagesToolsGenerationAndTypedOutput(t *testing.T) {
	rawRequest := []byte(`{
		"provider":"synthetic-provider",
		"model":"synthetic-model",
		"system":[{"type":"text","text":"system instruction"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"question"}]},
			{"role":"assistant","content":[{"type":"toolCall","id":"tool-1","name":"lookup","arguments":{"q":"value"}}]},
			{"role":"tool","content":[{"type":"toolResult","tool_call_id":"tool-1","content":[{"type":"text","text":"result"}]}]}
		],
		"tools":[{"name":"lookup","description":"Lookup fixture","input_schema":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}}],
		"temperature":0.7,
		"max_tokens":128
	}`)
	final := []byte(`{"role":"assistant","blocks":[{"type":"thinking","thinking":"reasoning"},{"type":"text","text":"answer"},{"type":"toolCall","id":"tool-2","name":"act","arguments":{}}]}`)
	src := OfflineCallSource{
		CallID: "C1", TrainingMode: "offline_rl",
		Provider: "synthetic-provider", Model: "synthetic-model", APIKind: "messages",
		RawProviderRequest: rawRequest, FinalAssistantMessage: final,
		Status: "completed", StopReason: "toolUse", ResponseComplete: true, TrainingEligible: true,
		RequestHash: offlinePayloadHash(rawRequest), ResponseHash: offlinePayloadHash(final),
	}

	line := NormalizeOfflineCall(src)
	require.Equal(t, "trajectory", line.Status)
	require.NotNil(t, line.Trajectory)
	assert.Equal(t, OfflineNormalizationVersion, line.Trajectory.NormalizationVersion)
	require.NotNil(t, line.Trajectory.System)
	assert.Equal(t, "text", line.Trajectory.System.Blocks[0].Type)
	assert.Equal(t, "system instruction", line.Trajectory.System.Blocks[0].Text)
	require.Len(t, line.Trajectory.Messages, 3)
	assert.Equal(t, "toolResult", line.Trajectory.Messages[2].Blocks[0].Type)
	assert.Equal(t, "tool-1", line.Trajectory.Messages[2].Blocks[0].ToolCallID)
	assert.Equal(t, "result", line.Trajectory.Messages[2].Blocks[0].Content[0].Text)
	require.Len(t, line.Trajectory.Tools, 1)
	assert.Equal(t, "lookup", line.Trajectory.Tools[0].Name)
	assert.Equal(t, 0.7, line.Trajectory.GenerationConfig["temperature"])
	assert.EqualValues(t, 128, line.Trajectory.GenerationConfig["max_output_tokens"])
	assert.Equal(t, []string{"thinking", "text", "toolCall"}, []string{
		line.Trajectory.Output.Blocks[0].Type,
		line.Trajectory.Output.Blocks[1].Type,
		line.Trajectory.Output.Blocks[2].Type,
	})
	assert.Equal(t, "reasoning", line.Trajectory.Output.Blocks[0].Thinking)
	assert.Equal(t, src.RequestHash, line.Trajectory.RequestHash)
	assert.Equal(t, src.ResponseHash, line.Trajectory.ResponseHash)
	assert.Equal(t, "synthetic-provider", line.Trajectory.Provider.Name)
	assert.Equal(t, "messages", line.Trajectory.Provider.APIKind)
}

func TestNormalizeOfflineCall_ExcludesUnsupportedSemantics(t *testing.T) {
	rawRequest := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"syntheticAudioBlock","data":"x"}]}],"tools":[]}`)
	final := []byte(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`)
	src := OfflineCallSource{
		CallID: "C2", TrainingMode: "offline_rl",
		Provider: "synthetic", Model: "m", APIKind: "messages",
		RawProviderRequest: rawRequest, FinalAssistantMessage: final,
		Status: "completed", StopReason: "stop", ResponseComplete: true, TrainingEligible: true,
		RequestHash: offlinePayloadHash(rawRequest), ResponseHash: offlinePayloadHash(final),
	}
	line := NormalizeOfflineCall(src)
	assert.Equal(t, "excluded", line.Status)
	assert.Equal(t, OfflineReasonNormalizationUnsupported, line.Reason)
	assert.Equal(t, "syntheticAudioBlock", line.Details["semantic_type"])
	assert.Equal(t, OfflineNormalizationVersion, line.Details["normalization_version"])
}

func TestNormalizeOfflineCall_ExcludesUnrepresentableBlockFields(t *testing.T) {
	baseRequest := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"q"}]}]}`)
	cases := []struct {
		name         string
		rawRequest   []byte
		final        []byte
		semanticType string
	}{
		{
			name:         "thinking signature is model-significant",
			rawRequest:   baseRequest,
			final:        []byte(`{"role":"assistant","blocks":[{"type":"thinking","thinking":"r","signature":"sig-1"}]}`),
			semanticType: "thinking.signature",
		},
		{
			name:         "text citations cannot round-trip",
			rawRequest:   []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"q","citations":[{"title":"t"}]}]}]}`),
			final:        []byte(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
			semanticType: "text.citations",
		},
		{
			name:         "tool result error flag changes model input",
			rawRequest:   []byte(`{"model":"m","messages":[{"role":"tool","content":[{"type":"toolResult","tool_call_id":"t1","is_error":true,"content":"boom"}]}]}`),
			final:        []byte(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
			semanticType: "toolResult.is_error",
		},
		{
			name:         "tool call extra provider field is lossy",
			rawRequest:   baseRequest,
			final:        []byte(`{"role":"assistant","blocks":[{"type":"toolCall","id":"t1","name":"act","arguments":{},"provider_trace":"xyz"}]}`),
			semanticType: "toolCall.provider_trace",
		},
		{
			name:         "openai message-level tool_calls cannot be dropped",
			rawRequest:   []byte(`{"model":"m","messages":[{"role":"assistant","content":"text","tool_calls":[{"id":"t1"}]}]}`),
			final:        []byte(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
			semanticType: "message.tool_calls",
		},
		{
			name:         "nested function wrapper fields cannot round-trip",
			rawRequest:   baseRequest,
			final:        []byte(`{"role":"assistant","blocks":[{"type":"toolCall","function":{"name":"act","arguments":{},"strict":true}}]}`),
			semanticType: "toolCall.function.strict",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := OfflineCallSource{
				CallID: "C-field", TrainingMode: "offline_rl",
				Provider: "synthetic", Model: "m", APIKind: "messages",
				RawProviderRequest: tc.rawRequest, FinalAssistantMessage: tc.final,
				Status: "completed", StopReason: "stop", ResponseComplete: true, TrainingEligible: true,
				RequestHash: offlinePayloadHash(tc.rawRequest), ResponseHash: offlinePayloadHash(tc.final),
			}
			line := NormalizeOfflineCall(src)
			assert.Equal(t, "excluded", line.Status)
			assert.Equal(t, OfflineReasonNormalizationUnsupported, line.Reason)
			assert.Equal(t, tc.semanticType, line.Details["semantic_type"])
		})
	}
}

func TestNormalizeOfflineCall_DetailedEligibilityAndModeReasons(t *testing.T) {
	base := OfflineCallSource{
		CallID: "Cx", TrainingMode: "offline_rl",
		Provider: "synthetic", Model: "m", APIKind: "messages",
		RawProviderRequest: []byte(`{"messages":[]}`), FinalAssistantMessage: []byte(`{"role":"assistant","blocks":[{"type":"text","text":"x"}]}`),
		Status: "completed", StopReason: "stop", ResponseComplete: true, TrainingEligible: true,
	}
	base.RequestHash = offlinePayloadHash(base.RawProviderRequest)
	base.ResponseHash = offlinePayloadHash(base.FinalAssistantMessage)

	online := base
	online.TrainingMode = "online_rl"
	assert.Equal(t, OfflineReasonWrongModeOnlineRL, NormalizeOfflineCall(online).Reason)

	none := base
	none.TrainingMode = "none"
	assert.Equal(t, OfflineReasonWrongModeNone, NormalizeOfflineCall(none).Reason)

	incomplete := base
	incomplete.ResponseComplete = false
	incomplete.TrainingEligible = false
	assert.Equal(t, OfflineReasonResponseIncomplete, NormalizeOfflineCall(incomplete).Reason)

	length := base
	length.StopReason = "length"
	length.TrainingEligible = false
	assert.Equal(t, OfflineReasonStopReasonLength, NormalizeOfflineCall(length).Reason)

	ineligible := base
	ineligible.TrainingEligible = false
	ineligible.StopReason = "error"
	assert.Equal(t, OfflineReasonTrainingIneligible, NormalizeOfflineCall(ineligible).Reason)

	missingRaw := base
	missingRaw.RawProviderRequest = nil
	assert.Equal(t, OfflineReasonRawPayloadUnavailable, NormalizeOfflineCall(missingRaw).Reason)

	mismatch := base
	mismatch.RequestHash = "sha256:tampered"
	assert.Equal(t, OfflineReasonHashMismatch, NormalizeOfflineCall(mismatch).Reason)
}

func TestResolveOfflineTrajectoryLines_CanonicalMemberOrderAndLexNonMemberSuffix(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"q"}]}],"tools":[]}`)
	final := []byte(`{"role":"assistant","blocks":[{"type":"text","text":"a"}]}`)
	mk := func(id string) OfflineCallSource {
		return OfflineCallSource{
			CallID: id, TrainingMode: "offline_rl",
			Provider: "synthetic", Model: "m", APIKind: "messages",
			RawProviderRequest: raw, FinalAssistantMessage: final,
			Status: "completed", StopReason: "stop", ResponseComplete: true, TrainingEligible: true,
			RequestHash: offlinePayloadHash(raw), ResponseHash: offlinePayloadHash(final),
		}
	}
	lines := ResolveOfflineTrajectoryLines(
		[]OfflineCallSource{mk("C2"), mk("C1"), mk("C3")},
		[]string{"C1", "ZX", "C2", "C1", "AY"},
	)
	require.Len(t, lines, 4)
	assert.Equal(t, []string{"C2", "C1", "AY", "ZX"}, []string{
		lines[0].CallID, lines[1].CallID, lines[2].CallID, lines[3].CallID,
	})
	assert.Equal(t, "trajectory", lines[0].Status)
	assert.Equal(t, "trajectory", lines[1].Status)
	assert.Equal(t, OfflineReasonCallNotInSnapshot, lines[2].Reason)
	assert.Equal(t, OfflineReasonCallNotInSnapshot, lines[3].Reason)
}

func TestResolveOfflineTrajectoryLines_DeterministicReplay(t *testing.T) {
	raw := []byte(`{"system":[{"type":"text","text":"sys"}],"messages":[{"role":"user","content":[{"type":"text","text":"q"}]}],"tools":[],"temperature":0}`)
	final := []byte(`{"role":"assistant","blocks":[{"type":"thinking","thinking":"t"},{"type":"text","text":"a"}]}`)
	src := OfflineCallSource{
		CallID: "call-synthetic-1", TrainingMode: "offline_rl",
		Provider: "synthetic-provider", Model: "synthetic-model", APIKind: "messages",
		RawProviderRequest: raw, FinalAssistantMessage: final,
		Status: "completed", StopReason: "stop", ResponseComplete: true, TrainingEligible: true,
		RequestHash: offlinePayloadHash(raw), ResponseHash: offlinePayloadHash(final),
	}
	first := ResolveOfflineTrajectoryLines([]OfflineCallSource{src}, []string{"call-synthetic-1", "missing", "call-synthetic-1"})
	second := ResolveOfflineTrajectoryLines([]OfflineCallSource{src}, []string{"call-synthetic-1", "missing", "call-synthetic-1"})
	firstJSON, err := json.Marshal(first)
	require.NoError(t, err)
	secondJSON, err := json.Marshal(second)
	require.NoError(t, err)
	assert.JSONEq(t, string(firstJSON), string(secondJSON))
	assert.Equal(t, offlinePayloadHash(raw), first[0].Trajectory.RequestHash)
	assert.Equal(t, OfflineNormalizationVersion, first[0].Trajectory.NormalizationVersion)
}

func TestOfflinePayloadHash_MatchesSha256PrefixFormat(t *testing.T) {
	raw := []byte(`{"ok":true}`)
	sum := sha256.Sum256(raw)
	assert.Equal(t, fmt.Sprintf("sha256:%x", sum[:]), offlinePayloadHash(raw))
}

func TestNormalizeOfflineCall_ContentArrayAssistantOutput(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[]}`)
	final := []byte(`{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","id":"t1","name":"act","input":{"x":1}}],"stopReason":"toolUse"}`)
	src := OfflineCallSource{
		CallID: "C9", TrainingMode: "offline_rl",
		Provider: "synthetic", Model: "m", APIKind: "messages",
		RawProviderRequest: raw, FinalAssistantMessage: final,
		Status: "completed", StopReason: "toolUse", ResponseComplete: true, TrainingEligible: true,
		RequestHash: offlinePayloadHash(raw), ResponseHash: offlinePayloadHash(final),
	}
	line := NormalizeOfflineCall(src)
	require.Equal(t, "trajectory", line.Status)
	require.Len(t, line.Trajectory.Output.Blocks, 2)
	assert.Equal(t, "toolCall", line.Trajectory.Output.Blocks[1].Type)
	assert.Equal(t, "t1", line.Trajectory.Output.Blocks[1].ID)
	assert.JSONEq(t, `{"x":1}`, string(line.Trajectory.Output.Blocks[1].Arguments))
}
