// @vitest-environment jsdom
import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enRuntimes from "../../locales/en/runtimes.json";

const initiateRestart = vi.hoisted(() => vi.fn());
const getRestart = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({
  api: { initiateRestart, getRestart },
}));

import { RestartSection } from "./restart-section";

function renderSection(props: Partial<React.ComponentProps<typeof RestartSection>> = {}) {
  render(
    <I18nProvider locale="en" resources={{ en: { runtimes: enRuntimes } }}>
      <RestartSection
        runtimeId="rt-1"
        isOnline
        canRestart
        {...props}
      />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("RestartSection", () => {
  it("disables the Restart button when the caller cannot restart", () => {
    renderSection({ canRestart: false });
    expect(screen.getByRole("button", { name: /restart/i })).toBeDisabled();
  });

  it("disables the Restart button when the runtime is offline", () => {
    renderSection({ isOnline: false });
    expect(screen.getByRole("button", { name: /restart/i })).toBeDisabled();
  });

  it("enables the Restart button when eligible and opens a confirm dialog", () => {
    renderSection();
    const button = screen.getByRole("button", { name: /restart/i });
    expect(button).not.toBeDisabled();
    fireEvent.click(button);
    expect(screen.getByText("Restart this daemon?")).toBeInTheDocument();
  });

  it("requests a restart and shows pending status after confirming", async () => {
    initiateRestart.mockResolvedValue({ id: "rs-1", status: "pending" });
    getRestart.mockResolvedValue({ id: "rs-1", status: "pending" });
    renderSection();

    fireEvent.click(screen.getByRole("button", { name: /restart/i }));
    fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
    await act(async () => {
      await Promise.resolve();
    });

    expect(initiateRestart).toHaveBeenCalledWith("rt-1");
    expect(screen.getByText("Restart requested...")).toBeInTheDocument();
  });

  it("polls until delivered and stops polling", async () => {
    vi.useFakeTimers();
    initiateRestart.mockResolvedValue({ id: "rs-1", status: "pending" });
    getRestart.mockResolvedValue({ id: "rs-1", status: "delivered" });
    renderSection();

    fireEvent.click(screen.getByRole("button", { name: /restart/i }));
    fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(
      screen.getByText("Restart delivered. Waiting for the daemon to reconnect..."),
    ).toBeInTheDocument();

    const callsAfterDelivered = getRestart.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(getRestart.mock.calls.length).toBe(callsAfterDelivered);
  });

  it("polls until timeout and shows a distinct error message", async () => {
    vi.useFakeTimers();
    initiateRestart.mockResolvedValue({ id: "rs-1", status: "pending" });
    getRestart.mockResolvedValue({ id: "rs-1", status: "timeout" });
    renderSection();

    fireEvent.click(screen.getByRole("button", { name: /restart/i }));
    fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
      await vi.advanceTimersByTimeAsync(2000);
    });

    expect(screen.getByText("Restart request timed out")).toBeInTheDocument();
  });

  it("shows an initiate-failure message distinct from the generic timeout label", async () => {
    initiateRestart.mockRejectedValue(new Error("network down"));
    renderSection();

    fireEvent.click(screen.getByRole("button", { name: /restart/i }));
    fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
    await act(async () => {
      await Promise.resolve();
    });

    expect(screen.getByText("Failed to request restart")).toBeInTheDocument();
  });
});
