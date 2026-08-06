import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { CodeBlockToolbar, languageLabel } from "./code-block-view";

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
      }),
  }),
}));

vi.mock("@multica/ui/components/ui/dropdown-menu", async () => {
  const React = await import("react");
  return {
    DropdownMenu: ({
      children,
      onOpenChange,
    }: {
      children?: React.ReactNode;
      onOpenChange?: (open: boolean) => void;
    }) => {
      // Expose open so tests can pin the toolbar the same way production menus do.
      onOpenChange?.(true);
      return <div>{children}</div>;
    },
    DropdownMenuTrigger: ({
      render: renderProp,
      children,
    }: {
      render?: React.ReactElement;
      children?: React.ReactNode;
    }) => React.cloneElement(renderProp ?? <button type="button" />, {}, children),
    DropdownMenuContent: ({ children }: { children?: React.ReactNode }) => (
      <div data-testid="menu">{children}</div>
    ),
    DropdownMenuItem: ({
      children,
      onClick,
    }: {
      children?: React.ReactNode;
      onClick?: () => void;
    }) => (
      <button type="button" onClick={onClick}>
        {children}
      </button>
    ),
  };
});

vi.mock("../mermaid-diagram", () => ({
  MermaidDiagram: () => <div data-testid="mermaid-diagram" />,
}));

vi.mock("../code-block-iframe", () => ({
  CodeBlockIframe: () => <div data-testid="html-iframe" />,
}));

function renderToolbar(
  overrides: Partial<ComponentProps<typeof CodeBlockToolbar>> = {},
) {
  const props = {
    language: "markdown",
    isMermaid: false,
    isHtml: false,
    htmlView: "preview" as const,
    mermaidView: "both" as const,
    copied: false,
    mermaidActionsEnabled: false,
    onLanguageChange: vi.fn(),
    onMermaidViewChange: vi.fn(),
    onToggleHtmlView: vi.fn(),
    onCopy: vi.fn(),
    onZoom: vi.fn(),
    onDownload: vi.fn(),
    onDelete: vi.fn(),
    onMenuOpenChange: vi.fn(),
    ...overrides,
  };
  render(<CodeBlockToolbar {...props} />);
  return props;
}

describe("languageLabel", () => {
  it("capitalizes known languages for the toolbar pill", () => {
    expect(languageLabel("mermaid")).toBe("Mermaid");
    expect(languageLabel("markdown")).toBe("Markdown");
    expect(languageLabel("unknown")).toBe("Plaintext");
  });
});

describe("CodeBlockToolbar", () => {
  it("shows the simple toolbar for non-mermaid languages", () => {
    renderToolbar({ language: "markdown", isMermaid: false });

    expect(screen.getByTestId("code-block-language")).toHaveTextContent("Markdown");
    expect(screen.getByTestId("code-block-copy")).toBeInTheDocument();
    expect(screen.getByTestId("code-block-more")).toBeInTheDocument();
    expect(screen.queryByTestId("code-block-mermaid-view")).toBeNull();
    expect(screen.queryByTestId("code-block-mermaid-zoom")).toBeNull();
    expect(screen.queryByTestId("code-block-mermaid-download")).toBeNull();
  });

  it("shows mermaid view / zoom / download controls for mermaid blocks", () => {
    const props = renderToolbar({
      language: "mermaid",
      isMermaid: true,
      mermaidActionsEnabled: true,
    });

    expect(screen.getByTestId("code-block-language")).toHaveTextContent("Mermaid");
    expect(screen.getByTestId("code-block-mermaid-view")).toBeInTheDocument();
    expect(screen.getByTestId("code-block-mermaid-zoom")).toBeEnabled();
    expect(screen.getByTestId("code-block-mermaid-download")).toBeEnabled();

    fireEvent.click(screen.getAllByText("Show diagram only", { exact: false })[0]!);
    expect(props.onMermaidViewChange).toHaveBeenCalledWith("diagram");

    fireEvent.click(screen.getAllByText("Show source only", { exact: false })[0]!);
    expect(props.onMermaidViewChange).toHaveBeenCalledWith("source");

    fireEvent.click(screen.getAllByText("Show diagram and source", { exact: false })[0]!);
    expect(props.onMermaidViewChange).toHaveBeenCalledWith("both");
  });

  it("disables zoom and download when the diagram is not visible", () => {
    renderToolbar({
      language: "mermaid",
      isMermaid: true,
      mermaidView: "source",
      mermaidActionsEnabled: false,
    });

    expect(screen.getByTestId("code-block-mermaid-zoom")).toBeDisabled();
    expect(screen.getByTestId("code-block-mermaid-download")).toBeDisabled();
  });
});
