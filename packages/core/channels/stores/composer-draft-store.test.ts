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
});
