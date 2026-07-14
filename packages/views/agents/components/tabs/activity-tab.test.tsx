import type { ReactNode } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockEvents = vi.hoisted(() => ({ current: [] as { id: string }[] }));

vi.mock("./use-agent-activity-events", () => ({
  useAgentActivityEvents: () => ({ events: mockEvents.current, latest: null, isLoading: false }),
}));

vi.mock("./activity-timeline", () => ({
  ActivityTimeline: ({ events }: { events: { id: string }[] }) => (
    <div data-testid="timeline">{events.length} rows</div>
  ),
}));

vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (select: (dict: { tab_body: { activity: { jump_to_latest: string } } }) => ReactNode) =>
      select({ tab_body: { activity: { jump_to_latest: "Jump to latest" } } }),
  }),
}));

import { ActivityTab } from "./activity-tab";

// Capture the IntersectionObserver callback so a test can simulate the bottom
// sentinel scrolling in/out of view (jsdom has neither IO nor layout).
let ioCallback: (entries: { isIntersecting: boolean }[]) => void = () => {};
class MockIntersectionObserver {
  constructor(cb: (entries: { isIntersecting: boolean }[]) => void) {
    ioCallback = cb;
  }
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
  takeRecords = vi.fn(() => []);
}

const agent = { id: "a1" } as never;

beforeEach(() => {
  mockEvents.current = [];
  ioCallback = () => {};
  vi.stubGlobal("IntersectionObserver", MockIntersectionObserver);
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

const ev = (n: number) => ({ id: `e${n}` });

describe("ActivityTab scroll-to-latest (#421)", () => {
  it("lands on the newest row when the first page arrives", () => {
    mockEvents.current = [ev(1), ev(2)];
    render(<ActivityTab agent={agent} />);
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({ block: "end" });
  });

  it("shows the jump-to-latest pill only when scrolled up, and jumps on click", () => {
    mockEvents.current = [ev(1)];
    render(<ActivityTab agent={agent} />);
    // At the bottom by default → no pill.
    expect(screen.queryByRole("button", { name: /jump to latest/i })).toBeNull();

    // Sentinel scrolls out of view (reader scrolled up to read history) → pill.
    act(() => ioCallback([{ isIntersecting: false }]));
    const pill = screen.getByRole("button", { name: /jump to latest/i });
    (Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mockClear();

    fireEvent.click(pill);
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({ behavior: "smooth", block: "end" });

    // Back at the bottom → pill hidden again.
    act(() => ioCallback([{ isIntersecting: true }]));
    expect(screen.queryByRole("button", { name: /jump to latest/i })).toBeNull();
  });

  it("does NOT auto-follow appends while the reader is scrolled up", () => {
    mockEvents.current = [ev(1)];
    const { rerender } = render(<ActivityTab agent={agent} />);
    // Reader scrolls up.
    act(() => ioCallback([{ isIntersecting: false }]));
    (Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mockClear();

    // A new event appends.
    mockEvents.current = [ev(1), ev(2)];
    rerender(<ActivityTab agent={agent} />);

    expect(Element.prototype.scrollIntoView).not.toHaveBeenCalled();
  });

  it("auto-follows appends while the reader is at the bottom", () => {
    mockEvents.current = [ev(1)];
    const { rerender } = render(<ActivityTab agent={agent} />);
    act(() => ioCallback([{ isIntersecting: true }])); // at bottom
    (Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mockClear();

    mockEvents.current = [ev(1), ev(2)];
    rerender(<ActivityTab agent={agent} />);

    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({ block: "end" });
  });

  it("re-lands on the newest row when switching agents, even with the same event count", () => {
    mockEvents.current = [ev(1), ev(2)];
    const { rerender } = render(<ActivityTab agent={agent} />);
    (Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mockClear();

    // Switch to a different agent whose cached page has the SAME number of events
    // (events.length unchanged) — landing must still fire, keyed on agent.id.
    rerender(<ActivityTab agent={{ id: "a2" } as never} />);

    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({ block: "end" });
  });
});
