import { describe, expect, it } from "vitest";
import { buildChannelMessageParts, type MessagePart } from "./message-part";

describe("buildChannelMessageParts", () => {
  it("builds text + attachment parts in order and skips empty ids", () => {
    expect(
      buildChannelMessageParts("hello", ["att-1", "", "att-2"]),
    ).toEqual([
      { type: "text", text: "hello" },
      { type: "attachment", attachment_id: "att-1" },
      { type: "attachment", attachment_id: "att-2" },
    ]);
  });

  it("returns attachment-only parts when text is empty/whitespace", () => {
    expect(buildChannelMessageParts("  \n", ["att-1"])).toEqual([
      { type: "attachment", attachment_id: "att-1" },
    ]);
  });

  it("returns text-only parts when no attachment ids are provided", () => {
    expect(buildChannelMessageParts(" just text ")).toEqual([
      { type: "text", text: "just text" },
    ]);
    expect(buildChannelMessageParts("", [])).toEqual([]);
    expect(buildChannelMessageParts("")).toEqual([]);
  });
});

describe("MessagePart voice lifecycle contract", () => {
  it("represents server-owned Agent synthesis states", () => {
    const parts = [
      { type: "voice", synthesis_status: "pending" },
      {
        type: "voice",
        synthesis_status: "completed",
        attachment_id: "tts-audio-1",
        content_type: "audio/wav",
        duration_ms: 1250,
      },
      { type: "voice", synthesis_status: "failed" },
    ] satisfies MessagePart[];

    expect(parts.map((part) => part.synthesis_status)).toEqual([
      "pending",
      "completed",
      "failed",
    ]);
  });
});

describe("MessagePart choice contract", () => {
  it("represents binary and list choice cards plus reply lock", () => {
    const binary = {
      type: "choice",
      choice_id: "c1",
      prompt: "开 PR？",
      layout: "binary",
      options: [
        { id: "yes", label: "是" },
        { id: "no", label: "否" },
      ],
    } satisfies MessagePart;
    const reply = {
      type: "choice_reply",
      choice_id: "c1",
      option_id: "yes",
      label: "是",
    } satisfies MessagePart;
    expect(binary.layout).toBe("binary");
    expect(reply.option_id).toBe("yes");
  });
});

describe("MessagePart note_write contract", () => {
  it("allows a create proposal without ref_id and a targeted write with one", () => {
    const create: Extract<MessagePart, { type: "note_write" }> = { type: "note_write" };
    const existing: Extract<MessagePart, { type: "note_write" }> = {
      type: "note_write",
      ref_id: "11111111-1111-1111-1111-111111111111",
      label: "Weekly plan",
    };
    expect(create.ref_id).toBeUndefined();
    expect(existing.ref_id).toBe("11111111-1111-1111-1111-111111111111");
  });
});
