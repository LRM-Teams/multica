import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        code_block: {
          fullscreen: "Fullscreen",
        },
        mermaid: {
          render_error: "Unable to render Mermaid diagram.",
          rendering: "Rendering diagram…",
        },
      }),
  }),
}));

vi.mock("mermaid", () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn().mockResolvedValue({
      svg: '<svg viewBox="0 0 123 45"><g><text>mock diagram</text></g></svg>',
    }),
  },
}));

Object.defineProperty(HTMLCanvasElement.prototype, "getContext", {
  configurable: true,
  value: () => ({
    fillStyle: "#000",
    fillRect: vi.fn(),
    getImageData: () => ({ data: new Uint8ClampedArray([12, 34, 56, 255]) }),
  }),
});

const { MermaidDiagram, normalizeMermaidChart } = await import("./mermaid-diagram");

describe("normalizeMermaidChart", () => {
  it("restores Worker-escape lookalike arrows", () => {
    expect(
      normalizeMermaidChart(
        "flowchart TD\n  A --› B\n  B ==› C\n  D -.-› E\n  F ‹--› G\n  H ‹-- I\n",
      ),
    ).toBe("flowchart TD\n  A --> B\n  B ==> C\n  D -.-> E\n  F <--> G\n  H <-- I\n");
  });
});

describe("MermaidDiagram fullscreen", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it(
    "opens a dialog with a close button and dismisses on Escape",
    async () => {
      const user = userEvent.setup();
      render(<MermaidDiagram chart={"graph TD; A-->B;"} showToolbar />);

      const maximize = await screen.findByRole(
        "button",
        { name: "Fullscreen" },
        { timeout: 8000 },
      );
      await user.click(maximize);

      await waitFor(() => {
        expect(document.querySelector('[data-slot="dialog-content"]')).not.toBeNull();
      });
      // Dialog ships a visible X close control — the old custom lightbox had none.
      expect(document.querySelector('[data-slot="dialog-close"]')).not.toBeNull();

      await user.keyboard("{Escape}");

      await waitFor(() => {
        expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull();
      });
    },
    15_000,
  );
});
