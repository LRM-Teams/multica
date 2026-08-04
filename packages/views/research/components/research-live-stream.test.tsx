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
const TASK_UUID = "11111111-1111-4111-8111-111111111111";
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
      data: opts.generating ? { task_id: TASK_UUID } : undefined,
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

  it("AC1–4: generating keeps body class stable; shimmer on status; link not under shimmer", () => {
    mockStreamState({
      generating: true,
      text: "出处见 来源 3",
    });
    const { rerender } = render(<ResearchLiveStream sessionId={SESSION_ID} />);
    const root = screen.getByTestId("research-live-stream");
    expect(root.getAttribute("aria-live")).toBeNull();
    expect(root.getAttribute("aria-busy")).toBe("true");

    const body = Array.from(root.querySelectorAll("div")).find(
      (el) => el.className === BODY_CLASS,
    );
    expect(body, "body container with exact className").toBeTruthy();
    expect(body!.className).toBe(BODY_CLASS);
    expect(body!.className).not.toContain("animate-chat-text-shimmer");

    const statusShimmer = root.querySelector(".animate-chat-text-shimmer");
    expect(statusShimmer).toBeTruthy();
    expect(statusShimmer!.closest("[data-testid='mock-streaming-md']")).toBeNull();
    expect(statusShimmer!.textContent).toContain("Streaming");

    const link = root.querySelector("a.text-brand.underline");
    expect(link).toBeTruthy();
    expect(link!.closest(".animate-chat-text-shimmer")).toBeNull();

    // AC2: settled body className identical
    mockStreamState({
      generating: false,
      text: "出处见 来源 3",
    });
    rerender(<ResearchLiveStream sessionId={SESSION_ID} />);
    const settledRoot = screen.getByTestId("research-live-stream");
    const settledBody = Array.from(settledRoot.querySelectorAll("div")).find(
      (el) => el.className === BODY_CLASS,
    );
    expect(settledBody!.className).toBe(BODY_CLASS);
  });

  it("AC5: empty returns null; settled has no shimmer / pulse / aria-busy", () => {
    mockStreamState({ generating: false, text: "" });
    const { container, rerender } = render(
      <ResearchLiveStream sessionId={SESSION_ID} />,
    );
    expect(container.querySelector("[data-testid='research-live-stream']")).toBeNull();

    mockStreamState({ generating: false, text: "settled body" });
    rerender(<ResearchLiveStream sessionId={SESSION_ID} />);
    const root = screen.getByTestId("research-live-stream");
    expect(root.getAttribute("aria-busy")).toBeNull();
    expect(root.querySelector(".animate-chat-text-shimmer")).toBeNull();
    expect(root.querySelector(".animate-pulse")).toBeNull();
    expect(screen.getByText("Done")).toBeTruthy();
  });
});
