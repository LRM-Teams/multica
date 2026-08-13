// @vitest-environment jsdom

/**
 * LRM-1341 — live-stream shimmer must not wrap StreamingMarkdown (SC 1.4.1);
 * article must not host a second aria-live (drawer already has LRM-1225 region).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useQueries, useQuery } from "@tanstack/react-query";
import { ResearchLiveStream } from "./research-live-stream";

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

const SESSION_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const BODY_CLASS = "text-[13px] leading-relaxed text-foreground/90";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        chat: {
          streaming_from: "Fleet",
          streaming: "Streaming…",
          stream_settled: "Done",
          streaming_wait: "Generating…",
        },
      }),
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/ui/markdown", () => ({
  StreamingMarkdown: ({ content }: { content: string }) => (
    <div data-testid="mock-streaming-md">
      <a href="https://example.com/src-3" className="text-brand underline">
        {content.includes("来源") ? "来源 3" : content}
      </a>
    </div>
  ),
}));

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: vi.fn(),
    useQueries: vi.fn(),
  };
});

const useQueryMock = vi.mocked(useQuery);
const useQueriesMock = vi.mocked(useQueries);

function mockStreamState(opts: {
  generating: boolean;
  text: string;
}) {
  const wakeTitle = `research:${SESSION_ID}`;
  useQueryMock.mockReset();
  useQueriesMock.mockReset();

  let queryCall = 0;
  useQueryMock.mockImplementation((() => {
    queryCall += 1;
    if (queryCall === 1) {
      return {
        data:
          opts.generating || opts.text
            ? [{ id: "chat-wake-1", title: wakeTitle }]
            : [],
      };
    }
    // transcript
    return {
      data: opts.text
        ? [{ type: "text", content: opts.text, visibility: "default" }]
        : [],
    };
  }) as unknown as typeof useQuery);

  useQueriesMock.mockReturnValue([
    {
      // Wake path is Raft delivery pending flag (no inbox-event transcript yet).
      data: opts.generating ? { pending: true } : undefined,
    },
  ] as unknown as ReturnType<typeof useQueries>);
}

describe("ResearchLiveStream (LRM-1341)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("source: shimmer is not on StreamingMarkdown container; aria-live absent", () => {
    const src = readSrc("research-live-stream.tsx");
    // AC1 / AC3 — red-before assertions (exact tokens)
    expect(src).not.toMatch(
      /leading-relaxed text-foreground\/90[\s\S]{0,80}animate-chat-text-shimmer/,
    );
    expect(src).toMatch(
      /animate-chat-text-shimmer[\s\S]{0,120}chat\.streaming|span className=\{cn\(isGenerating && "animate-chat-text-shimmer"\)\}/,
    );
    expect(src).not.toMatch(/aria-live/);
    expect(src).toMatch(/aria-busy=\{isGenerating\s*\|\|\s*undefined\}/);
  });

  it("AC1–4: generating keeps busy + status shimmer; wait copy (no md body until transcript returns)", () => {
    mockStreamState({
      generating: true,
      text: "",
    });
    render(<ResearchLiveStream sessionId={SESSION_ID} />);
    const root = screen.getByTestId("research-live-stream");
    expect(root.getAttribute("aria-live")).toBeNull();
    expect(root.getAttribute("aria-busy")).toBe("true");

    // Wake-via-Raft stub: no inbox transcript yet → wait line, not StreamingMarkdown.
    expect(root.querySelector(`[class="${BODY_CLASS}"]`)).toBeNull();
    expect(screen.getByText("Generating…")).toBeTruthy();

    const statusShimmer = root.querySelector(".animate-chat-text-shimmer");
    expect(statusShimmer).toBeTruthy();
    expect(statusShimmer!.closest("[data-testid='mock-streaming-md']")).toBeNull();
    expect(statusShimmer!.textContent).toContain("Streaming");
    expect(root.querySelector(".animate-pulse")).toBeTruthy();
  });

  it("AC5: empty / settled-without-transcript returns null", () => {
    mockStreamState({ generating: false, text: "" });
    const { container, rerender } = render(
      <ResearchLiveStream sessionId={SESSION_ID} />,
    );
    expect(container.querySelector("[data-testid='research-live-stream']")).toBeNull();

    // Settled text cannot render until a transcript source is rewired; card stays null.
    mockStreamState({ generating: false, text: "settled body" });
    rerender(<ResearchLiveStream sessionId={SESSION_ID} />);
    expect(container.querySelector("[data-testid='research-live-stream']")).toBeNull();
  });
});
