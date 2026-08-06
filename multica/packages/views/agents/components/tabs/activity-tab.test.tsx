import type { ReactNode } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockEvents = vi.hoisted(() => ({ current: [] as { id: string }[] }));
const mockPaging = vi.hoisted(() => ({
  loadOlder: vi.fn(),
  hasOlder: false,
  isLoadingOlder: false,
  isLoading: false,
  isError: false,
  refetch: vi.fn(),
  latest: null as null | { id: string; occurred_at: string; activity_kind: string },
}));

vi.mock("./use-agent-activity-events", () => ({
  useAgentActivityEvents: () => ({
    events: mockEvents.current,
    latest: mockPaging.latest,
    isLoading: mockPaging.isLoading,
    isError: mockPaging.isError,
    refetch: mockPaging.refetch,
    loadOlder: mockPaging.loadOlder,
    hasOlder: mockPaging.hasOlder,
    isLoadingOlder: mockPaging.isLoadingOlder,
  }),
}));

vi.mock("./activity-timeline", () => ({
  ActivityTimeline: ({
    events,
    isLoading,
    isError,
  }: {
    events: { id: string }[];
    isLoading?: boolean;
    isError?: boolean;
  }) => (
    <div
      data-testid="timeline"
      data-loading={String(!!isLoading)}
      data-error={String(!!isError)}
    >
      {events.length} rows
    </div>
  ),
}));

vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (select: (dict: { tab_body: { activity: { jump_to_latest: string } } }) => ReactNode) =>
      select({ tab_body: { activity: { jump_to_latest: "Jump to latest" } } }),
  }),
}));

import { ActivityTab } from "./activity-tab";

// Capture each sentinel's IntersectionObserver callback so a test can simulate
// scrolling (jsdom has neither IO nor layout). The top sentinel (older-page
// load) uses a "200px…" rootMargin; the bottom sentinel (follow / jump pill)
// uses "0px 0px 40px…". `ioCallback` stays the bottom one so existing tests are
// unchanged.
let ioCallback: (entries: { isIntersecting: boolean }[]) => void = () => {};
let ioTopCallback: (entries: { isIntersecting: boolean }[]) => void = () => {};
class MockIntersectionObserver {
  constructor(
    cb: (entries: { isIntersecting: boolean }[]) => void,
    options?: { rootMargin?: string },
  ) {
    if (options?.rootMargin?.startsWith("200px")) ioTopCallback = cb;
    else ioCallback = cb;
  }
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
  takeRecords = vi.fn(() => []);
}

const agent = { id: "a1", display_name: "Beckham", avatar_url: null } as never;

beforeEach(() => {
  mockEvents.current = [];
  ioCallback = () => {};
  ioTopCallback = () => {};
  mockPaging.loadOlder.mockClear();
  mockPaging.refetch.mockClear();
  mockPaging.hasOlder = false;
  mockPaging.isLoadingOlder = false;
  mockPaging.isLoading = false;
  mockPaging.isError = false;
  mockPaging.latest = null;
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

describe("ActivityTab older-page loading (#620)", () => {
  it("loads an older page when the reader scrolls to the top and older pages remain", () => {
    mockEvents.current = [ev(1), ev(2)];
    mockPaging.hasOlder = true;
    render(<ActivityTab agent={agent} />);

    // Top sentinel scrolls into view (reader reached the top of loaded history).
    act(() => ioTopCallback([{ isIntersecting: true }]));
    expect(mockPaging.loadOlder).toHaveBeenCalledTimes(1);
  });

  it("does not load an older page when none remain", () => {
    mockEvents.current = [ev(1)];
    mockPaging.hasOlder = false;
    render(<ActivityTab agent={agent} />);

    act(() => ioTopCallback([{ isIntersecting: true }]));
    expect(mockPaging.loadOlder).not.toHaveBeenCalled();
  });

  it("does not re-load while an older page is already fetching", () => {
    mockEvents.current = [ev(1), ev(2)];
    mockPaging.hasOlder = true;
    mockPaging.isLoadingOlder = true;
    render(<ActivityTab agent={agent} />);

    act(() => ioTopCallback([{ isIntersecting: true }]));
    expect(mockPaging.loadOlder).not.toHaveBeenCalled();
  });
});

describe("ActivityTab no page header + four states (LRM-618 / LRM-571 lock C)", () => {
  it("does not render the Activity page header row (avatar + name + status)", () => {
    mockEvents.current = [ev(1)];
    mockPaging.latest = {
      id: "e1",
      occurred_at: new Date().toISOString(),
      activity_kind: "task_completed",
    };
    render(<ActivityTab agent={agent} />);
    expect(screen.getByTestId("activity-tab")).toBeInTheDocument();
    expect(screen.queryByTestId("activity-tab-header")).toBeNull();
    expect(screen.queryByTestId("activity-tab-latest-status")).toBeNull();
    expect(screen.queryByText("Beckham")).toBeNull();
    expect(screen.getByTestId("timeline")).toBeInTheDocument();
  });

  it("passes loading to the timeline on first paint with no rows", () => {
    mockPaging.isLoading = true;
    mockEvents.current = [];
    render(<ActivityTab agent={agent} />);
    expect(screen.getByTestId("timeline")).toHaveAttribute("data-loading", "true");
  });

  it("passes error (not empty) when the query failed with no rows", () => {
    mockPaging.isError = true;
    mockEvents.current = [];
    render(<ActivityTab agent={agent} />);
    expect(screen.getByTestId("timeline")).toHaveAttribute("data-error", "true");
    expect(screen.getByTestId("timeline")).toHaveAttribute("data-loading", "false");
  });
});
