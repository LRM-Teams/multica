import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import enChannels from "../locales/en/channels.json";
import { formatVoiceCallDuration } from "./voice-call-format";
import {
  VoiceCallPanel,
  type VoiceCallPanelProps,
} from "./voice-call-panel";

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
    mode: "rtc",
    toolStatus: null,
    speakerphone: false,
    onRequestClose: vi.fn(),
    onMinimize: vi.fn(),
    onExpand: vi.fn(),
    onToggleMute: vi.fn(),
    onToggleSpeakerphone: vi.fn(),
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
  it("renders WeChat-style fullscreen shell with mute, speakerphone, hang-up", async () => {
    const user = userEvent.setup();
    const props = renderPanel();

    expect(screen.getByTestId("voice-call-fullscreen")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Beckham" })).toBeInTheDocument();
    expect(screen.getByText("In call")).toBeInTheDocument();
    expect(screen.getByText("1:05")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Mute" }));
    await user.click(screen.getByRole("button", { name: "Speaker" }));
    await user.click(screen.getByRole("button", { name: "Hang up" }));

    expect(props.onToggleMute).toHaveBeenCalledOnce();
    expect(props.onToggleSpeakerphone).toHaveBeenCalledOnce();
    expect(props.onHangUp).toHaveBeenCalledOnce();
  });

  it("shows speakerphone for rtc mode (not duplex-only)", () => {
    renderPanel({ mode: "rtc", speakerphone: true });
    const speaker = screen.getByRole("button", { name: "Speaker" });
    expect(speaker).toHaveAttribute("aria-pressed", "true");
  });

  it("offers unmute when the local microphone is muted", async () => {
    const user = userEvent.setup();
    const props = renderPanel({ phase: "muted" });

    expect(screen.getByText("In call")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Unmute" }));

    expect(props.onToggleMute).toHaveBeenCalledOnce();
  });

  it("minimizes to a floating pip without hanging up", async () => {
    const user = userEvent.setup();
    const props = renderPanel();

    await user.click(screen.getByTestId("voice-call-minimize"));
    expect(props.onMinimize).toHaveBeenCalledOnce();
    expect(props.onHangUp).not.toHaveBeenCalled();
  });

  it("expands from the floating pip", async () => {
    const user = userEvent.setup();
    const props = renderPanel({ minimized: true });

    expect(screen.getByTestId("voice-call-pip")).toBeInTheDocument();
    expect(screen.queryByTestId("voice-call-fullscreen")).not.toBeInTheDocument();
    await user.click(screen.getByTestId("voice-call-pip"));
    expect(props.onExpand).toHaveBeenCalledOnce();
  });

  it("shows invite status while connecting", () => {
    renderPanel({ phase: "joining" });
    expect(screen.getByText("Inviting you to a voice call…")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Hang up" })).toBeInTheDocument();
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

  it("explains when the voice agent did not answer before the timeout", () => {
    renderPanel({
      phase: "failed",
      error: {
        source: "server",
        code: "provider_activation_timeout",
        message: "internal timeout detail",
      },
    });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "The voice agent did not answer in time. Try again.",
    );
    expect(screen.queryByText("internal timeout detail")).not.toBeInTheDocument();
  });

  it("shows a bounded RTC diagnostic code without exposing provider details", () => {
    renderPanel({
      phase: "failed",
      error: {
        source: "media",
        code: "provider_error",
        message: "provider response with account details",
        providerCode: "-1000",
      },
    });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "RTC diagnostic code: -1000",
    );
    expect(screen.queryByText("provider response with account details"))
      .not.toBeInTheDocument();
  });

  it("explains that an insecure origin cannot use browser voice calls", () => {
    renderPanel({
      phase: "failed",
      error: {
        source: "media",
        code: "insecure_context",
        message: "internal secure-context detail",
      },
    });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Browsers only allow microphone access over HTTPS. Open the HTTPS address and try again.",
    );
    expect(screen.queryByText("internal secure-context detail"))
      .not.toBeInTheDocument();
    expect(screen.queryByText(
      "Voice connection failed. Check microphone permission and network.",
    )).not.toBeInTheDocument();
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

  it("shows duplex tool progress alongside speakerphone", async () => {
    const user = userEvent.setup();
    const props = renderPanel({
      mode: "duplex",
      toolStatus: {
        name: "delegate_work_to_multica_agent",
        status: "started",
      },
    });

    expect(screen.getByRole("status")).toHaveTextContent(
      "Working on delegate_work_to_multica_agent…",
    );
    expect(screen.getByText("Duplex voice")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Speaker" }));
    expect(props.onToggleSpeakerphone).toHaveBeenCalledOnce();
  });
});
