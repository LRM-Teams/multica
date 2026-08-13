package messageparts

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestNormalizeTreatsStickerTokenAsPlainText(t *testing.T) {
	content, parts, err := Normalize(" :sticker:hi: ", nil)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if content != ":sticker:hi:" {
		t.Fatalf("content = %q, want trimmed plain text", content)
	}
	if len(parts) != 0 {
		t.Fatalf("parts = %+v, want no structured parts without parts input", parts)
	}
}

func TestNormalizeTreatsEditorBreakPlaceholderAsEmpty(t *testing.T) {
	for _, input := range []string{"<br/>", " <br> ", "<br />&nbsp;<br/>"} {
		content, parts, err := Normalize(input, nil)
		if err != nil {
			t.Fatalf("Normalize(%q) returned error: %v", input, err)
		}
		if content != "" || len(parts) != 0 {
			t.Fatalf("Normalize(%q) = %q %+v, want empty", input, content, parts)
		}
	}
}

func TestNormalizeKeepsInlineBreakText(t *testing.T) {
	content, parts, err := Normalize("hello<br/>world", nil)
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if content != "hello<br/>world" || len(parts) != 0 {
		t.Fatalf("Normalize = %q %+v, want original content", content, parts)
	}
}

func TestNormalizeBuildsFallbackContentFromParts(t *testing.T) {
	content, parts, err := Normalize("", []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "hello"},
		{Type: protocol.MessagePartTypeSticker, StickerID: "hi"},
	})
	if err != nil {
		t.Fatalf("Normalize returned error: %v", err)
	}
	if len(parts) != 2 || parts[1].PackID != BuiltinStickerPackID {
		t.Fatalf("parts = %+v, want normalized builtin sticker", parts)
	}
	if content != "hello "+parts[1].Alt {
		t.Fatalf("content = %q, want text plus sticker alt fallback %q", content, "hello "+parts[1].Alt)
	}
}

func TestNormalizeRejectsUnknownSticker(t *testing.T) {
	_, _, err := Normalize("", []protocol.MessagePart{{Type: protocol.MessagePartTypeSticker, StickerID: "does-not-exist"}})
	if err == nil {
		t.Fatal("Normalize accepted an unknown sticker")
	}
}

