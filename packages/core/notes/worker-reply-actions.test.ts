import { describe, expect, it } from "vitest";
import type { ChannelMessage } from "../types";
import {
  appendWorkerReplyBelowNote,
  buildNoteWorkerPageIdByMessageId,
  buildNoteWriteConfirmationByMessageId,
  deriveNoteWorkerReplyTitle,
  extractNotePageIdsFromText,
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
  it("does not treat ordinary replies after note_brief as write proposals", () => {
    const messages = [
      {
        id: "u1",
        type: "user",
        parts: [{ type: "note_brief", ref_id: "page-1", label: "A", text: "t" }],
      },
      { id: "a1", type: "agent", content: "working on it" },
    ] as ChannelMessage[];

    expect(buildNoteWorkerPageIdByMessageId(messages).has("a1")).toBe(false);
  });

  it("uses the latest sticky note_brief when the agent proposes a write", () => {
    const messages = [
      {
        id: "u1",
        type: "user",
        parts: [{ type: "note_brief", ref_id: "page-1", label: "A", text: "t" }],
      },
      { id: "a1", type: "agent", content: "first", parts: [{ type: "note_write" }] },
      {
        id: "u2",
        type: "user",
        parts: [{ type: "note_brief", ref_id: "page-2", label: "B", text: "t" }],
      },
      { id: "a2", type: "agent", content: "second", parts: [{ type: "note_write" }] },
      { id: "u3", type: "user", content: "plain" },
      { id: "a3", type: "agent", content: "chat only" },
    ] as ChannelMessage[];

    const map = buildNoteWorkerPageIdByMessageId(messages);
    expect(map.get("a1")).toBe("page-1");
    expect(map.get("a2")).toBe("page-2");
    expect(map.has("a3")).toBe(false);
    expect(map.has("u1")).toBe(false);
  });
});

describe("extractNotePageIdsFromText", () => {
  it("pulls note page ids from /notes/<uuid> links", () => {
    expect(
      extractNotePageIdsFromText(
        "see https://app.example/acme/notes/11111111-1111-1111-1111-111111111111 please",
      ),
    ).toEqual(["11111111-1111-1111-1111-111111111111"]);
  });
});

describe("buildNoteWriteConfirmationByMessageId", () => {
  it("marks an unspecified note_write as create", () => {
    const messages = [
      { id: "u1", type: "user", content: "write this as a note" },
      { id: "a1", type: "agent", content: "Draft body", parts: [{ type: "note_write" }] },
    ] as ChannelMessage[];

    const map = buildNoteWriteConfirmationByMessageId(messages);
    expect(map.get("a1")).toEqual({ mode: "create" });
  });

  it("uses --note-page-id on the write part for insert/child actions", () => {
    const messages = [
      {
        id: "a1",
        type: "agent",
        content: "Draft body",
        parts: [{ type: "note_write", ref_id: "page-9" }],
      },
    ] as ChannelMessage[];

    expect(buildNoteWriteConfirmationByMessageId(messages).get("a1")).toEqual({
      mode: "existing",
      pageId: "page-9",
    });
  });

  it("prefers a sticky note_brief over an unspecified note_write", () => {
    const messages = [
      {
        id: "u1",
        type: "user",
        parts: [{ type: "note_brief", ref_id: "page-1", label: "A", text: "t" }],
      },
      { id: "a1", type: "agent", content: "Draft", parts: [{ type: "note_write" }] },
    ] as ChannelMessage[];

    expect(buildNoteWriteConfirmationByMessageId(messages).get("a1")).toEqual({
      mode: "existing",
      pageId: "page-1",
    });
  });

  it("reads a note URL from the preceding user message when proposing a write", () => {
    const messages = [
      {
        id: "u1",
        type: "user",
        content: "put this in /notes/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      },
      { id: "a1", type: "agent", content: "Draft", parts: [{ type: "note_write" }] },
    ] as ChannelMessage[];

    expect(buildNoteWriteConfirmationByMessageId(messages).get("a1")).toEqual({
      mode: "existing",
      pageId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    });
  });

  it("does not show create on ordinary agent replies", () => {
    const messages = [
      { id: "u1", type: "user", content: "hello" },
      { id: "a1", type: "agent", content: "hi" },
    ] as ChannelMessage[];

    expect(buildNoteWriteConfirmationByMessageId(messages).has("a1")).toBe(false);
  });

  it("does not show buttons on a one-line ack after a write-note request", () => {
    const messages = [
      { id: "u1", type: "user", content: "再试试，看看能不能写入笔记" },
      { id: "a1", type: "agent", content: "好的，我先确认要写什么。" },
    ] as ChannelMessage[];

    expect(buildNoteWriteConfirmationByMessageId(messages).has("a1")).toBe(false);
  });

  it("shows create when the human asked to insert and the reply looks like the payload", () => {
    const messages = [
      { id: "u1", type: "user", content: "插入笔记" },
      {
        id: "a1",
        type: "agent",
        content: "《西江月·秋思》\n\n金风剪水芦花白，一笛渔村晚照残。\n霜冷横塘人独立，半窗灯火入秋寒。",
      },
    ] as ChannelMessage[];

    expect(buildNoteWriteConfirmationByMessageId(messages).get("a1")).toEqual({ mode: "create" });
  });

  it("shows create when the human asked for the confirm button", () => {
    const messages = [
      { id: "u1", type: "user", content: "你没写进去，给我按钮，你不用自己写入" },
      {
        id: "a1",
        type: "agent",
        content:
          "我理解了。你点下面的按钮即可：\n\n《西江月·秋思》\n\n金风剪水芦花白，一笛渔村晚照残。",
      },
    ] as ChannelMessage[];

    expect(buildNoteWriteConfirmationByMessageId(messages).get("a1")).toEqual({ mode: "create" });
  });

  it("does not show create after a poem request that did not ask to save a note", () => {
    const messages = [
      { id: "u1", type: "user", content: "写首《西江月》，以秋天为主题" },
      {
        id: "a1",
        type: "agent",
        content: "《西江月·秋思》\n\n金风剪水芦花白，一笛渔村晚照残。\n霜冷横塘人独立，半窗灯火入秋寒。",
      },
    ] as ChannelMessage[];

    expect(buildNoteWriteConfirmationByMessageId(messages).has("a1")).toBe(false);
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
