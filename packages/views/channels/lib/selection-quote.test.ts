import { afterEach, describe, expect, it, vi } from "vitest";
import {
  buildSelectionQuoteMarkdown,
  isFinePointerViewport,
  resolveMessageSelection,
} from "./selection-quote";

describe("buildSelectionQuoteMarkdown", () => {
  it("prefixes the first line with the author and `>`", () => {
    expect(buildSelectionQuoteMarkdown("Alice", "hello world")).toBe(
      "> Alice: hello world",
    );
  });

  it("keeps `>` on every line for a multi-line selection (one quote block)", () => {
    expect(buildSelectionQuoteMarkdown("Bob", "line1\nline2\nline3")).toBe(
      "> Bob: line1\n> line2\n> line3",
    );
  });

  it("omits the author prefix when none is provided", () => {
    expect(buildSelectionQuoteMarkdown(null, "anon")).toBe("> anon");
  });

  it("omits the author prefix when the author is blank", () => {
    expect(buildSelectionQuoteMarkdown("   ", "anon")).toBe("> anon");
  });

  it("trims the author name", () => {
    expect(buildSelectionQuoteMarkdown("  Alice  ", "hi")).toBe("> Alice: hi");
  });

  it("trims a trailing blank line a triple-click selection grabs", () => {
    expect(buildSelectionQuoteMarkdown("Alice", "hi\n")).toBe("> Alice: hi");
    expect(buildSelectionQuoteMarkdown("Alice", "hi\n\n")).toBe("> Alice: hi");
  });

  it("preserves intentional internal blank lines", () => {
    expect(buildSelectionQuoteMarkdown("Alice", "a\n\nb")).toBe(
      "> Alice: a\n> \n> b",
    );
  });

  it("normalizes CRLF to LF", () => {
    expect(buildSelectionQuoteMarkdown("Alice", "a\r\nb")).toBe(
      "> Alice: a\n> b",
    );
  });

  it("still prefixes `>` on an empty selection body", () => {
    expect(buildSelectionQuoteMarkdown("Alice", "")).toBe("> Alice: ");
  });
});

describe("isFinePointerViewport", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns false on a coarse-pointer viewport", () => {
    vi.stubGlobal(
      "matchMedia",
      (query: string) =>
        ({
          matches: query.includes("coarse"),
          media: query,
          onchange: null,
          addEventListener: () => {},
          removeEventListener: () => {},
          addListener: () => {},
          removeListener: () => {},
          dispatchEvent: () => false,
        }) as unknown as MediaQueryList,
    );
    expect(isFinePointerViewport()).toBe(false);
  });

  it("returns true on a fine-pointer, wide-enough viewport", () => {
    vi.stubGlobal(
      "matchMedia",
      (query: string) =>
        ({
          matches: false,
          media: query,
          onchange: null,
          addEventListener: () => {},
          removeEventListener: () => {},
          addListener: () => {},
          removeListener: () => {},
          dispatchEvent: () => false,
        }) as unknown as MediaQueryList,
    );
    expect(isFinePointerViewport()).toBe(true);
  });
});

function rect(x: number, y: number, w: number, h: number): DOMRect {
  return {
    x,
    y,
    left: x,
    top: y,
    right: x + w,
    bottom: y + h,
    width: w,
    height: h,
    toJSON: () => ({}),
  } as DOMRect;
}

function setupMessageDOM(author = "Alice", messageId = "m-1") {
  document.body.innerHTML = "";
  const container = document.createElement("div");
  const bubble = document.createElement("div");
  bubble.setAttribute("data-message-author", author);
  if (messageId) bubble.setAttribute("data-message-id", messageId);
  const body = document.createElement("div");
  body.setAttribute("data-testid", "message-body");
  body.textContent = "hello world";
  bubble.appendChild(body);
  container.appendChild(bubble);
  document.body.appendChild(container);
  return { container, bubble, body };
}

function stubSelection(body: HTMLElement, start: number, end: number, r: DOMRect) {
  const textNode = body.firstChild as Text;
  const range = document.createRange();
  range.setStart(textNode, start);
  range.setEnd(textNode, end);
  // jsdom inherits getBoundingClientRect from the prototype (zeros); override
  // on the instance so the helper sees the test's rect.
  Object.defineProperty(range, "getBoundingClientRect", {
    value: () => r,
    configurable: true,
  });
  vi.stubGlobal("getSelection", () => ({
    isCollapsed: start === end,
    rangeCount: 1,
    toString: () => range.toString(),
    getRangeAt: () => range,
    removeAllRanges: () => {},
  }));
  return range;
}

