// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { Attachment, ChannelMessage } from "@multica/core/types";
import { resolveVoiceMessagePresentation } from "./voice-message-presentation";

function audioAttachment(): Attachment {
  return {
    id: "019f88be-9292-775f-a550-fc99a15efe9f",
    workspace_id: "workspace-1",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "agent",
    uploader_id: "agent-1",
    filename: "nihao.mp3",
    url: "/uploads/nihao.mp3",
    download_url: "/api/attachments/audio-1/download",
    markdown_url: "/api/attachments/audio-1/download",
    content_type: "audio/mpeg",
    size_bytes: 13_940,
    created_at: "2026-07-22T15:33:00Z",
  };
}

function productionMessage(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  const attachment = audioAttachment();
  return {
    id: "87480f50-5887-4b8f-9655-e20b040b1caf",
    channel_id: "channel-1",
    workspace_id: "workspace-1",
    seq: 1,
    type: "agent",
    author_id: "agent-1",
    author_name: "Wendy",
    content: "你好～",
    parts: [
      { type: "text", text: "你好～" },
      { type: "attachment", attachment_id: attachment.id },
    ],
    attachments: [attachment],
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-22T15:33:00Z",
    ...overrides,
  };
}

describe("resolveVoiceMessagePresentation", () => {
  it("resolves a structured human recording from its voice attachment", () => {
    const attachment = {
      ...audioAttachment(),
      uploader_type: "member" as const,
      uploader_id: "user-1",
      filename: "voice-recording.wav",
      content_type: "audio/wav",
    };
    const presentation = resolveVoiceMessagePresentation(productionMessage({
      type: "user",
      author_id: "user-1",
      parts: [
        { type: "text", text: "你好" },
        {
          type: "voice",
          duration_ms: 1800,
          attachment_id: attachment.id,
          filename: attachment.filename,
          content_type: attachment.content_type,
          size_bytes: attachment.size_bytes,
        },
      ],
      attachments: [attachment],
    }));

    expect(presentation).toEqual({
      voicePart: expect.objectContaining({ attachment_id: attachment.id }),
      recordingAttachment: attachment,
      consumedAttachmentIds: [attachment.id],
      source: "recording",
    });
  });

  it("projects the exact production Agent audio attachment shape as server TTS", () => {
    const presentation = resolveVoiceMessagePresentation(productionMessage());

    expect(presentation).toEqual({
      voicePart: { type: "voice" },
      consumedAttachmentIds: ["019f88be-9292-775f-a550-fc99a15efe9f"],
      source: "legacy-agent-audio",
    });
  });

  it("does not reinterpret a normal user audio attachment", () => {
    expect(resolveVoiceMessagePresentation(productionMessage({ type: "user" }))).toBeNull();
  });

  it("does not reinterpret a mixed Agent file delivery", () => {
    const second = { ...audioAttachment(), id: "document-1", filename: "notes.txt", content_type: "text/plain" };
    expect(resolveVoiceMessagePresentation(productionMessage({
      parts: [
        { type: "text", text: "files" },
        { type: "attachment", attachment_id: audioAttachment().id },
        { type: "attachment", attachment_id: second.id },
      ],
      attachments: [audioAttachment(), second],
    }))).toBeNull();
  });

  it("consumes the old audio attachment after the server adds a voice marker", () => {
    const message = productionMessage();
    expect(resolveVoiceMessagePresentation({
      ...message,
      parts: [...(message.parts ?? []), { type: "voice" }],
    })).toEqual({
      voicePart: { type: "voice" },
      consumedAttachmentIds: ["019f88be-9292-775f-a550-fc99a15efe9f"],
      source: "structured",
    });
  });
});
