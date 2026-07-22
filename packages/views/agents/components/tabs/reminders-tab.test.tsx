// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import type { Agent } from "@multica/core/types";
import type {
  RawReminderDefinition,
  RawReminderOccurrence,
} from "@multica/core/agents/reminder-view-model";
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

// This tab doesn't exercise realtime behavior directly (that's covered by a
// dedicated use-agent-reminders-realtime test) — a no-op stub just keeps the
// hook from needing a real WS provider in the tree.
vi.mock("@multica/core/realtime", () => ({
  useWSEvent: () => {},
  useWSReconnect: () => {},
}));

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

function oneShotUpcoming(overrides: Partial<RawReminderDefinition> = {}): RawReminderDefinition {
  return {
    id: "rem-1",
    title: "Ping the deploy thread",
    status: "scheduled",
    schedule_kind: "one_shot",
    next_fire_at: "2026-07-23T09:00:00Z",
    snooze_count: 0,
    anchor: {
      available: true,
      kind: "channel",
      display: "#deploys",
      href: "/acme/channels/chan-1?message=msg-1",
    },
    ...overrides,
  };
}

function recurringFired(overrides: Partial<RawReminderOccurrence> = {}): RawReminderOccurrence {
  return {
    id: "occ-1",
    reminder_id: "rem-2",
    title: "Daily standup follow-up",
    status: "fired",
    // The parent definition is STILL scheduled (it's recurring) even though
    // this row describes a past fired occurrence.
    definition_status: "scheduled",
    schedule_kind: "recurring",
    cadence: "daily@09:00",
    schedule_timezone: "Asia/Shanghai",
    cadence_scheduled_for: "2026-07-21T01:00:00Z",
    due_at: "2026-07-21T01:00:00Z",
    fired_at: "2026-07-21T01:00:00Z",
    anchor: {
      available: true,
      kind: "channel",
      display: "#standup",
      href: "/acme/channels/chan-2?message=msg-2",
    },
    ...overrides,
  };
}

function definitionsPage(definitions: RawReminderDefinition[]) {
  return { definitions, occurrences: [], limit: 20, has_more: false };
}

