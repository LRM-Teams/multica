import { beforeEach, describe, expect, it } from "vitest";
import { useComposerDraftStore } from "./composer-draft-store";

describe("composer draft store", () => {
  beforeEach(() => {
    useComposerDraftStore.setState({ drafts: {} });
  });

  it("returns undefined for a key with no draft", () => {
    expect(useComposerDraftStore.getState().getDraft("channel:c1")).toBeUndefined();
  });

  it("setDraft stores content retrievable via getDraft", () => {
    const { setDraft, getDraft } = useComposerDraftStore.getState();

    setDraft("channel:c1", "hello world");

    expect(getDraft("channel:c1")).toBe("hello world");
  });

  it("keeps drafts for different conversations independent", () => {
    const { setDraft, getDraft } = useComposerDraftStore.getState();

    setDraft("channel:c1", "channel draft");
    setDraft("dm:d1", "dm draft");

    expect(getDraft("channel:c1")).toBe("channel draft");
    expect(getDraft("dm:d1")).toBe("dm draft");
  });

  it("clearDraft removes the entry for that key only", () => {
    const { setDraft, clearDraft, getDraft } = useComposerDraftStore.getState();

    setDraft("channel:c1", "keep typing");
    setDraft("dm:d1", "dm draft");
    clearDraft("channel:c1");

    expect(getDraft("channel:c1")).toBeUndefined();
    expect(getDraft("dm:d1")).toBe("dm draft");
  });

  it("overwriting a draft replaces the previous content", () => {
    const { setDraft, getDraft } = useComposerDraftStore.getState();

    setDraft("channel:c1", "first");
    setDraft("channel:c1", "second");

    expect(getDraft("channel:c1")).toBe("second");
  });

  it("setDraft with empty content drops the entry (no empty drafts)", () => {
    const { setDraft } = useComposerDraftStore.getState();

    setDraft("channel:c1", "hello");
    setDraft("channel:c1", "");

    expect(useComposerDraftStore.getState().drafts["channel:c1"]).toBeUndefined();
  });

  describe("LRM-801 attachment drafts", () => {
    const image = {
      attachmentId: "att-1",
      filename: "photo.png",
      contentType: "image/png",
      sizeBytes: 1234,
      previewUrl: "https://cdn.example.com/photo.png",
    };

    it("setDraftAttachments stores attachments retrievable from drafts", () => {
      const { setDraftAttachments } = useComposerDraftStore.getState();

      setDraftAttachments("channel:c1", [image]);

      expect(useComposerDraftStore.getState().drafts["channel:c1"]?.attachments).toEqual([image]);
    });

    it("text and attachments are independent halves of one draft", () => {
      const { setDraft, setDraftAttachments, clearDraftContent } =
        useComposerDraftStore.getState();

      setDraft("channel:c1", "hello");
      setDraftAttachments("channel:c1", [image]);
      // Deleting only the text keeps the images (spec: 只删字允许).
      clearDraftContent("channel:c1");

      const draft = useComposerDraftStore.getState().drafts["channel:c1"];
      expect(draft?.content).toBe("");
      expect(draft?.attachments).toEqual([image]);
    });

    it("setDraft preserves attachments; setDraftAttachments preserves text", () => {
      const { setDraft, setDraftAttachments } = useComposerDraftStore.getState();

      setDraftAttachments("channel:c1", [image]);
      setDraft("channel:c1", "new text");
      expect(useComposerDraftStore.getState().drafts["channel:c1"]?.attachments).toEqual([image]);

      setDraftAttachments("channel:c1", []);
      expect(useComposerDraftStore.getState().drafts["channel:c1"]?.content).toBe("new text");
      expect(useComposerDraftStore.getState().drafts["channel:c1"]?.attachments).toBeUndefined();
    });

    it("clearing the last remaining half removes the entry", () => {
      const { setDraft, setDraftAttachments, clearDraftContent } =
        useComposerDraftStore.getState();

      setDraftAttachments("channel:c1", [image]);
      setDraftAttachments("channel:c1", []);
      expect(useComposerDraftStore.getState().drafts["channel:c1"]).toBeUndefined();

      setDraft("channel:c1", "text only");
      clearDraftContent("channel:c1");
      expect(useComposerDraftStore.getState().drafts["channel:c1"]).toBeUndefined();
    });

    it("clearDraft (send) wipes text and attachments together", () => {
      const { setDraft, setDraftAttachments, clearDraft } = useComposerDraftStore.getState();

      setDraft("channel:c1", "hello");
      setDraftAttachments("channel:c1", [image]);
      clearDraft("channel:c1");

      expect(useComposerDraftStore.getState().drafts["channel:c1"]).toBeUndefined();
    });
  });
});