/** Two stamped bubbles sharing one container, for cross-message tests. */
function setupTwoMessageDOM() {
  document.body.innerHTML = "";
  const container = document.createElement("div");
  const mk = (author: string, id: string, text: string) => {
    const bubble = document.createElement("div");
    bubble.setAttribute("data-message-author", author);
    bubble.setAttribute("data-message-id", id);
    const body = document.createElement("div");
    body.setAttribute("data-testid", "message-body");
    body.textContent = text;
    bubble.appendChild(body);
    container.appendChild(bubble);
    return body;
  };
  const bodyA = mk("Alice", "m-1", "first message");
  const bodyB = mk("Bob", "m-2", "second message");
  document.body.appendChild(container);
  return { container, bodyA, bodyB };
}

/** A selection whose start/end live in DIFFERENT message bodies. */
function stubCrossMessageSelection(
  bodyA: HTMLElement,
  startA: number,
  bodyB: HTMLElement,
  endB: number,
  r: DOMRect,
) {
  const range = document.createRange();
  range.setStart(bodyA.firstChild as Text, startA);
  range.setEnd(bodyB.firstChild as Text, endB);
  Object.defineProperty(range, "getBoundingClientRect", {
    value: () => r,
    configurable: true,
  });
  vi.stubGlobal("getSelection", () => ({
    isCollapsed: false,
    rangeCount: 1,
    toString: () => "cross",
    getRangeAt: () => range,
    removeAllRanges: () => {},
  }));
  return range;
}

describe("resolveMessageSelection", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  it("returns null for a collapsed selection", () => {
    const { container } = setupMessageDOM();
    expect(resolveMessageSelection({ isCollapsed: true } as Selection, container)).toBeNull();
  });

  it("returns null when there is no container", () => {
    expect(resolveMessageSelection({ isCollapsed: false } as Selection, null)).toBeNull();
  });

  it("resolves a selection inside a message body to author / text / rect", () => {
    const { container, body } = setupMessageDOM();
    stubSelection(body, 0, 5, rect(50, 100, 40, 16));
    const result = resolveMessageSelection(window.getSelection(), container);
    expect(result).not.toBeNull();
    expect(result?.text).toBe("hello");
    expect(result?.author).toBe("Alice");
    expect(result?.messageId).toBe("m-1");
    expect(result?.rect.width).toBe(40);
  });

  it("returns null when a zero-size rect is reported (caret-like)", () => {
    const { container, body } = setupMessageDOM();
    // Non-empty text but a zero-size rect (defensive: shouldn't float over it).
    stubSelection(body, 0, 5, rect(0, 0, 0, 0));
    expect(resolveMessageSelection(window.getSelection(), container)).toBeNull();
  });

  it("returns null when the selection lives outside the container", () => {
    document.body.innerHTML = "";
    const container = document.createElement("div");
    const outside = document.createElement("div");
    const outsideBubble = document.createElement("div");
    outsideBubble.setAttribute("data-message-author", "Eve");
    const outsideBody = document.createElement("div");
    outsideBody.setAttribute("data-testid", "message-body");
    outsideBody.textContent = "elsewhere";
    outsideBubble.appendChild(outsideBody);
    outside.appendChild(outsideBubble);
    document.body.append(container, outside);
    stubSelection(outsideBody, 0, 9, rect(10, 10, 50, 16));
    expect(resolveMessageSelection(window.getSelection(), container)).toBeNull();
  });

  it("omits the author when the bubble has no author stamp", () => {
    const { container, body } = setupMessageDOM("", "");
    stubSelection(body, 0, 5, rect(50, 100, 40, 16));
    const result = resolveMessageSelection(window.getSelection(), container);
    expect(result?.author).toBeNull();
    expect(result?.messageId).toBeNull();
    expect(result?.text).toBe("hello");
  });

  it("returns null for a cross-message selection (endpoints in different bodies) — AC#7", () => {
    const { container, bodyA, bodyB } = setupTwoMessageDOM();
    stubCrossMessageSelection(bodyA, 0, bodyB, 6, rect(10, 100, 220, 16));
    expect(resolveMessageSelection(window.getSelection(), container)).toBeNull();
  });
});
