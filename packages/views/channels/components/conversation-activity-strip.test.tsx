import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelActiveTask } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ConversationActivityStrip } from "./channels-page";

// #277: the strip consumes new-chain terminal rows (#388) from the same
// active-tasks poll. `no_reply` is a neutral acknowledgement (no retry);
// `failed`+`retryable` is a warning with a Retry that re-dispatches by
// `inbox_event_id`; terminal rows are never rendered as stoppable active tasks.

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

describe("ConversationActivityStrip — #277 terminal outcome rows", () => {
  it("renders no_reply as a neutral acknowledgement with no Retry", () => {
    renderStrip(
      <ConversationActivityStrip
        tasks={[task({ status: "no_reply", outcome: "no_reply", inbox_event_id: "e1" })]}
        onRetryTask={vi.fn()}
      />,
    );
    expect(screen.getByText("Acknowledged · no reply needed")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
  });

  it("renders failed+retryable with a Retry that re-dispatches by inbox_event_id", async () => {
    const onRetryTask = vi.fn();
    renderStrip(
      <ConversationActivityStrip
        tasks={[task({ status: "failed", outcome: "failed", retryable: true, inbox_event_id: "e9" })]}
        onRetryTask={onRetryTask}
      />,
    );
    expect(screen.getByText("Couldn't reply")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(onRetryTask).toHaveBeenCalledWith(expect.objectContaining({ inbox_event_id: "e9" }));
  });

  it("renders failed but NOT retryable without a Retry button", () => {
    renderStrip(
      <ConversationActivityStrip
        tasks={[task({ status: "failed", outcome: "failed", retryable: false, inbox_event_id: "e2" })]}
        onRetryTask={vi.fn()}
      />,
    );
    expect(screen.getByText("Couldn't reply")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
  });

  it("never renders a terminal row as a stoppable active task", () => {
    renderStrip(
      <ConversationActivityStrip
        tasks={[task({ status: "no_reply", outcome: "no_reply", inbox_event_id: "e1" })]}
        onStopTask={vi.fn()}
      />,
    );
    expect(screen.queryByRole("button", { name: /stop/i })).toBeNull();
  });
});