function occurrencesPage(occurrences: RawReminderOccurrence[], overrides: { has_more?: boolean; next_cursor?: string } = {}) {
  return { definitions: [], occurrences, limit: 20, has_more: false, ...overrides };
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
      if (params.status === "scheduled") return Promise.resolve(definitionsPage([oneShotUpcoming()]));
      return Promise.resolve(occurrencesPage([recurringFired()]));
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
        return Promise.resolve(
          definitionsPage([
            oneShotUpcoming({
              id: "rem-2",
              title: "Daily standup follow-up",
              schedule_kind: "recurring",
              cadence: "daily@09:00",
              next_fire_at: "2026-07-22T01:00:00Z",
            }),
          ]),
        );
      }
      return Promise.resolve(occurrencesPage([recurringFired()]));
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
        return Promise.resolve(
          definitionsPage([
            oneShotUpcoming({ schedule_kind: "recurring", cadence: "daily@09:00", schedule_timezone: "Asia/Tokyo" }),
          ]),
        );
      }
      return Promise.resolve(occurrencesPage([]));
    });

    renderTab();

    expect(await screen.findByText("Asia/Tokyo")).toBeInTheDocument();
  });

  it("does not show a timezone tag for a one-shot reminder (an instant, not a recurring calendar rule)", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve(definitionsPage([oneShotUpcoming({ schedule_kind: "one_shot" })]));
      }
      return Promise.resolve(occurrencesPage([]));
    });

    renderTab();

    await screen.findByText("Ping the deploy thread");
    expect(screen.queryByText("Asia/Tokyo")).toBeNull();
  });

  it("does not show a timezone tag for an interval cadence (every:*), distinct from a calendar cadence (daily/weekly)", async () => {
    // `every:*` is a zone-free elapsed interval, not a calendar rule — the
    // server never populates `schedule_timezone` for it (`reminderTimezonePtr`
    // returns nil for anything that isn't daily/weekly) — even though it IS
    // recurring (unlike the one-shot case above), it must not show a
    // timezone tag either. This is the cadence-FAMILY distinction, not a
    // blanket recurring-vs-one-shot split.
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve(
          definitionsPage([oneShotUpcoming({ schedule_kind: "recurring", cadence: "every:30m" })]),
        );
      }
      return Promise.resolve(occurrencesPage([]));
    });

    renderTab();

    await screen.findByText("Ping the deploy thread");
    expect(screen.queryByText("Asia/Tokyo")).toBeNull();
  });

  it("does not show a timezone tag or calendar cadence on a one-shot reminder that retains a hidden lifetime-locked timezone (recurring→one-shot conversion)", async () => {
    // Per the locked BE contract: converting a recurring reminder to
    // one-shot (`update --fire-at`) clears cadence/schedule_kind but RETAINS
    // the hidden timezone in the DB (so it can restore on a future re-convert
    // back to recurring). The read API may still surface that retained value
    // on an otherwise one_shot row — `schedule_kind` must be the only family
    // authority, never "is schedule_timezone present".
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve(
          definitionsPage([
            oneShotUpcoming({ schedule_kind: "one_shot", cadence: undefined, schedule_timezone: "Asia/Tokyo" }),
          ]),
        );
      }
      return Promise.resolve(occurrencesPage([]));
    });

    renderTab();

    expect(await screen.findByText("One-time")).toBeInTheDocument();
    expect(screen.queryByText("Asia/Tokyo")).toBeNull();
  });

  it("accepts a transient 'firing' definition status as-is, without coercing it to fired", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") return Promise.resolve(definitionsPage([]));
      return Promise.resolve(
        occurrencesPage([recurringFired({ definition_status: "firing" })]),
      );
    });

    renderTab();

    // Renders without throwing/falling back — the union includes "firing".
    expect(await screen.findByText("Daily standup follow-up")).toBeInTheDocument();
  });

  it("shows a safe anchor link when available, and an honest 'unavailable' marker (no leaked ids) when not", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") {
        return Promise.resolve(
          definitionsPage([
            oneShotUpcoming({ id: "rem-a", title: "Has anchor" }),
            oneShotUpcoming({
              id: "rem-b",
              title: "No anchor",
              anchor: { available: false },
            }),
          ]),
        );
      }
      return Promise.resolve(occurrencesPage([]));
    });

    renderTab();

    const anchorLink = await screen.findByRole("link", { name: "#deploys" });
    expect(anchorLink).toHaveAttribute("href", "/acme/channels/chan-1?message=msg-1");
    expect(screen.getByText("Anchor unavailable")).toBeInTheDocument();
    // Never dumps a raw id/url as a fallback label when unavailable.
    expect(screen.queryByText(/rem-b/)).toBeNull();
  });

  it("distinguishes a genuine fetch error from a real empty result", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") return Promise.reject(new Error("network down"));
      return Promise.resolve(occurrencesPage([]));
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
      if (params.status === "scheduled") return Promise.resolve(definitionsPage([oneShotUpcoming()]));
      return Promise.resolve(occurrencesPage([recurringFired()]));
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
      if (params.status === "scheduled") return Promise.resolve(definitionsPage([]));
      if (!params.cursor) {
        return Promise.resolve(
          occurrencesPage([recurringFired({ id: "occ-page1" })], { has_more: true, next_cursor: "cursor-2" }),
        );
      }
      return Promise.resolve(
        occurrencesPage([recurringFired({ id: "occ-page2", title: "Older occurrence" })]),
      );
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

  it("does not show Load more when has_more is false, even if a stale next_cursor is also present", async () => {
    // `has_more` is the locked authority — a residual/stale cursor string
    // alongside has_more:false must not surface pagination for a page that
    // has nothing after it.
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) => {
      if (params.status === "scheduled") return Promise.resolve(definitionsPage([]));
      return Promise.resolve(
        occurrencesPage([recurringFired()], { has_more: false, next_cursor: "stale-cursor" }),
      );
    });

    renderTab();

    await screen.findByText("Daily standup follow-up");
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("shows exactly one aggregated 'No reminders yet' message when BOTH Upcoming and History are genuinely empty", async () => {
    mockGetAgentReminders.mockImplementation((_agentId: string, params: { status: string }) =>
      Promise.resolve(params.status === "scheduled" ? definitionsPage([]) : occurrencesPage([])),
    );

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
      if (params.status === "scheduled") return Promise.resolve(definitionsPage([oneShotUpcoming()]));
      return Promise.resolve(occurrencesPage([]));
    });

    renderTab();

    expect(await screen.findByText("Ping the deploy thread")).toBeInTheDocument();
    expect(screen.getByText("No fired reminders yet.")).toBeInTheDocument();
    expect(screen.queryByText("No reminders yet.")).toBeNull();
  });
});
