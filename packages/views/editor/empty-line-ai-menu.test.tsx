/**
 * @vitest-environment happy-dom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { EmptyLineAiMenu, type EmptyLineAiState } from "./empty-line-ai-menu";

const mockInsertContentAt = vi.fn((_pos: number, _content: string) => undefined);
const mockRun = vi.fn();

function makeEditor() {
  return {
    view: { dom: document.createElement("div") },
    markdown: null,
    getMarkdown: () => "",
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
          result_title: "AI draft",
          insert: "Insert",
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
});
