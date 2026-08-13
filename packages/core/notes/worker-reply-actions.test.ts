import { describe, expect, it } from "vitest";
import type { ChannelMessage } from "../types";
import {
  appendWorkerReplyBelowNote,
  buildNoteWorkerPageIdByMessageId,
  deriveNoteWorkerReplyTitle,
  noteWorkerReplyPlainText,
} from "./worker-reply-actions";

describe("deriveNoteWorkerReplyTitle", () => {
  it("uses the first non-empty line and strips a markdown heading", () => {
    expect(deriveNoteWorkerReplyTitle("## Ship checklist\n\n- a\n- b", "Fallback")).toBe(
      "Ship checklist",
    );
  });

  it("falls back when reply is empty", () => {
    expect(deriveNoteWorkerReplyTitle("  \n  ", "智能体回复")).toBe("智能体回复");
  });
});

describe("appendWorkerReplyBelowNote", () => {
  it("inserts a blank line then a titled section", () => {
    expect(appendWorkerReplyBelowNote("Original body", "Agent reply", "Done.")).toBe(
      "Original body\n\n## Agent reply\n\nDone.",
    );
  });

  it("uses only the section when the note is empty", () => {
    expect(appendWorkerReplyBelowNote("", "Title", "Body")).toBe("## Title\n\nBody");
  });
});

describe("buildNoteWorkerPageIdByMessageId", () => {
  it("attaches the latest note_brief page id to following agent replies", () => {
    const messages = [
      {
        id: "u1",
        type: "user",
        parts: [{ type: "note_brief", ref_id: "page-1", label: "A", text: "t" }],
      },
      { id: "a1", type: "agent", content: "first" },
      {
        id: "u2",
        type: "user",
        parts: [{ type: "note_brief", ref_id: "page-2", label: "B", text: "t" }],
      },
      { id: "a2", type: "agent", content: "second" },
      { id: "u3", type: "user", content: "plain" },
      { id: "a3", type: "agent", content: "still page-2" },
    ] as ChannelMessage[];

    const map = buildNoteWorkerPageIdByMessageId(messages);
    expect(map.get("a1")).toBe("page-1");
    expect(map.get("a2")).toBe("page-2");
    expect(map.get("a3")).toBe("page-2");
    expect(map.has("u1")).toBe(false);
  });
});

describe("noteWorkerReplyPlainText", () => {
  it("prefers text parts over content", () => {
    expect(
      noteWorkerReplyPlainText({
        content: "ignored",
        parts: [{ type: "text", text: "from parts" }],
      }),
    ).toBe("from parts");
  });
});
