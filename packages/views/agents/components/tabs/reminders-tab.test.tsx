// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import type { Agent } from "@multica/core/types";
import type { RawReminderRow } from "@multica/core/agents/reminder-view-model";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

const mockGetAgentReminders = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: { ...actual.api, getAgentReminders: (...args: unknown[]) => mockGetAgentReminders(...args) },
  };
});

import { RemindersTab } from "./reminders-tab";
import { ApiError } from "@multica/core/api";

const agent: Agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Agent",
  display_name: "Agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [],
  created_at: "2026-04-16T00:00:00Z",
  updated_at: "2026-04-16T00:00:00Z",
  archived_at: null,
  archived_by: null,
} as Agent;

function oneShotUpcoming(overrides: Partial<RawReminderRow> = {}): RawReminderRow {
  return {
    id: "rem-1",
    title: "Ping the deploy thread",
    status: "scheduled",
    definition_state: "scheduled",
    recurrence: null,
    timezone: "America/Los_Angeles",
    next_fire_at: "2026-07-23T09:00:00Z",
    fired_at: null,
    anchor_available: true,
    anchor_label: "#deploys",
    anchor_url: "https://multica.test/channels/deploys",
    ...overrides,
  };
}

function recurringFired(overrides: Partial<RawReminderRow> = {}): RawReminderRow {
  return {
    id: "rem-2",
    title: "Daily standup follow-up",
    status: "fired",
    // The parent definition is STILL scheduled (it's recurring) even though
    // this row describes a past fired occurrence — see the #652 note this
    // adapter is built around.
    definition_state: "scheduled",
    recurrence: "daily@09:00",
    timezone: "Asia/Shanghai",
    next_fire_at: null,
    fired_at: "2026-07-21T01:00:00Z",
    anchor_available: true,
    anchor_label: "#standup",
    anchor_url: "https://multica.test/channels/standup",
    ...overrides,
  };
}

function renderTab() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <RemindersTab agent={agent} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  mockGetAgentReminders.mockReset();
});

