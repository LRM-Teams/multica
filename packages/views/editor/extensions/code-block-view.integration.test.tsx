import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Editor } from "@tiptap/core";
import { useEditor, EditorContent } from "@tiptap/react";
// Side-effect: augments Editor with getMarkdown().
import "@tiptap/markdown";
import { createEditorExtensions } from "./index";

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        code_block: {
          copy_code: "Copy code",
          menu: "Code block menu",
          language: "Code language",
          show_preview: "Show preview",
          show_source: "Show source",
          mermaid_view: "Diagram view",
          mermaid_both: "Show diagram and source",
          mermaid_diagram: "Show diagram only",
          mermaid_source: "Show source only",
          download_diagram: "Download diagram",
          delete: "Delete",
          fullscreen: "Fullscreen",
        },
        mermaid: {
          render_error: "Unable to render Mermaid diagram.",
          rendering: "Rendering diagram…",
        },
        slash_command: {
          no_skills_configured: "",
          no_results: "",
          commands: {},
        },
      }),
  }),
}));

vi.mock("../mermaid-diagram", () => ({
  MermaidDiagram: () => <div data-testid="mermaid-diagram">diagram</div>,
}));

/** Mutable box so TS does not narrow a `let … = null` past the React callback. */
function editorBox() {
  return { current: null as Editor | null };
}

function CodeBlockEditorHarness({
  markdown,
  onEditor,
}: {
  markdown: string;
  onEditor?: (editor: Editor) => void;
}) {
  const editor = useEditor({
    extensions: createEditorExtensions({}),
    content: markdown,
    contentType: "markdown",
    editorProps: {
      attributes: { class: "rich-text-editor" },
    },
  });

  if (editor) onEditor?.(editor);
  if (!editor) return null;
  return <EditorContent editor={editor} data-testid="harness-editor" />;
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("CodeBlockView controls (TipTap integration)", () => {
  it("changes the fenced language when a language menu item is chosen", async () => {
    const user = userEvent.setup();
    const box = editorBox();

    render(
      <CodeBlockEditorHarness
        markdown={"```mermaid\ngraph TD; A-->B;\n```"}
        onEditor={(ed) => {
          box.current = ed;
        }}
      />,
    );

    const languageTrigger = await screen.findByTestId("code-block-language");
    expect(languageTrigger).toHaveTextContent("Mermaid");

    await user.click(languageTrigger);
    await user.click(await screen.findByRole("menuitem", { name: "Python" }));

    await waitFor(() => {
      expect(screen.getByTestId("code-block-language")).toHaveTextContent("Python");
    });
    expect(screen.queryByTestId("mermaid-diagram")).toBeNull();
    expect(box.current?.getMarkdown()).toMatch(/^```python/m);
  });

  it("hides the diagram when mermaid view is set to source-only", async () => {
    const user = userEvent.setup();
    render(
      <CodeBlockEditorHarness
        markdown={"```mermaid\ngraph TD; A-->B;\n```"}
      />,
    );

    expect(await screen.findByTestId("mermaid-diagram")).toBeInTheDocument();

    await user.click(screen.getByTestId("code-block-mermaid-view"));
    await user.click(await screen.findByRole("menuitem", { name: /Show source only/i }));

    await waitFor(() => {
      expect(screen.queryByTestId("mermaid-diagram")).toBeNull();
    });
  });

  it("persists mermaid diagram-only mode across markdown reload", async () => {
    const user = userEvent.setup();
    const box = editorBox();

    const { unmount } = render(
      <CodeBlockEditorHarness
        markdown={"```mermaid\ngraph TD; A-->B;\n```"}
        onEditor={(ed) => {
          box.current = ed;
        }}
      />,
    );

    expect(await screen.findByTestId("mermaid-diagram")).toBeInTheDocument();

    await user.click(screen.getByTestId("code-block-mermaid-view"));
    await user.click(await screen.findByRole("menuitem", { name: /Show diagram only/i }));

    await waitFor(() => {
      expect(document.querySelector("pre.code-block-source")).toHaveClass(
        "code-block-source-visually-hidden",
      );
    });

    const md = box.current?.getMarkdown() ?? "";
    expect(md).toMatch(/^```mermaid view=diagram\n/m);

    unmount();
    document.body.innerHTML = "";

    render(<CodeBlockEditorHarness markdown={md} />);

    expect(await screen.findByTestId("mermaid-diagram")).toBeInTheDocument();
    await waitFor(() => {
      expect(document.querySelector("pre.code-block-source")).toHaveClass(
        "code-block-source-visually-hidden",
      );
    });
  });
});
