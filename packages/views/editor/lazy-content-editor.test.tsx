import { useImperativeHandle, type Ref } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ContentEditorRef } from "./content-editor";

const insertQuoteText = vi.hoisted(() => vi.fn());

vi.mock("./content-editor", () => ({
  ContentEditor: function MockContentEditor({ ref }: { ref?: Ref<ContentEditorRef> }) {
    useImperativeHandle(ref, () => ({
      getMarkdown: () => "",
      clearContent: vi.fn(),
      focus: vi.fn(),
      blur: vi.fn(),
      uploadFile: vi.fn(),
      hasActiveUploads: () => false,
      insertText: vi.fn(),
      insertQuoteText,
      insertBlankLineAtStart: vi.fn(),
      openIssueReferences: vi.fn(),
      getSelectedText: () => "",
      insertIssueReference: vi.fn(),
      insertRunReference: vi.fn(),
      insertMarkdown: vi.fn(),
      openPageAI: () => false,
    }));
    return <div data-testid="loaded-content-editor" />;
  },
}));

import { ContentEditor } from "./lazy-content-editor";

describe("LazyContentEditor", () => {
  it("arms and replays a quote insertion requested before TipTap loads", async () => {
    const ref = { current: null as ContentEditorRef | null };
    render(<ContentEditor ref={ref} placeholder="Message" />);

    expect(screen.getByTestId("content-editor-deferred")).toBeInTheDocument();
    act(() => ref.current?.insertQuoteText("> editable quote"));

    await waitFor(() => expect(insertQuoteText).toHaveBeenCalledWith("> editable quote"));
    expect(screen.getByTestId("loaded-content-editor")).toBeInTheDocument();
  });
});
