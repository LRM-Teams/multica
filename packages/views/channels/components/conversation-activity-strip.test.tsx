import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelActiveTask } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ConversationActivityStrip } from "./channels-page";

// The strip is the "in progress" control surface ONLY: who is running now +
// Stop. Terminal outcomes (#388 no_reply / failed) and Retry live in the
// Activity tab ("what happened"), not here (Frank/Parker 2026-07-14). Multiple
// running agents collapse behind a single count + chevron so the Stop buttons
// never pile up horizontally and garble.

function renderStrip(ui: React.ReactElement) {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      {ui}
    </I18nProvider>,
  );
}

function task(over: Partial<ChannelActiveTask>): ChannelActiveTask {
  return { agent_id: "a1", agent_name: "Aria", task_id: "t1", status: "running", ...over };
}

describe("ConversationActivityStrip", () => {
  it("shows a single running agent inline with a Stop button", () => {
    renderStrip(
      <ConversationActivityStrip
        tasks={[task({ agent_name: "Aria", task_id: "t1" })]}
        onStopTask={vi.fn()}
      />,
    );
    expect(screen.getByText("Aria is preparing a reply...")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /stop/i })).toBeInTheDocument();
  });

  it("collapses multiple running agents behind a count + chevron, expanding to per-agent Stop", async () => {
    const onStopTask = vi.fn();
    renderStrip(
      <ConversationActivityStrip
        tasks={[
          task({ agent_name: "Aria", task_id: "t1" }),
          task({ agent_name: "Bo", task_id: "t2" }),
          task({ agent_name: "Cy", task_id: "t3" }),
        ]}
        onStopTask={onStopTask}
      />,
    );
    // Collapsed: one count summary, no per-agent Stop buttons stacked in a row.
    expect(screen.getByText("3 agents are preparing replies...")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /stop/i })).toBeNull();

    // Expand → one Stop per agent, agent names visible.
    await userEvent.click(
      screen.getByRole("button", { name: /3 agents are preparing replies/i }),
    );
    const stops = screen.getAllByRole("button", { name: /stop/i });
    expect(stops.length).toBe(3);
    expect(screen.getByText("Aria")).toBeInTheDocument();
    expect(screen.getByText("Bo")).toBeInTheDocument();

    await userEvent.click(stops[0]!);
    expect(onStopTask).toHaveBeenCalledWith(expect.objectContaining({ task_id: "t1" }));
  });

  it("does NOT render terminal outcomes (no_reply / failed) — those live in Activity", () => {
    renderStrip(
      <ConversationActivityStrip
        tasks={[
          task({ status: "no_reply", outcome: "no_reply", inbox_event_id: "e1" }),
          task({
            status: "failed",
            outcome: "failed",
            retryable: true,
            inbox_event_id: "e2",
            task_id: "t2",
          }),
        ]}
        onStopTask={vi.fn()}
      />,
    );
    expect(screen.queryByText("Acknowledged · no reply needed")).toBeNull();
    expect(screen.queryByText("Couldn't reply")).toBeNull();
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
    // Terminal rows are not stoppable active tasks either → nothing renders.
    expect(screen.queryByTestId("conversation-activity-strip")).toBeNull();
  });
});
