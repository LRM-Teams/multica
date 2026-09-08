import { describe, expect, it } from "vitest";
import { NOTE_PAGE_DRAG_MIME, encodeNotePageDragPayload } from "@multica/core/notes/page-ref";
import { notePageRefFromDataTransfer } from "./use-file-drop-zone";

function fakeDataTransfer(entries: Record<string, string>): DataTransfer {
  return {
    getData: (type: string) => entries[type] ?? "",
    types: Object.keys(entries),
  } as unknown as DataTransfer;
}

describe("notePageRefFromDataTransfer", () => {
  it("prefers the custom MIME payload with title", () => {
    const pageId = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11";
    const data = fakeDataTransfer({
      [NOTE_PAGE_DRAG_MIME]: encodeNotePageDragPayload({ pageId, title: "本周" }),
      "text/plain": pageId,
    });
    expect(notePageRefFromDataTransfer(data)).toEqual({ pageId, title: "本周" });
  });

  it("falls back to text/uri-list then text/plain UUID", () => {
    const pageId = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11";
    expect(
      notePageRefFromDataTransfer(
        fakeDataTransfer({ "text/uri-list": `multica-note-page:${pageId}` }),
      ),
    ).toEqual({ pageId, title: "Untitled" });
    expect(notePageRefFromDataTransfer(fakeDataTransfer({ "text/plain": pageId }))).toEqual({
      pageId,
      title: "Untitled",
    });
  });

  it("ignores ordinary plain text", () => {
    expect(notePageRefFromDataTransfer(fakeDataTransfer({ "text/plain": "hello" }))).toBeNull();
  });
});
