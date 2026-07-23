import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import enChannels from "../locales/en/channels.json";
import {
  formatVoiceCallDuration,
  VoiceCallPanel,
  type VoiceCallPanelProps,
} from "./voice-call-panel";

vi.mock("@multica/ui/components/ui/dialog", () => ({
  Dialog: ({
    children,
    open,
  }: {
    children: ReactNode;
    open: boolean;
  }) => open ? <div>{children}</div> : null,
  DialogContent: ({ children }: { children: ReactNode }) => (
    <div>{children}</div>
  ),
  DialogTitle: ({ children }: { children: ReactNode }) => (
    <h1>{children}</h1>
  ),
  DialogDescription: ({ children }: { children: ReactNode }) => (
    <p>{children}</p>
  ),
}));

vi.mock("../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <div data-testid="agent-avatar">{actorId}</div>
  ),
}));

const TEST_RESOURCES = {
  en: { channels: enChannels },
};

function renderPanel(overrides: Partial<VoiceCallPanelProps> = {}) {
  const props: VoiceCallPanelProps = {
    open: true,
    agentId: "agent-1",
    agentName: "Beckham",
    phase: "connected",
    error: null,
    durationSeconds: 65,
    autoplayBlocked: false,
    onRequestClose: vi.fn(),
    onToggleMute: vi.fn(),
    onHangUp: vi.fn(),
    onRetry: vi.fn(),
    onResumeAudio: vi.fn(),
    ...overrides,
  };

  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <VoiceCallPanel {...props} />
    </I18nProvider>,
  );

  return props;
}

describe("formatVoiceCallDuration", () => {
  it.each([
    [0, "0:00"],
    [65, "1:05"],
    [65.9, "1:05"],
    [-1, "0:00"],
    [Number.NaN, "0:00"],
  ])("formats %s as %s", (seconds, expected) => {
    expect(formatVoiceCallDuration(seconds)).toBe(expected);
  });
});

describe("VoiceCallPanel", () => {
  it("renders the connected call and invokes mute and hang-up actions", async () => {
    const user = userEvent.setup();
    const props = renderPanel();

    expect(screen.getByRole("heading", {
      name: "Voice call with Beckham",
    })).toBeInTheDocument();
    expect(screen.getByText("Connected · 1:05")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Mute" }));
    await user.click(screen.getByRole("button", { name: "Hang up" }));

    expect(props.onToggleMute).toHaveBeenCalledOnce();
    expect(props.onHangUp).toHaveBeenCalledOnce();
  });

  it("offers unmute when the local microphone is muted", async () => {
    const user = userEvent.setup();
    const props = renderPanel({ phase: "muted" });

    expect(screen.getByText("Microphone off · 1:05")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Unmute" }));

    expect(props.onToggleMute).toHaveBeenCalledOnce();
  });

  it("keeps a failed server call visible so hang-up can be retried", async () => {
    const user = userEvent.setup();
    const props = renderPanel({
      phase: "failed",
      error: {
        source: "stop",
        code: "stop_failed",
        message: "provider details must not be exposed",
      },
    });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "The call could not be ended. Try hanging up again.",
    );
    expect(screen.queryByText("provider details must not be exposed"))
      .not.toBeInTheDocument();
    expect(screen.getByText("Try hang up again")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Hang up" }));
    expect(props.onHangUp).toHaveBeenCalledOnce();
  });

  it("shows a retry action for a media failure without exposing raw errors", async () => {
    const user = userEvent.setup();
    const props = renderPanel({
      phase: "failed",
      error: {
        source: "media",
        code: "permission_denied",
        message: "internal browser error",
      },
    });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Voice connection failed. Check microphone permission and network.",
    );
    expect(screen.queryByText("internal browser error")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(props.onRetry).toHaveBeenCalledOnce();
  });

  it("lets the user resume browser-blocked remote audio", async () => {
    const user = userEvent.setup();
    const props = renderPanel({ autoplayBlocked: true });

    expect(screen.getByText(
      "Your browser paused Beckham's audio.",
    )).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Play audio" }));

    expect(props.onResumeAudio).toHaveBeenCalledOnce();
  });
});
