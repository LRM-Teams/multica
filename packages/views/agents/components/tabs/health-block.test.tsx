// @vitest-environment jsdom

import { describe, it, expect } from "vitest";
import type { ComponentProps } from "react";
import { render, screen, within } from "@testing-library/react";
import type { AgentHealthEvent, AgentHealthState, AgentHealthSummary } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";
import { HealthBlockView } from "./health-block";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

// Fixed clock so relative-duration assertions are deterministic.
const NOW = new Date("2026-07-06T12:00:00Z").getTime();
const HOUR = 60 * 60 * 1000;

function summary(state: AgentHealthState, overrides: Partial<AgentHealthSummary> = {}): AgentHealthSummary {
  return {
    agent_id: "a1",
    runtime_id: null,
    state,
    reason_code: "heartbeat_received",
    state_since: new Date(NOW - 3 * HOUR).toISOString(),
    last_seen_at: "2026-07-06T09:41:00Z",
    last_event_at: "2026-07-06T09:41:00Z",
    ...overrides,
  };
}

function event(overrides: Partial<AgentHealthEvent>): AgentHealthEvent {
  return {
    id: "e1",
    agent_id: "a1",
    runtime_id: null,
    // Internal BE codes — MUST NOT surface (E5). Present here (reason_code +
    // message) to prove the zero-leak guard: copy is derived from state_after
    // only, never these fields.
    type: "daemon_liveness_probe_sent",
    state_after: "online",
    reason_code: "probe_timeout",
    message: "probe timeout from liveness daemon",
    occurred_at: "2026-07-06T09:00:00Z",
    ...overrides,
  };
}

function renderView(props: Partial<ComponentProps<typeof HealthBlockView>>) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <HealthBlockView
        summary={undefined}
        events={undefined}
        summaryLoading={false}
        eventsLoading={false}
        now={NOW}
        {...props}
      />
    </I18nProvider>,
  );
}