describe("RemindersTab (#656)", () => {
  it("renders Upcoming and History as distinct sections from a single fixture set", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve({ reminders: [oneShotUpcoming()], next_cursor: null });
      }
      return Promise.resolve({ reminders: [recurringFired()], next_cursor: null });
    });

    renderTab();

    expect(await screen.findByText("Ping the deploy thread")).toBeInTheDocument();
    expect(await screen.findByText("Daily standup follow-up")).toBeInTheDocument();
    // Distinct sections, not one merged list.
    expect(screen.getByRole("region", { name: "Upcoming" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "History" })).toBeInTheDocument();
  });

  it("does not render a recurring definition's past fired occurrence as if the reminder itself is terminally done", async () => {
    // The same recurring reminder appears in BOTH sections: still scheduled
    // (Upcoming, by next_fire_at) AND has a past fired occurrence
    // (History) — the real state this bug class targets.
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve({
          reminders: [
            oneShotUpcoming({
              id: "rem-2",
              title: "Daily standup follow-up",
              recurrence: "daily@09:00",
              next_fire_at: "2026-07-22T01:00:00Z",
            }),
          ],
          next_cursor: null,
        });
      }
      return Promise.resolve({ reminders: [recurringFired()], next_cursor: null });
    });

    renderTab();

    const upcomingSection = screen.getByRole("region", { name: "Upcoming" });
    const historySection = screen.getByRole("region", { name: "History" });
    // Same reminder title shows up in both — proves the recurring
    // definition's "still scheduled, will fire again" state and its past
    // occurrence coexist, neither one hiding or overriding the other.
    expect(await within(upcomingSection).findByText("Daily standup follow-up")).toBeInTheDocument();
    expect(await within(historySection).findByText("Daily standup follow-up")).toBeInTheDocument();
  });

  it("shows the locked schedule timezone for a recurring reminder, not the viewer's own zone", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve({
          reminders: [oneShotUpcoming({ recurrence: "daily@09:00", timezone: "Asia/Tokyo" })],
          next_cursor: null,
        });
      }
      return Promise.resolve({ reminders: [], next_cursor: null });
    });

    renderTab();

    expect(await screen.findByText("Asia/Tokyo")).toBeInTheDocument();
  });

  it("does not show a timezone tag for a one-shot reminder (an instant, not a recurring calendar rule)", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve({
          reminders: [oneShotUpcoming({ recurrence: null, timezone: "Asia/Tokyo" })],
          next_cursor: null,
        });
      }
      return Promise.resolve({ reminders: [], next_cursor: null });
    });

    renderTab();

    await screen.findByText("Ping the deploy thread");
    expect(screen.queryByText("Asia/Tokyo")).toBeNull();
  });

  it("does not show a timezone tag for an interval cadence (every:*), distinct from a calendar cadence (daily/weekly)", async () => {
    // `every:*` is a zone-free elapsed interval, not a calendar rule — even
    // though it IS recurring (unlike the one-shot case above), it must not
    // show a timezone tag either. This is the cadence-FAMILY distinction,
    // not a blanket recurring-vs-one-shot split.
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve({
          reminders: [oneShotUpcoming({ recurrence: "every:30m", timezone: "Asia/Tokyo" })],
          next_cursor: null,
        });
      }
      return Promise.resolve({ reminders: [], next_cursor: null });
    });

    renderTab();

    await screen.findByText("Ping the deploy thread");
    expect(screen.queryByText("Asia/Tokyo")).toBeNull();
  });

  it("shows a safe anchor link when available, and an honest 'unavailable' marker (no leaked ids) when not", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve({
          reminders: [
            oneShotUpcoming({ id: "rem-a", title: "Has anchor" }),
            oneShotUpcoming({
              id: "rem-b",
              title: "No anchor",
              anchor_available: false,
              anchor_label: null,
              anchor_url: null,
            }),
          ],
          next_cursor: null,
        });
      }
      return Promise.resolve({ reminders: [], next_cursor: null });
    });

    renderTab();

    const anchorLink = await screen.findByRole("link", { name: "#deploys" });
    expect(anchorLink).toHaveAttribute("href", "https://multica.test/channels/deploys");
    expect(screen.getByText("Anchor unavailable")).toBeInTheDocument();
    // Never dumps a raw id/url as a fallback label when unavailable.
    expect(screen.queryByText(/rem-b/)).toBeNull();
  });

  it("distinguishes a genuine fetch error from a real empty result", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") return Promise.reject(new Error("network down"));
      return Promise.resolve({ reminders: [], next_cursor: null });
    });

    renderTab();

    // Upcoming: real error -> error state with Retry, NOT "No upcoming reminders."
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByText("No upcoming reminders.")).toBeNull();
    // History: genuinely empty (resolved with []) -> the real empty copy, not an error.
    expect(await screen.findByText("No fired reminders yet.")).toBeInTheDocument();
  });

  it("shows the inaccessible state (not a generic error, not an empty state) on a 403", async () => {
    mockGetAgentReminders.mockRejectedValue(new ApiError("forbidden", 403, "Forbidden"));

    renderTab();

    expect(
      await screen.findByText("You don't have permission to view this agent's reminders."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByText("No upcoming reminders.")).toBeNull();
  });

  it("exposes zero mutation affordances anywhere in the tab", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve({ reminders: [oneShotUpcoming()], next_cursor: null });
      }
      return Promise.resolve({ reminders: [recurringFired()], next_cursor: null });
    });

    renderTab();
    await screen.findByText("Ping the deploy thread");

    // The only buttons anywhere are non-mutating: Retry (only when errored,
    // not present here) and Load more (pagination). Assert by name — no
    // Schedule/Snooze/Update/Cancel/Dismiss button, menu, or form control.
    const buttons = screen.queryAllByRole("button");
    for (const button of buttons) {
      expect(button.textContent).not.toMatch(/schedule|snooze|update|cancel|dismiss/i);
    }
    expect(screen.queryByRole("form")).toBeNull();
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("paginates History via cursor with a Load more control, not an unbounded auto-fetch", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string; cursor?: string }) => {
      if (params.status === "scheduled") return Promise.resolve({ reminders: [], next_cursor: null });
      if (!params.cursor) {
        return Promise.resolve({
          reminders: [recurringFired({ id: "rem-page1" })],
          next_cursor: "cursor-2",
        });
      }
      return Promise.resolve({
        reminders: [recurringFired({ id: "rem-page2", title: "Older occurrence" })],
        next_cursor: null,
      });
    });

    renderTab();

    await screen.findByText("Daily standup follow-up");
    expect(screen.queryByText("Older occurrence")).toBeNull();

    const loadMore = screen.getByRole("button", { name: "Load more" });
    loadMore.click();

    expect(await screen.findByText("Older occurrence")).toBeInTheDocument();
    expect(mockGetAgentReminders).toHaveBeenCalledWith(
      "agent-1",
      expect.objectContaining({ status: "fired", cursor: "cursor-2" }),
    );
  });

  it("shows exactly one aggregated 'No reminders yet' message when BOTH Upcoming and History are genuinely empty", async () => {
    mockGetAgentReminders.mockResolvedValue({ reminders: [], next_cursor: null });

    renderTab();

    expect(await screen.findByText("No reminders yet.")).toBeInTheDocument();
    // Not the two separate per-section empty texts — one honest message,
    // not a redundant stack saying the same thing twice.
    expect(screen.queryByText("No upcoming reminders.")).toBeNull();
    expect(screen.queryByText("No fired reminders yet.")).toBeNull();
    // And not the individual section headings/regions either — the
    // aggregate replaces the whole two-section layout, it isn't nested
    // inside it.
    expect(screen.queryByRole("region", { name: "Upcoming" })).toBeNull();
    expect(screen.queryByRole("region", { name: "History" })).toBeNull();
  });

  it("shows only History's own specific empty copy when Upcoming has real content (not the aggregate message)", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve({ reminders: [oneShotUpcoming()], next_cursor: null });
      }
      return Promise.resolve({ reminders: [], next_cursor: null });
    });

    renderTab();

    expect(await screen.findByText("Ping the deploy thread")).toBeInTheDocument();
    expect(screen.getByText("No fired reminders yet.")).toBeInTheDocument();
    expect(screen.queryByText("No reminders yet.")).toBeNull();
  });
});
