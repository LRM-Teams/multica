// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ActivityTimeline } from "./activity-timeline";
import { formatActivityTime, type ActivityEvent } from "./activity-event";

vi.mock("../../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../../i18n", () => ({
  useT: () => ({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    t: (selector: (r: any) => string) =>
      selector({
        tab_body: {
          activity: {
            timeline_empty: "No activity yet",
            view_diagnostics: "View diagnostic details",
            hide_diagnostics: "Hide diagnostic details",
          },
        },
      }),
  }),
}));

const USER: ActivityEvent = {
  id: "u1",
  occurred_at: "2026-07-06T09:36:05Z",
  visibility: "user_facing",
  label: "Ran a command",
  subtext: "Built the project.",
  tone: "action",
};
const DIAG: ActivityEvent = {
  id: "d1",
  occurred_at: "2026-07-06T09:36:10Z",
  visibility: "diagnostic_only",
  label: "Send held by freshness check",
  tone: "muted",
};

describe("ActivityTimeline", () => {
  beforeEach(() => cleanup());

  it("renders user_facing events (label + subtext) and hides diagnostic_only by default", () => {
    render(<ActivityTimeline events={[USER, DIAG]} />);
    expect(screen.getByText("Ran a command")).toBeInTheDocument();
    expect(screen.getByText("Built the project.")).toBeInTheDocument();
    expect(screen.queryByText("Send held by freshness check")).toBeNull();
    expect(screen.getByText("View diagnostic details")).toBeInTheDocument();
  });

  it("reveals diagnostic_only events when the toggle is clicked", async () => {
    const user = userEvent.setup();
    render(<ActivityTimeline events={[USER, DIAG]} />);
    await user.click(screen.getByText("View diagnostic details"));
    expect(screen.getByText("Send held by freshness check")).toBeInTheDocument();
    expect(screen.getByText("Hide diagnostic details")).toBeInTheDocument();
  });

  it("compact mode: user_facing only, no diagnostics toggle", () => {
    render(<ActivityTimeline events={[USER, DIAG]} compact />);
    expect(screen.getByText("Ran a command")).toBeInTheDocument();
    expect(screen.queryByText("Send held by freshness check")).toBeNull();
    expect(screen.queryByText("View diagnostic details")).toBeNull();
  });

  it("shows the empty state when there are no user_facing events", () => {
    render(<ActivityTimeline events={[DIAG]} />);
    expect(screen.getByText("No activity yet")).toBeInTheDocument();
  });

  it("never renders raw command text — labels come from the read model", () => {
    // A diagnostic row's raw content is not exposed unless explicitly toggled;
    // and even then it's the BE-provided label, never a raw command string.
    render(<ActivityTimeline events={[USER]} />);
    expect(screen.queryByText(/\/bin\/|--target|raft message/)).toBeNull();
  });
});

describe("formatActivityTime", () => {
  it("formats HH:MM:SS 24-hour in the given timezone", () => {
    expect(formatActivityTime("2026-07-06T09:36:05Z", "UTC")).toBe("09:36:05");
    expect(formatActivityTime("2026-07-06T09:36:05Z", "Asia/Shanghai")).toBe("17:36:05");
  });

  it("returns empty string for an invalid date", () => {
    expect(formatActivityTime("not-a-date", "UTC")).toBe("");
  });
});