describe("HealthBlockView — head (LRM-248 Online/Offline live)", () => {
  it("folds reconnecting / suspected / recovered → Online on the live head", () => {
    for (const state of [
      "online",
      "recovered",
      "suspected_disconnect",
      "reconnecting",
    ] as const) {
      const { unmount } = renderView({ summary: summary(state), events: [] });
      expect(screen.getAllByText("Online").length).toBeGreaterThan(0);
      expect(screen.queryByText("Reconnecting")).not.toBeInTheDocument();
      expect(screen.queryByText("Connection unstable")).not.toBeInTheDocument();
      expect(screen.queryByText("Recovered")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("shows Offline on the live head when offline", () => {
    renderView({ summary: summary("offline"), events: [] });
    expect(screen.getAllByText("Offline").length).toBeGreaterThan(0);
  });

  it("renders state_since as a relative duration", () => {
    renderView({ summary: summary("online"), events: [] });
    expect(screen.getByText("3h")).toBeInTheDocument();
  });

  it("renders last_seen as a clock time", () => {
    renderView({ summary: summary("online"), events: [] });
    expect(screen.getByText(/Last active \d{2}:\d{2}/)).toBeInTheDocument();
  });

  it("is not silent when the summary is unavailable (API not live)", () => {
    renderView({ summary: undefined, events: [] });
    expect(
      screen.getByText("Connectivity status unavailable"),
    ).toBeInTheDocument();
  });

  it("shows a skeleton (not blank) while the summary is loading", () => {
    const { container } = renderView({ summaryLoading: true });
    expect(
      screen.queryByText("Connectivity status unavailable"),
    ).not.toBeInTheDocument();
    expect(container.querySelector(".animate-pulse")).not.toBeNull();
  });
});

describe("HealthBlockView — timeline (Iris §3c)", () => {
  it("renders events reverse-chron and keeps recovered rows", () => {
    const events = [
      event({ id: "e1", state_after: "suspected_disconnect", occurred_at: "2026-07-06T08:00:00Z" }),
      event({ id: "e2", state_after: "recovered", occurred_at: "2026-07-06T10:00:00Z" }),
      event({ id: "e3", state_after: "reconnecting", occurred_at: "2026-07-06T09:00:00Z" }),
    ];
    renderView({ summary: summary("online"), events });

    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(3);
    // Newest first: recovered (10:00) → reconnecting (09:00) → suspected (08:00)
    expect(within(items[0]!).getByText("Recovered")).toBeInTheDocument();
    expect(within(items[1]!).getByText("Reconnecting")).toBeInTheDocument();
    expect(within(items[2]!).getByText("Connection unstable")).toBeInTheDocument();
  });

  it("drops the synthetic current-state event (head shows it — no repeated badge, §3-v2)", () => {
    // Only a synthetic current-state event → timeline is empty (the head already
    // shows current state), so it renders the one-line empty note, not a card.
    renderView({
      summary: summary("online"),
      events: [event({ id: "syn", state_after: "online", synthetic: true })],
    });
    expect(screen.queryByRole("listitem")).not.toBeInTheDocument();
    expect(screen.getByText("No health events yet")).toBeInTheDocument();
  });

  it("folds consecutive same-state events into a single row (§3-v2 density)", () => {
    // Three consecutive "online" transitions collapse to ONE row; the distinct
    // offline transition stays a separate row.
    renderView({
      summary: summary("online"),
      events: [
        event({ id: "a", state_after: "online", occurred_at: "2026-07-06T11:00:00Z" }),
        event({ id: "b", state_after: "online", occurred_at: "2026-07-06T10:30:00Z" }),
        event({ id: "c", state_after: "online", occurred_at: "2026-07-06T10:00:00Z" }),
        event({ id: "d", state_after: "offline", occurred_at: "2026-07-06T09:00:00Z" }),
      ],
    });
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(within(items[0]!).getByText("Online")).toBeInTheDocument();
    expect(within(items[1]!).getByText("Offline")).toBeInTheDocument();
  });

  it("shows an explicit empty state (not silent) when there are no events", () => {
    renderView({ summary: summary("online"), events: [] });
    expect(screen.getByText("No health events yet")).toBeInTheDocument();
  });

  it("shows a skeleton (not the empty state) while events are loading", () => {
    renderView({ summary: summary("online"), eventsLoading: true });
    expect(screen.queryByText("No health events yet")).not.toBeInTheDocument();
  });
});

describe("HealthBlockView — read-only + zero-leak (E5)", () => {
  it("renders no react / quote / message / interactive affordance", () => {
    const { container } = renderView({
      summary: summary("online"),
      events: [event({ id: "e1", state_after: "recovered" })],
    });
    expect(container.querySelectorAll("button")).toHaveLength(0);
    expect(container.querySelectorAll("textarea")).toHaveLength(0);
    expect(container.querySelectorAll("input")).toHaveLength(0);
  });

  it("never surfaces internal BE event-type codes in the DOM", () => {
    const { container } = renderView({
      summary: summary("suspected_disconnect", { reason_code: "probe_timeout" }),
      events: [
        event({ id: "e1", type: "daemon_liveness_probe_sent", state_after: "suspected_disconnect" }),
        event({ id: "e2", type: "probe_timeout_reconnect", state_after: "reconnecting" }),
        event({ id: "e3", type: "transport_reconnected", state_after: "recovered" }),
        event({ id: "e4", type: "server_ping_received", state_after: "online" }),
      ],
    });
    const text = (container.textContent ?? "").toLowerCase();
    for (const leak of [
      "probe",
      "liveness",
      "daemon",
      "server_ping",
      "transport",
      "reconnect_", // guards against raw type codes; "Reconnecting" copy is fine
    ]) {
      expect(text).not.toContain(leak);
    }
  });
});

describe("HealthBlockView — mobile card (no horizontal overflow)", () => {
  it("is a full-width min-w-0 card with no forced wide / x-overflow", () => {
    const { container } = renderView({ summary: summary("online"), events: [] });
    const section = container.querySelector("section");
    expect(section).not.toBeNull();
    expect(section!.className).toContain("w-full");
    expect(section!.className).toContain("min-w-0");
    expect(section!.className).not.toContain("overflow-x");
  });
});
