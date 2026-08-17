import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useCommentDraftStore } from "@multica/core/issues/stores";
import type { Attachment } from "@multica/core/types";
import type { ContentEditorRef } from "../../editor";
import { useCommentComposer } from "./use-comment-composer";

const uploadWithToast = vi.fn();
let triggerAgents: Array<{ id: string; name: string; source: string; reason: string }> = [];

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast }),
}));

vi.mock("../../editor", () => ({
  useFileDropZone: () => ({ isDragOver: false, dropZoneProps: {} }),
}));

vi.mock("./use-comment-trigger-preview", () => ({
  useCommentTriggerPreview: () => ({ agents: triggerAgents }),
}));

function editorRef(markdown: string): ContentEditorRef {
  return {
    getMarkdown: vi.fn(() => markdown),
    clearContent: vi.fn(),
    uploadFile: vi.fn(),
    focus: vi.fn(),
    insertContent: vi.fn(),
  } as unknown as ContentEditorRef;
}

describe("useCommentComposer", () => {
  beforeEach(() => {
    triggerAgents = [];
    uploadWithToast.mockReset();
    useCommentDraftStore.setState({ drafts: {} });
  });

  it("hydrates and persists a keyed draft", () => {
    const draftKey = "new:issue-1" as const;
    useCommentDraftStore.getState().setDraft(draftKey, "saved draft");

    const { result } = renderHook(() =>
      useCommentComposer({
        issueId: "issue-1",
        draftKey,
        onSubmit: vi.fn(),
      }),
    );

    expect(result.current.editor.defaultValue).toBe("saved draft");
    expect(result.current.isEmpty).toBe(false);

    act(() => result.current.editor.onUpdate("updated draft"));
    expect(useCommentDraftStore.getState().getDraft(draftKey)).toBe(
      "updated draft",
    );

    act(() => result.current.editor.onUpdate("  "));
    expect(useCommentDraftStore.getState().getDraft(draftKey)).toBeUndefined();
    expect(result.current.isEmpty).toBe(true);
  });

  it("submits active attachments and suppressed triggers, then resets", async () => {
    const draftKey = "reply:issue-1:comment-1" as const;
    const attachment = {
      id: "attachment-1",
      url: "https://files.example.test/attachment-1",
    } as Attachment;
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const editor = editorRef("  body attachment:attachment-1\n\n");
    triggerAgents = [
      { id: "agent-1", name: "Agent", source: "mention_agent", reason: "" },
    ];
    uploadWithToast.mockResolvedValue(attachment);

    const { result } = renderHook(() =>
      useCommentComposer({
        issueId: "issue-1",
        parentId: "comment-1",
        draftKey,
        onSubmit,
      }),
    );
    result.current.editor.ref.current = editor;

    await act(async () => {
      await result.current.editor.onUploadFile(new File(["x"], "x.txt"));
    });
    act(() => result.current.triggers.onToggle("agent-1"));
    await act(async () => result.current.editor.onSubmit());

    expect(onSubmit).toHaveBeenCalledWith(
      "body attachment:attachment-1",
      ["attachment-1"],
      ["agent-1"],
    );
    expect(editor.clearContent).toHaveBeenCalledOnce();
    expect(result.current.editor.attachments).toEqual([]);
    expect(result.current.triggers.suppressedAgentIds.size).toBe(0);
    expect(result.current.isEmpty).toBe(true);
  });
});