func TestNormalizeAttachmentPart(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	content, parts, err := Normalize("", []protocol.MessagePart{{
		Type:         protocol.MessagePartTypeAttachment,
		AttachmentID: id,
		Filename:     "shot.png",
		Text:         "should-clear",
		StickerID:    "should-clear",
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "" {
		t.Fatalf("content = %q, want empty for attachment-only (no markdown URL)", content)
	}
	if len(parts) != 1 {
		t.Fatalf("parts len = %d, want 1", len(parts))
	}
	p := parts[0]
	if p.Type != protocol.MessagePartTypeAttachment || p.AttachmentID != id {
		t.Fatalf("parts[0] = %+v, want attachment %s", p, id)
	}
	if p.Filename != "shot.png" {
		t.Fatalf("filename = %q, want shot.png", p.Filename)
	}
	if p.Text != "" || p.StickerID != "" || p.PackID != "" || p.Alt != "" {
		t.Fatalf("attachment part retained text/sticker fields: %+v", p)
	}
}

func TestNormalizeAttachmentRequiresID(t *testing.T) {
	_, _, err := Normalize("", []protocol.MessagePart{{Type: protocol.MessagePartTypeAttachment}})
	if err == nil {
		t.Fatal("expected error for missing attachment_id")
	}
}

func TestNormalizeVoicePart(t *testing.T) {
	content, parts, err := Normalize("spoken question", []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "spoken question"},
		{
			Type:                protocol.MessagePartTypeVoice,
			DurationMS:          1234,
			Text:                "must clear",
			TranscriptionStatus: protocol.VoiceTranscriptionFailed,
			SynthesisStatus:     protocol.VoiceSynthesisCompleted,
			AttachmentID:        "11111111-1111-1111-1111-111111111111",
			Filename:            " recording.wav ",
			ContentType:         " audio/wav ",
			SizeBytes:           48,
		},
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "spoken question" || len(parts) != 2 {
		t.Fatalf("Normalize = %q %+v, want transcript and two parts", content, parts)
	}
	voice := parts[1]
	if voice.Type != protocol.MessagePartTypeVoice || voice.DurationMS != 1234 {
		t.Fatalf("voice part = %+v", voice)
	}
	if voice.Text != "" || voice.AttachmentID != "11111111-1111-1111-1111-111111111111" ||
		voice.Filename != "recording.wav" || voice.ContentType != "audio/wav" || voice.SizeBytes != 48 {
		t.Fatalf("voice part did not preserve recording metadata: %+v", voice)
	}
	if voice.TranscriptionStatus != protocol.VoiceTranscriptionCompleted || voice.SynthesisStatus != "" {
		t.Fatalf("voice part retained caller-owned lifecycle state: %+v", voice)
	}
	if voice.TranscriptionStatus != protocol.VoiceTranscriptionCompleted {
		t.Fatalf("voice transcription status = %q, want completed", voice.TranscriptionStatus)
	}
}

func TestNormalizeAgentVoicePartWithoutAttachment(t *testing.T) {
	_, parts, err := Normalize("spoken answer", []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "spoken answer"},
		{Type: protocol.MessagePartTypeVoice},
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(parts) != 2 || parts[1].AttachmentID != "" {
		t.Fatalf("parts = %+v, want attachment-free Agent voice", parts)
	}
}

func TestNormalizeVoicePartRejectsDurationAboveRecordingLimit(t *testing.T) {
	_, _, err := Normalize("spoken question", []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "spoken question"},
		{Type: protocol.MessagePartTypeVoice, DurationMS: 60_001},
	})
	if err == nil || !strings.Contains(err.Error(), "duration_ms") {
		t.Fatalf("error = %v, want duration_ms validation", err)
	}
}

func TestNormalizeRecordedVoiceWithoutTranscriptQueuesServerTranscription(t *testing.T) {
	content, parts, err := Normalize("", []protocol.MessagePart{{
		Type:                protocol.MessagePartTypeVoice,
		AttachmentID:        "11111111-1111-1111-1111-111111111111",
		DurationMS:          1234,
		TranscriptionStatus: protocol.VoiceTranscriptionFailed,
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "" || len(parts) != 1 {
		t.Fatalf("Normalize = %q %+v, want one pending recorded voice", content, parts)
	}
	if parts[0].TranscriptionStatus != protocol.VoiceTranscriptionPending {
		t.Fatalf("transcription status = %q, want pending", parts[0].TranscriptionStatus)
	}
}

func TestNormalizeVoiceWithoutTranscriptRejectsMissingAttachment(t *testing.T) {
	_, _, err := Normalize("", []protocol.MessagePart{{Type: protocol.MessagePartTypeVoice}})
	if err == nil || !strings.Contains(err.Error(), "recorded voice attachment") {
		t.Fatalf("error = %v, want recorded attachment validation", err)
	}
}

func TestNormalizeVoiceWithoutTextPartRejectsAmbiguousTopLevelContent(t *testing.T) {
	_, _, err := Normalize("caller claims this is a transcript", []protocol.MessagePart{{
		Type:         protocol.MessagePartTypeVoice,
		AttachmentID: "11111111-1111-1111-1111-111111111111",
	}})
	if err == nil || !strings.Contains(err.Error(), "transcript text part") {
		t.Fatalf("error = %v, want text-part requirement for supplied transcript", err)
	}
}

func TestNormalizeVoicePartRejectsTranscriptAboveTTSLimit(t *testing.T) {
	transcript := strings.Repeat("声", protocol.VoiceTranscriptMaxRunes+1)
	_, _, err := Normalize(transcript, []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: transcript},
		{Type: protocol.MessagePartTypeVoice},
	})
	if err == nil || !strings.Contains(err.Error(), "voice transcript") {
		t.Fatalf("error = %v, want voice transcript limit", err)
	}
}

func TestNormalizeTextPlusAttachments(t *testing.T) {
	a := "11111111-1111-1111-1111-111111111111"
	b := "22222222-2222-2222-2222-222222222222"
	content, parts, err := Normalize("", []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "  check s146  "},
		{Type: protocol.MessagePartTypeAttachment, AttachmentID: a},
		{Type: protocol.MessagePartTypeAttachment, AttachmentID: b},
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "check s146" {
		t.Fatalf("content = %q, want text only", content)
	}
	if len(parts) != 3 {
		t.Fatalf("parts len = %d, want 3", len(parts))
	}
	if parts[1].AttachmentID != a || parts[2].AttachmentID != b {
		t.Fatalf("attachment order = %+v", parts)
	}
}

func TestNormalizeReferenceParts(t *testing.T) {
	content, parts, err := Normalize("refs", []protocol.MessagePart{
		{
			Type:       protocol.MessagePartTypeReference,
			RefType:    "mention",
			RefSubType: "agent",
			RefID:      "11111111-1111-1111-1111-111111111111",
			Label:      "Backend",
			Text:       "should-clear",
			StickerID:  "should-clear",
		},
		{
			Type:         protocol.MessagePartTypeReference,
			RefType:      "issue-ref",
			RefID:        "MUL-123",
			Label:        "MUL-123",
			AttachmentID: "should-clear",
		},
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "refs" {
		t.Fatalf("content = %q, want refs", content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts len = %d, want 2", len(parts))
	}
	if parts[0].RefType != "mention" || parts[0].RefSubType != "agent" || parts[0].RefID == "" {
		t.Fatalf("mention reference = %+v", parts[0])
	}
	if parts[0].Text != "" || parts[0].StickerID != "" {
		t.Fatalf("mention reference retained non-reference fields: %+v", parts[0])
	}
	if parts[1].RefType != "issue-ref" || parts[1].RefSubType != "issue" || parts[1].RefID != "MUL-123" {
		t.Fatalf("issue reference = %+v, want default issue subtype", parts[1])
	}
	if parts[1].AttachmentID != "" {
		t.Fatalf("issue reference retained attachment fields: %+v", parts[1])
	}
}

func TestNormalizeReferencePartsRejectsAllMention(t *testing.T) {
	_, _, err := Normalize("@all", []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "all",
		RefID:      "all",
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported mention ref_subtype") {
		t.Fatalf("Normalize @all error = %v, want unsupported mention subtype", err)
	}
}

// TestNormalizeChannelReferencePart is task #912's unit coverage for
// normalizePart's channel-ref case — the sidecar the client may submit
// alongside a [Label](mention://channel/<id>) link (always without a source
// span; channel_structured_mentions.go's resolver re-derives the verified
// one). See TestChannelReferenceLinksBecomeStructuredMessageParts for the
// end-to-end resolution/anchoring behavior.
func TestNormalizeChannelReferencePart(t *testing.T) {
	content, parts, err := Normalize("see #general", []protocol.MessagePart{{
		Type:              protocol.MessagePartTypeReference,
		RefType:           "channel-ref",
		RefID:             "11111111-1111-1111-1111-111111111111",
		Label:             "general",
		ContentStartUTF16: intPtr(4),
		ContentEndUTF16:   intPtr(12),
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "see #general" {
		t.Fatalf("content = %q, want unchanged", content)
	}
	if len(parts) != 1 {
		t.Fatalf("parts len = %d, want 1", len(parts))
	}
	if parts[0].RefType != "channel-ref" || parts[0].RefSubType != "" || parts[0].RefID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("channel reference = %+v, want no ref_subtype", parts[0])
	}
	// Client-supplied source ranges are never trusted for a Reference part —
	// see normalizePart's comment. A caller-chosen span could point anywhere
	// in the visible content; only channel_structured_mentions.go's resolver
	// (which re-derives it from the actual matched link) may set it.
	if parts[0].ContentStartUTF16 != nil || parts[0].ContentEndUTF16 != nil {
		t.Fatalf("channel reference retained caller-supplied span: %+v", parts[0])
	}
}

func TestNormalizeReferencePartsRejectsChannelRefSubtype(t *testing.T) {
	_, _, err := Normalize("see #general", []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "channel-ref",
		RefSubType: "group",
		RefID:      "11111111-1111-1111-1111-111111111111",
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported channel-ref ref_subtype") {
		t.Fatalf("Normalize channel-ref subtype error = %v, want unsupported channel-ref subtype", err)
	}
}

func TestNormalizeSystemEventPart(t *testing.T) {
	content, parts, err := Normalize("", []protocol.MessagePart{{
		Type:        protocol.MessagePartTypeSystemEvent,
		Event:       "thread_unfollowed",
		EventParams: []byte(`{"actor_id":"agent-1"}`),
		Text:        "should-clear",
		RefID:       "should-clear",
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "" || len(parts) != 1 {
		t.Fatalf("Normalize = %q %+v, want one event part and empty fallback content", content, parts)
	}
	part := parts[0]
	if part.Type != protocol.MessagePartTypeSystemEvent || part.Event != "thread_unfollowed" || string(part.EventParams) != `{"actor_id":"agent-1"}` {
		t.Fatalf("event part = %+v", part)
	}
	if part.Text != "" || part.RefID != "" {
		t.Fatalf("event part retained non-event fields: %+v", part)
	}
}

func TestNormalizeSystemEventPartRejectsMalformedParams(t *testing.T) {
	_, _, err := Normalize("", []protocol.MessagePart{{
		Type:        protocol.MessagePartTypeSystemEvent,
		Event:       "thread_unfollowed",
		EventParams: []byte(`[]`),
	}})
	if err == nil {
		t.Fatal("Normalize accepted a non-object event_params value")
	}
}

func TestUnwrapStructuredMessageSendTextParts(t *testing.T) {
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(
		`{"action":"message_send","output":"Hello","parts":[{"type":"text","text":"Hello"}]}`,
		nil,
	)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if !unwrapped {
		t.Fatal("expected structured message_send payload to unwrap")
	}
	if content != "Hello" {
		t.Fatalf("content = %q, want Hello", content)
	}
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeText || parts[0].Text != "Hello" {
		t.Fatalf("parts = %+v, want one normalized text part", parts)
	}
}

func TestUnwrapStructuredMessageSendStickerOnly(t *testing.T) {
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(
		`{"action":"message_send","parts":[{"type":"sticker","sticker_id":"hi"}]}`,
		nil,
	)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if !unwrapped {
		t.Fatal("expected sticker-only message_send payload to unwrap")
	}
	if content == "" {
		t.Fatal("sticker-only payload must receive accessible fallback content")
	}
	if len(parts) != 1 ||
		parts[0].Type != protocol.MessagePartTypeSticker ||
		parts[0].PackID != BuiltinStickerPackID ||
		parts[0].StickerID != "hi" ||
		parts[0].Alt == "" {
		t.Fatalf("parts = %+v, want one normalized builtin hi sticker", parts)
	}
}

func TestUnwrapStructuredMessageSendEmbeddedTextPrefixedEnvelope(t *testing.T) {
	raw := `Repo is not checked out this turn either - consistent with prior attempts. {"action":"message_send","output":"Visible reply","parts":[{"type":"text","text":"Visible reply"}]}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if !unwrapped {
		t.Fatal("expected embedded structured message_send payload to unwrap")
	}
	if content != "Visible reply" {
		t.Fatalf("content = %q, want Visible reply", content)
	}
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeText || parts[0].Text != "Visible reply" {
		t.Fatalf("parts = %+v, want one normalized text part", parts)
	}
}

func TestUnwrapStructuredMessageSendEmbeddedEnvelopeHandlesBracesInStrings(t *testing.T) {
	raw := `prefix {"action":"message_send","output":"Visible {brace} reply","parts":[{"type":"text","text":"Visible {brace} reply"}]} suffix {"parts":["left alone"]}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if !unwrapped {
		t.Fatal("expected embedded structured message_send payload to unwrap")
	}
	if content != "Visible {brace} reply" {
		t.Fatalf("content = %q, want visible reply with braces", content)
	}
	if len(parts) != 1 || parts[0].Text != "Visible {brace} reply" {
		t.Fatalf("parts = %+v, want one normalized text part", parts)
	}
}

func TestUnwrapStructuredMessageSendLeavesPlainJSONAlone(t *testing.T) {
	raw := `{"output":"Hello"}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if unwrapped {
		t.Fatal("plain JSON without action/type/parts must not unwrap")
	}
	if content != raw || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged raw JSON", content, parts)
	}
}

func TestUnwrapStructuredMessageSendLeavesEmbeddedJSONWithoutActionAlone(t *testing.T) {
	raw := `Here is sample JSON: {"parts":["a","b"]}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if unwrapped {
		t.Fatal("embedded JSON without action must not unwrap")
	}
	if content != raw || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged input", content, parts)
	}
}

func TestUnwrapStructuredMessageSendLeavesEmbeddedMessageSendWithoutPartsAlone(t *testing.T) {
	raw := `Here is a JSON snippet: {"action":"message_send","output":"plain text"}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if unwrapped {
		t.Fatal("embedded message_send without parts must not unwrap")
	}
	if content != raw || len(parts) != 0 {
		t.Fatalf("content=%q parts=%+v, want unchanged input", content, parts)
	}
}

func TestUnwrapStructuredMessageSendLeavesExistingPartsAlone(t *testing.T) {
	existing := []protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "Already normalized"}}
	raw := `{"action":"message_send","output":"Hidden"}`
	content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, existing)
	if err != nil {
		t.Fatalf("UnwrapStructuredMessageSend returned error: %v", err)
	}
	if unwrapped {
		t.Fatal("content with existing structured parts must not unwrap again")
	}
	if content != raw || len(parts) != 1 || parts[0].Text != "Already normalized" {
		t.Fatalf("content=%q parts=%+v, want unchanged input", content, parts)
	}
}

func intPtr(v int) *int { return &v }

func TestNormalizeAgentCreateProposalReference(t *testing.T) {
	seed := "Hiree Bot"
	_, parts, err := Normalize("hire", []protocol.MessagePart{{
		Type:    protocol.MessagePartTypeReference,
		RefType: "agent:create",
		RefID:   seed,
		Label:   "Hiree Bot",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0].RefType != "agent:create" || parts[0].RefSubType != "" || parts[0].RefID != seed {
		t.Fatalf("parts=%+v", parts)
	}
	// A legacy action-card sidecar and ref subtypes are rejected: only the
	// canonical Message-backed Proposal survives the hard cut.
	if _, _, err := Normalize("", []protocol.MessagePart{{
		Type:    protocol.MessagePartTypeReference,
		RefType: "action_card",
		RefID:   "legacy-id",
	}}); err == nil {
		t.Fatal("expected legacy action card rejection")
	}
	if _, _, err := Normalize("", []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "agent:create",
		RefSubType: "legacy",
		RefID:      seed,
	}}); err == nil {
		t.Fatal("expected agent:create ref_subtype rejection")
	}
}

func TestNormalizeNoteBriefPart(t *testing.T) {
	content, parts, err := Normalize("run this brief", []protocol.MessagePart{{
		Type:  protocol.MessagePartTypeNoteBrief,
		RefID: "11111111-1111-1111-1111-111111111111",
		Label: " Weekly plan ",
		Text:  "Ship the bridge",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if content != "run this brief" {
		t.Fatalf("content = %q", content)
	}
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeNoteBrief {
		t.Fatalf("parts=%+v", parts)
	}
	if parts[0].RefID != "11111111-1111-1111-1111-111111111111" || parts[0].Label != "Weekly plan" || parts[0].Text != "Ship the bridge" {
		t.Fatalf("note_brief fields = %+v", parts[0])
	}
	if _, _, err := Normalize("x", []protocol.MessagePart{{
		Type: protocol.MessagePartTypeNoteBrief,
		Text: "missing page id",
	}}); err == nil {
		t.Fatal("expected missing ref_id rejection")
	}
}

func TestUnwrapStructuredMessageSendLeavesNoteAIEditJSONAlone(t *testing.T) {
	for _, raw := range []string{
		`{"action":"insert","markdown":"hi","target":null,"title":null,"rationale":"x"}`,
		`{"action":"replace_page","markdown":"# Title\n\nBody","target":null}`,
		`{"action":"patch","markdown":"new","target":"old"}`,
		`{"action":"replace_selection","markdown":"improved"}`,
	} {
		content, parts, unwrapped, err := UnwrapStructuredMessageSend(raw, nil)
		if err != nil {
			t.Fatalf("UnwrapStructuredMessageSend(%s) error: %v", raw, err)
		}
		if unwrapped {
			t.Fatalf("note AI edit JSON must not unwrap: %s", raw)
		}
		if content != raw || len(parts) != 0 {
			t.Fatalf("content=%q parts=%+v, want unchanged note AI JSON", content, parts)
		}
	}
}
