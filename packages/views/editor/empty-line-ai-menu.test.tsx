/**
 * @vitest-environment happy-dom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { EmptyLineAiMenu, type EmptyLineAiState } from "./empty-line-ai-menu";

const toastSuccessMock = vi.hoisted(() => vi.fn());
const captureEventMock = vi.hoisted(() => vi.fn());
const mockInsertContentAt = vi.fn((_pos: number, _content: string) => undefined);
const mockRun = vi.fn();

function makeEditor() {
  return {
    view: { dom: document.createElement("div") },
    markdown: null,
    getMarkdown: () => "Old paragraph\n\nKeep this",
    chain: () => ({
      focus: () => ({
        insertContentAt: (pos: number, content: string) => {
          mockInsertContentAt(pos, content);
          return { run: mockRun };
        },
        command: () => ({ run: mockRun }),
      }),
    }),
    commands: {
      focus: vi.fn(),
      setContent: vi.fn(),
    },
    state: {
      doc: {
        content: { size: 10 },
        textBetween: () => "",
      },
      selection: { from: 1, to: 1 },
    },
  } as any;
}

function makeState(overrides: Partial<EmptyLineAiState> = {}): EmptyLineAiState {
  return {
    status: "prompt",
    from: 0,
    to: 2,
    caretPos: 1,
    anchorRect: new DOMRect(10, 20, 0, 0),
    instruction: "",
    ...overrides,
  };
}

vi.mock("sonner", () => ({
  toast: {
    success: toastSuccessMock,
  },
}));

vi.mock("@multica/core/analytics", () => ({
  captureEvent: captureEventMock,
}));

vi.mock("@floating-ui/dom", () => ({
  computePosition: vi.fn(async () => ({ x: 0, y: 0 })),
  offset: vi.fn(),
  flip: vi.fn(),
  shift: vi.fn(),
  autoUpdate: vi.fn(() => () => undefined),
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        page_ai: {
          trigger_label: "Edit with AI",
          hint: "hint",
          instruction_placeholder: "Edit with AI",
          send: "Send",
          cancel: "Cancel",
          result_title: "AI draft",
          action_insert: "Insert action",
          action_replace_selection: "Replace block action",
          action_replace_page: "Replace page action",
          action_patch: "Patch action",
          title_suggestion: "Suggested title:",
          copy_patch: "Copy patch",
          apply_patch: "Apply patch",
          current_fragment: "Current fragment",
          replacement_fragment: "Replacement",
          current_page: "Current page",
          proposed_page: "AI proposal",
          patch_target_missing: "Patch target missing",
          invalid_markdown: "Invalid markdown",
          applied: "AI edit applied",
          undo: "Undo",
          status_starting: "Starting",
          status_queued: "Queued",
          status_dispatched: "Dispatched",
          status_running: "Running",
          status_cancelling: "Cancelling",
          status_completed: "Completed",
          status_failed: "Failed status",
          status_cancelled: "Cancelled",
          insert: "Insert",
          replace_selection: "Replace block",
          replace_page: "Replace",
          discard: "Discard",
          failed: "Failed",
          empty_result: "Empty",
        },
      }),
  }),
}));

describe("EmptyLineAiMenu dismiss interactions", () => {
  beforeEach(() => {
    toastSuccessMock.mockClear();
    captureEventMock.mockClear();
    mockInsertContentAt.mockClear();
    mockRun.mockClear();
  });

  it("dismisses and inserts a literal space when Space is pressed on an empty prompt", () => {
    const editor = makeEditor();
    const onClose = vi.fn();
    const onChange = vi.fn();
    const onEditPageWithAI = vi.fn();

    render(
      <EmptyLineAiMenu
        editor={editor}
        state={makeState()}
        onChange={onChange}
        onEditPageWithAI={onEditPageWithAI}
        onClose={onClose}
      />,
    );

    const textarea = screen.getByRole("textbox");
    fireEvent.keyDown(textarea, { key: " ", code: "Space" });

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mockInsertContentAt).toHaveBeenCalledWith(1, " ");
    expect(mockRun).toHaveBeenCalled();
    expect(onEditPageWithAI).not.toHaveBeenCalled();
  });

  it("keeps typing spaces inside a non-empty AI instruction", () => {
    const editor = makeEditor();
    const onClose = vi.fn();

    render(
      <EmptyLineAiMenu
        editor={editor}
        state={makeState({ instruction: "rewrite" })}
        onChange={vi.fn()}
        onEditPageWithAI={vi.fn()}
        onClose={onClose}
      />,
    );

    fireEvent.keyDown(screen.getByRole("textbox"), { key: " ", code: "Space" });
    expect(onClose).not.toHaveBeenCalled();
    expect(mockInsertContentAt).not.toHaveBeenCalled();
  });

  it("closes on Escape even when focus is outside the prompt", () => {
    const onClose = vi.fn();
    render(
      <EmptyLineAiMenu
        editor={makeEditor()}
        state={makeState()}
        onChange={vi.fn()}
        onEditPageWithAI={vi.fn()}
        onClose={onClose}
      />,
    );

    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes on pointerdown outside the floating menu", () => {
    const onClose = vi.fn();
    render(
      <div>
        <button type="button" data-testid="outside">
          outside
        </button>
        <EmptyLineAiMenu
          editor={makeEditor()}
          state={makeState()}
          onChange={vi.fn()}
          onEditPageWithAI={vi.fn()}
          onClose={onClose}
        />
      </div>,
    );

    fireEvent.pointerDown(screen.getByTestId("outside"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not dismiss or abort while loading when pointerdown is outside", () => {
    const onClose = vi.fn();
    render(
      <div>
        <button type="button" data-testid="outside">
          outside
        </button>
        <EmptyLineAiMenu
          editor={makeEditor()}
          state={makeState({ status: "loading", instruction: "rewrite", jobStatus: "running" })}
          onChange={vi.fn()}
          onEditPageWithAI={vi.fn()}
          onClose={onClose}
        />
      </div>,
    );

    fireEvent.pointerDown(screen.getByTestId("outside"));
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  });

  it("applies the structured replace-page action and suggested title", () => {
    const editor = makeEditor();
    const onClose = vi.fn();
    const onApplyTitle = vi.fn();
    render(
      <EmptyLineAiMenu
        editor={editor}
        state={makeState({
          status: "review",
          result: {
            action: "replace_page",
            markdown: "# Revised page",
            title: "Revised title",
            rationale: "Whole-page rewrite requested.",
          },
        })}
        onChange={vi.fn()}
        onEditPageWithAI={vi.fn()}
        onApplyTitle={onApplyTitle}
        onClose={onClose}
      />,
    );

    expect(screen.getByText("Replace page action")).toBeInTheDocument();
    expect(screen.getByText("Whole-page rewrite requested.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Replace" }));
    expect(editor.commands.setContent).toHaveBeenCalledWith("# Revised page");
    expect(onApplyTitle).toHaveBeenCalledWith("Revised title");
    expect(captureEventMock).toHaveBeenCalledWith("note_ai_edit_apply_result", {
      surface: "page",
      outcome: "applied",
      action: "replace_page",
      title_suggested: true,
      has_patch_target: false,
      markdown_length: "# Revised page".length,
    });
    expect(toastSuccessMock).toHaveBeenCalledWith(
      "AI edit applied",
      expect.objectContaining({
        action: expect.objectContaining({ label: "Undo" }),
      }),
    );
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("undoes an applied AI edit and title suggestion from the toast action", () => {
    const editor = makeEditor();
    const onApplyTitle = vi.fn();
    render(
      <EmptyLineAiMenu
        editor={editor}
        state={makeState({
          status: "review",
          result: {
            action: "replace_page",
            markdown: "# Revised page",
            title: "Revised title",
          },
        })}
        onChange={vi.fn()}
        onEditPageWithAI={vi.fn()}
        onApplyTitle={onApplyTitle}
        currentTitle="Old title"
        onClose={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Replace" }));
    const [, options] = toastSuccessMock.mock.calls[0] as [string, { action: { onClick: () => void } }];
    options.action.onClick();

    expect(editor.commands.setContent).toHaveBeenNthCalledWith(1, "# Revised page");
    expect(editor.commands.setContent).toHaveBeenNthCalledWith(2, "Old paragraph\n\nKeep this");
    expect(onApplyTitle).toHaveBeenNthCalledWith(1, "Revised title");
    expect(onApplyTitle).toHaveBeenNthCalledWith(2, "Old title");
    expect(captureEventMock).toHaveBeenLastCalledWith("note_ai_edit_apply_result", expect.objectContaining({
      surface: "page",
      outcome: "undo_clicked",
      action: "replace_page",
    }));
  });

  it("applies structured patch by replacing only the target fragment", () => {
    const editor = makeEditor();
    const onClose = vi.fn();
    render(
      <EmptyLineAiMenu
        editor={editor}
        state={makeState({
          status: "review",
          result: {
            action: "patch",
            target: "Old paragraph",
            markdown: "New paragraph",
            rationale: "Targeted paragraph update.",
          },
        })}
        onChange={vi.fn()}
        onEditPageWithAI={vi.fn()}
        onClose={onClose}
      />,
    );

    expect(screen.getByText("Old paragraph")).toBeInTheDocument();
    expect(screen.getByText("New paragraph")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Apply patch" }));
    expect(editor.commands.setContent).toHaveBeenCalledWith("New paragraph\n\nKeep this");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not close on pointerdown inside the floating menu", () => {
    const onClose = vi.fn();
    render(
      <EmptyLineAiMenu
        editor={makeEditor()}
        state={makeState()}
        onChange={vi.fn()}
        onEditPageWithAI={vi.fn()}
        onClose={onClose}
      />,
    );

    fireEvent.pointerDown(screen.getByTestId("empty-line-ai-menu"));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("swaps the send control for a pulsing stop button while loading, without status copy", () => {
    const onChange = vi.fn();
    render(
      <EmptyLineAiMenu
        editor={makeEditor()}
        state={makeState({ status: "loading", instruction: "rewrite this page", jobStatus: "running" })}
        onChange={onChange}
        onEditPageWithAI={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(screen.queryByText("Running")).not.toBeInTheDocument();
    expect(screen.queryByText("AI is editing…")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send" })).not.toBeInTheDocument();

    const stop = screen.getByRole("button", { name: "Cancel" });
    expect(stop).toHaveAttribute("aria-busy", "true");
    expect(stop.className).toContain("animate-pulse");
    expect(screen.getByTestId("empty-line-ai-menu").style.width).toContain("720px");
  });

  it("aborts the in-flight edit when the stop button is clicked", () => {
    const onChange = vi.fn();
    let capturedSignal: AbortSignal | undefined;
    const onEditPageWithAI = vi.fn((_request, options?: { signal?: AbortSignal }) => {
      capturedSignal = options?.signal;
      return new Promise(() => undefined);
    });

    const { rerender } = render(
      <EmptyLineAiMenu
        editor={makeEditor()}
        state={makeState({ instruction: "rewrite this page" })}
        onChange={onChange}
        onEditPageWithAI={onEditPageWithAI}
        onClose={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ status: "loading" }));

    const loadingState = onChange.mock.calls[0][0] as EmptyLineAiState;
    rerender(
      <EmptyLineAiMenu
        editor={makeEditor()}
        state={loadingState}
        onChange={onChange}
        onEditPageWithAI={onEditPageWithAI}
        onClose={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ status: "loading", cancelling: true }));
    expect(capturedSignal?.aborted).toBe(true);
  });
});
