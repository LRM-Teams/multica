import { I18nProvider } from "@multica/core/i18n/react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import enChannels from "../locales/en/channels.json";
import type { VoiceCallController } from "./use-voice-call-controller";

const mocks = vi.hoisted(() => ({
  useVoiceCallController: vi.fn(),
}));

vi.mock("./use-voice-call-controller", () => ({
  useVoiceCallController: mocks.useVoiceCallController,
}));

vi.mock("./voice-call-panel", () => ({
  VoiceCallPanel: ({
    open,
    durationSeconds,
    onRequestClose,
    onToggleMute,
    onHangUp,
    onRetry,
    onResumeAudio,
  }: {
    open: boolean;
    durationSeconds: number;
    onRequestClose: () => void;
    onToggleMute: () => void;
    onHangUp: () => void;
    onRetry: () => void;
    onResumeAudio: () => void;
  }) => open ? (
    <div data-testid="call-panel">
      <span>duration:{durationSeconds}</span>
      <button type="button" onClick={onRequestClose}>panel-close</button>
      <button type="button" onClick={onToggleMute}>panel-mute</button>
      <button type="button" onClick={onHangUp}>panel-hang-up</button>
      <button type="button" onClick={onRetry}>panel-retry</button>
      <button type="button" onClick={onResumeAudio}>panel-resume</button>
    </div>
  ) : null,
}));

import { DmAgentVoiceCall } from "./dm-agent-voice-call";

const TEST_RESOURCES = {
  en: { channels: enChannels },
};

function renderCall(peerType: "agent" | "user" = "agent") {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <DmAgentVoiceCall
        workspaceId="workspace-1"
        channelId="channel-1"
        peer={{
          type: peerType,
          id: peerType === "agent" ? "agent-1" : "user-1",
          name: peerType === "agent" ? "Beckham" : "Alice",
        }}
      />
    </I18nProvider>,
  );
}

function createController(
  overrides: Partial<VoiceCallController> = {},
): VoiceCallController {
  return {
    call: null,
    callId: "",
    phase: "idle",
    error: null,
    autoplayBlockedUserId: null,
    mode: null,
    toolStatus: null,
    speakerphone: false,
    start: vi.fn().mockResolvedValue("call-1"),
    hangUp: vi.fn().mockResolvedValue(undefined),
    setMuted: vi.fn().mockResolvedValue(undefined),
    resumeRemoteAudio: vi.fn().mockResolvedValue(undefined),
    setSpeakerphone: vi.fn(),
    interrupt: vi.fn(),
    ...overrides,
  };
}

describe("DmAgentVoiceCall", () => {
  let controller: VoiceCallController;

  beforeEach(() => {
    controller = createController();
    mocks.useVoiceCallController.mockImplementation(() => controller);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it("does not expose calling for human direct messages", () => {
    renderCall("user");

    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(mocks.useVoiceCallController).not.toHaveBeenCalled();
  });

  it("starts an agent call with the current DM channel", () => {
    renderCall();

    fireEvent.click(screen.getByRole("button", { name: "Call Beckham" }));

    expect(controller.start).toHaveBeenCalledWith({
      channel_id: "channel-1",
      agent_id: "agent-1",
    });
    expect(screen.getByTestId("call-panel")).toBeInTheDocument();
  });

  it("hangs up before closing an active call", async () => {
    controller = createController({ phase: "connected" });
    renderCall();
    fireEvent.click(screen.getByRole("button", { name: "Call Beckham" }));

    fireEvent.click(screen.getByRole("button", { name: "panel-close" }));

    expect(controller.hangUp).toHaveBeenCalledOnce();
    await waitFor(() => {
      expect(screen.queryByTestId("call-panel")).not.toBeInTheDocument();
    });
  });

  it("keeps a call visible when the server cannot stop it", async () => {
    controller = createController({
      phase: "failed",
      error: {
        source: "stop",
        code: "stop_failed",
        message: "stop failed",
      },
      hangUp: vi.fn().mockRejectedValue(new Error("stop failed")),
    });
    renderCall();
    fireEvent.click(screen.getByRole("button", { name: "Call Beckham" }));

    fireEvent.click(screen.getByRole("button", { name: "panel-close" }));

    await waitFor(() => expect(controller.hangUp).toHaveBeenCalledOnce());
    expect(screen.getByTestId("call-panel")).toBeInTheDocument();
  });

  it("toggles mute from the controller's current phase", () => {
    controller = createController({ phase: "muted" });
    renderCall();
    fireEvent.click(screen.getByRole("button", { name: "Call Beckham" }));

    fireEvent.click(screen.getByRole("button", { name: "panel-mute" }));

    expect(controller.setMuted).toHaveBeenCalledWith(false);
  });

  it("counts connected call duration across mute transitions", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-23T12:00:00Z"));
    controller = createController();
    const view = renderCall();
    fireEvent.click(screen.getByRole("button", { name: "Call Beckham" }));

    controller = { ...controller, phase: "connected" };
    view.rerender(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DmAgentVoiceCall
          workspaceId="workspace-1"
          channelId="channel-1"
          peer={{ type: "agent", id: "agent-1", name: "Beckham" }}
        />
      </I18nProvider>,
    );
    act(() => {
      vi.advanceTimersByTime(2_000);
    });
    expect(screen.getByText("duration:2")).toBeInTheDocument();

    controller = { ...controller, phase: "muted" };
    view.rerender(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <DmAgentVoiceCall
          workspaceId="workspace-1"
          channelId="channel-1"
          peer={{ type: "agent", id: "agent-1", name: "Beckham" }}
        />
      </I18nProvider>,
    );
    act(() => {
      vi.advanceTimersByTime(1_000);
    });

    expect(screen.getByText("duration:3")).toBeInTheDocument();
  });
});
